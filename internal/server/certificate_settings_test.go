package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/config"
	"github.com/OXeu/configcenter/sdk/configcenter"
)

func settingsFlows(t *testing.T, local bool) (*flows, *Server) {
	t.Helper()
	f, s := certificateFlows(t)
	providerAdmin(t, f, s)
	if local {
		root, err := repoRoot()
		if err != nil {
			t.Fatal(err)
		}
		directory := filepath.Join(root, ".tmp", strings.ReplaceAll(t.Name(), "/", "-"))
		t.Cleanup(func() {
			if err := os.RemoveAll(directory); err != nil {
				t.Error(err)
			}
		})
		client, err := configcenter.New(configcenter.Config{LocalPath: filepath.Join(directory, "settings.kv")})
		if err != nil {
			t.Fatal(err)
		}
		providers, err := s.cfg.Remote.Providers(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		s.cfg.Remote = certconfig.NewRemote(client)
		if err := s.cfg.Remote.Write(t.Context(), "certcenter.providers", providers); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultServer()
	cfg.Remote, cfg.ConfigCenterURL = s.cfg.Remote, s.cfg.ConfigCenterURL
	cfg.EncryptionKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cfg.SessionSecret = "session-secret-do-not-expose"
	cfg.DatabaseDSN = "file:private-database-path?mode=memory&cache=shared"
	cfg.PublicURL = "https://cert.example.com"
	cfg.Bootstrap = config.Bootstrap{AdminEmail: "admin@example.com", AdminName: "Admin"}
	cfg.OAuth = auth.Config{IssuerURL: "https://id.example.com", ClientID: "certcenter", ClientSecret: "oauth-secret-do-not-expose", Scopes: []string{"openid", "email"}, GroupsClaim: "groups"}
	if err := cfg.Remote.Write(t.Context(), "certcenter.server", cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	s.cfg = cfg
	return f, s
}

func settingsView(t *testing.T, f *flows) map[string]any {
	t.Helper()
	status, body := f.do(http.MethodGet, "/api/v1/certificate-settings", nil)
	if status != http.StatusOK {
		t.Fatalf("settings read: %d %+v", status, body)
	}
	return body
}

func settingsWrite(f *flows, section string, version, values any) (status int, body map[string]any) {
	return f.do(http.MethodPut, "/api/v1/certificate-settings/"+section, map[string]any{"version": version, "values": values})
}

func TestSettingsPermissionsCSRFAndRedaction(t *testing.T) {
	f, s := settingsFlows(t, false)
	body := settingsView(t, f)
	if body["restart_required"] != false {
		t.Fatal("unchanged settings require restart", body)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{s.cfg.EncryptionKey, s.cfg.SessionSecret, s.cfg.DatabaseDSN, s.cfg.OAuth.ClientSecret, "do-not-expose", "secret-id", "cloud-secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("settings response disclosed credentials")
		}
	}
	f.cookies["cc_session"].Value = "owner-session"
	for _, section := range []string{"general", "acme", "dns", "oauth", "access", "database"} {
		status, _ := settingsWrite(f, section, body["server_version"], map[string]any{})
		if status != http.StatusForbidden {
			t.Fatal("non-admin settings mutation", section, status)
		}
	}
	status, _ := f.do(http.MethodGet, "/api/v1/certificate-settings", nil)
	if status != http.StatusForbidden {
		t.Fatal("non-admin settings read", status)
	}
	f.cookies["cc_session"].Value = "provider-admin"
	f.csrf = ""
	status, _ = settingsWrite(f, "acme", body["providers_version"], map[string]any{"accept_terms": true})
	if status != http.StatusForbidden {
		t.Fatal("CSRF bypass", status)
	}
}

func TestSettingsPersistThroughRemoteAndLocalSDK(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "remote"
		if local {
			name = "local"
		}
		t.Run(name, func(t *testing.T) {
			f, s := settingsFlows(t, local)
			body := settingsView(t, f)
			status, body := settingsWrite(f, "acme", body["providers_version"], map[string]any{"directory_url": "https://new-ca.example/directory", "email": "new@example.com", "accept_terms": true})
			if status != http.StatusOK || body["restart_required"] != false {
				t.Fatal("ACME settings save", status, body)
			}
			providers, err := s.cfg.Remote.Providers(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !providers.ACME.AcceptTerms || providers.ACME.Email != "new@example.com" || providers.DNS["dns"].SecretKey != "do-not-expose" || providers.Upstreams["ali"].SecretKey != "cloud-secret" {
				t.Fatal("ACME save changed provider accounts")
			}
			status, body = settingsWrite(f, "access", body["server_version"], map[string]any{"allow_registration": true})
			if status != http.StatusOK || body["restart_required"] != false {
				t.Fatal("registration needs no restart", status, body)
			}
			allowed, err := s.cfg.Remote.RegistrationAllowed(t.Context())
			if err != nil || !allowed {
				t.Fatal("registration did not take effect", err)
			}
			status, body = settingsWrite(f, "dns", body["server_version"], map[string]any{"zone": "auth.example.com", "listen_address": "0.0.0.0:53", "nameserver": "ns.example.com", "addresses": []string{"192.0.2.10"}})
			if status != http.StatusOK || body["restart_required"] != true {
				t.Fatal("DNS save", status, body)
			}
			if s.cfg.ACMEDNS.Zone != "" {
				t.Fatal("DNS runtime changed before restart")
			}
			status, body = settingsWrite(f, "oauth", body["server_version"], map[string]any{"client_id": "new-client", "client_secret": ""})
			if status != http.StatusOK {
				t.Fatal("OAuth save", status, body)
			}
			status, body = settingsWrite(f, "general", body["server_version"], map[string]any{"session_ttl": "12h", "languages": []string{"zh-CN"}})
			if status != http.StatusOK {
				t.Fatal("general save", status, body)
			}
			var saved config.Config
			if err := s.cfg.Remote.Read(t.Context(), "certcenter.server", &saved); err != nil {
				t.Fatal(err)
			}
			if saved.EncryptionKey != s.cfg.EncryptionKey || saved.SessionSecret != s.cfg.SessionSecret || saved.DatabaseDSN != s.cfg.DatabaseDSN || saved.OAuth.ClientSecret != s.cfg.OAuth.ClientSecret {
				t.Fatal("settings save replaced stored secrets")
			}
			if saved.OAuth.ClientID != "new-client" || saved.SessionTTL != 12*time.Hour || saved.ACMEDNS.Zone != "auth.example.com" {
				t.Fatal("settings were not persisted")
			}
			if s.cfg.SessionTTL != 24*time.Hour || s.cfg.OAuth.ClientID != "certcenter" {
				t.Fatal("settings modified running config")
			}
			if err := saved.Validate(); err != nil {
				t.Fatal(err)
			}
			saved.Remote = s.cfg.Remote
			s.cfg = saved
			if settingsView(t, f)["restart_required"] != false {
				t.Fatal("restart did not clear pending status")
			}
		})
	}
}

func TestSettingsRejectStaleOrInvalidChanges(t *testing.T) {
	f, s := settingsFlows(t, false)
	before := settingsView(t, f)
	for _, tc := range []struct {
		section string
		values  any
	}{
		{"general", map[string]any{"session_ttl": "-1h"}},
		{"general", map[string]any{"languages": []string{}}},
		{"general", map[string]any{"languages": []string{"unsupported"}}},
		{"general", map[string]any{"encryption_key": "replacement"}},
		{"general", map[string]any{"public_url": "javascript:alert(1)"}},
		{"oauth", map[string]any{"issuer_url": "http://user:secret@example.com"}},
		{"oauth", map[string]any{"client_id": ""}},
		{"database", map[string]any{"driver": "mysql", "dsn": ""}},
		{"database", map[string]any{"driver": "mysql", "dsn": "invalid-secret-dsn"}},
		{"access", map[string]any{"mapping": map[string]string{"staff": "superadmin"}}},
		{"dns", map[string]any{"zone": "auth.example.com"}},
		{"acme", map[string]any{"directory_url": "http://ca.example/directory"}},
		{"general", nil},
	} {
		version := before["server_version"]
		if tc.section == "acme" {
			version = before["providers_version"]
		}
		status, reply := settingsWrite(f, tc.section, version, tc.values)
		if status != http.StatusBadRequest {
			t.Fatal("invalid settings accepted", tc.section, status)
		}
		raw, err := json.Marshal(reply)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "invalid-secret-dsn") || strings.Contains(string(raw), "replacement") {
			t.Fatal("validation disclosed submitted secrets")
		}
	}
	status, after := settingsWrite(f, "general", before["server_version"], map[string]any{"addr": "127.0.0.1:9090"})
	if status != http.StatusOK {
		t.Fatal("valid write rejected", status, after)
	}
	status, _ = settingsWrite(f, "oauth", before["server_version"], map[string]any{"client_id": "stale"})
	if status != http.StatusConflict {
		t.Fatal("stale server write accepted", status)
	}
	providers, err := s.cfg.Remote.Providers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	providers.DNS["new"] = certconfig.DNS{Kind: "cloudflare", APIToken: "token"}
	if err := s.cfg.Remote.Write(t.Context(), "certcenter.providers", providers); err != nil {
		t.Fatal(err)
	}
	status, _ = settingsWrite(f, "acme", before["providers_version"], map[string]any{"accept_terms": true})
	if status != http.StatusConflict {
		t.Fatal("stale ACME write accepted", status)
	}
}

func TestSettingsKeepUnknownFieldsAndReplaceSecretsExplicitly(t *testing.T) {
	f, s := settingsFlows(t, false)
	var stored map[string]any
	if err := s.cfg.Remote.Read(t.Context(), "certcenter.server", &stored); err != nil {
		t.Fatal(err)
	}
	stored["future_setting"] = "preserve"
	stored["oauth"].(map[string]any)["future_option"] = "preserve"
	if err := s.cfg.Remote.Write(t.Context(), "certcenter.server", stored); err != nil {
		t.Fatal(err)
	}
	body := settingsView(t, f)
	status, body := settingsWrite(f, "oauth", body["server_version"], map[string]any{"client_secret": "rotated-secret"})
	if status != http.StatusOK {
		t.Fatal("secret replacement failed", status)
	}
	status, body = settingsWrite(f, "database", body["server_version"], map[string]any{"driver": "mysql", "dsn": "user:new-password@tcp(db:3306)/certcenter"})
	if status != http.StatusOK {
		t.Fatal("database settings save", status, body)
	}
	if err := s.cfg.Remote.Read(t.Context(), "certcenter.server", &stored); err != nil {
		t.Fatal(err)
	}
	oauth := stored["oauth"].(map[string]any)
	if stored["future_setting"] != "preserve" || oauth["future_option"] != "preserve" || oauth["client_secret"] != "rotated-secret" {
		t.Fatal("unknown settings or secret update lost")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "rotated-secret") || strings.Contains(string(raw), "new-password") {
		t.Fatal("save response disclosed secret")
	}
}

func TestSettingsReplaceMappingsAndPreserveRuntimePolicy(t *testing.T) {
	f, s := settingsFlows(t, false)
	s.cfg.Groups.Mapping = map[string]string{"previous": "admin"}
	s.cfg.AllowRegistration = true
	if err := s.cfg.Remote.Write(t.Context(), "certcenter.server", s.cfg); err != nil {
		t.Fatal(err)
	}
	body := settingsView(t, f)
	status, _ := settingsWrite(f, "access", body["server_version"], map[string]any{"allow_registration": false, "mapping": map[string]string{}})
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var stored config.Config
	if err := s.cfg.Remote.Read(t.Context(), "certcenter.server", &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Groups.Mapping) != 0 || stored.AllowRegistration {
		t.Fatal("explicit empty mapping or false setting was ignored")
	}
	if s.cfg.Groups.Mapping["previous"] != "admin" {
		t.Fatal("edit changed active policy before restart")
	}
}

func TestSettingsWriteFailureKeepsStoredConfiguration(t *testing.T) {
	f, s := settingsFlows(t, false)
	var saved, providers settingsDocument
	if err := s.cfg.Remote.Read(t.Context(), "certcenter.server", &saved); err != nil {
		t.Fatal(err)
	}
	if err := s.cfg.Remote.Read(t.Context(), "certcenter.providers", &providers); err != nil {
		t.Fatal(err)
	}
	records := []configcenter.Item{}
	for key, value := range map[string]settingsDocument{"certcenter.server": saved, "certcenter.providers": providers} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, configcenter.Item{Key: key, Value: string(raw)})
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusServiceUnavailable)
		case r.URL.Path == "/api/v1/agent/project":
			json.NewEncoder(w).Encode(map[string]any{"project": map[string]string{"slug": "certcenter"}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"items": records})
		}
	}))
	defer remote.Close()
	client, err := configcenter.New(configcenter.Config{BaseURL: remote.URL, APIKey: "cc_test"})
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Remote = certconfig.NewRemote(client)
	before := settingsView(t, f)
	status, body := settingsWrite(f, "oauth", before["server_version"], map[string]any{"client_secret": "new-private-secret"})
	if status != http.StatusServiceUnavailable {
		t.Fatal("failed storage reported success", status, body)
	}
	after := settingsView(t, f)
	if after["server_version"] != before["server_version"] || after["restart_required"] != false {
		t.Fatal("failed write changed stored state")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "new-private-secret") {
		t.Fatal("failed write disclosed submitted secret")
	}
}
