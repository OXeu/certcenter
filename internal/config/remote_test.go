package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/OXeu/configcenter/sdk/configcenter"
)

func TestRemoteSettingsOverrideAllLocalOperationalVariables(t *testing.T) {
	settings := map[string]any{"addr": "127.0.0.1:9090", "database_driver": "sqlite", "database_dsn": "file:remote?mode=memory&cache=shared", "public_url": "https://cert.example.com", "encryption_key": base64.StdEncoding.EncodeToString(make([]byte, 32)), "allow_registration": true, "oauth": map[string]string{"issuer_url": "https://id.example.com", "client_id": "remote-client", "client_secret": "remote-secret"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cc_bootstrap" {
			t.Error("bootstrap key missing")
		}
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []configcenter.Item{{Key: "certcenter.server", Value: string(raw)}, {Key: "certcenter.providers", Value: "{}"}}})
	}))
	defer server.Close()
	t.Setenv("CC_CONFIGCENTER_URL", server.URL)
	t.Setenv("CC_CONFIGCENTER_API_KEY", "cc_bootstrap")
	t.Setenv("CC_ENV_FILE", "../../.tmp/missing-bootstrap-env")
	t.Setenv("CC_ADDR", "bad-local-address")
	t.Setenv("CC_DB", "file:must-not-be-used")
	t.Setenv("CC_OAUTH_CLIENT_SECRET", "local-secret")
	cfg, err := LoadRemote(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:9090" || cfg.OAuth.ClientSecret != "remote-secret" || cfg.OAuth.RedirectURL != "https://cert.example.com/oauth/callback" {
		t.Fatal("local operational settings bypassed ConfigCenter")
	}
	settings["encryption_key"] = "invalid-key"
	if _, err := LoadRemote(t.Context()); err == nil || !strings.Contains(err.Error(), "certcenter.server") {
		t.Fatal("invalid encryption key accepted or key context omitted", err)
	}
}

func TestRemoteStartupInitializesAllMissingDefaults(t *testing.T) {
	values := map[string]string{}
	var mu sync.Mutex
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer cc_bootstrap" {
			t.Error("bootstrap bearer token missing")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/agent/project":
			json.NewEncoder(w).Encode(map[string]any{"project": map[string]string{"slug": "certcenter"}})
		case "/api/v1/agent/environments/production/values":
			items := []configcenter.Item{}
			for key, value := range values {
				items = append(items, configcenter.Item{Key: key, Value: value})
			}
			json.NewEncoder(w).Encode(map[string]any{"items": items})
		case "/api/v1/projects/certcenter/environments/production/items":
			if r.Method != http.MethodPut {
				t.Error("unexpected method")
			}
			var in struct {
				Items []configcenter.Item `json:"items"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			for _, item := range in.Items {
				values[item.Key] = item.Value
			}
			writes++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected settings path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("CC_ENV_FILE", "../../.tmp/missing-bootstrap-env")
	t.Setenv("CC_CONFIGCENTER_URL", server.URL)
	t.Setenv("CC_CONFIGCENTER_API_KEY", "cc_bootstrap")
	t.Setenv("CC_CONFIGCENTER_ENVIRONMENT", "production")
	first, err := LoadRemote(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.Remote.IsLocal() || first.EncryptionKey == "" {
		t.Fatal("initialization did not use remote storage")
	}
	providers, err := first.Remote.Providers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if providers.ACME.AcceptTerms || len(providers.DNS) != 0 {
		t.Fatal("unsafe provider defaults")
	}
	mu.Lock()
	if len(values) != 2 || writes != 1 {
		t.Error("defaults not initialized in one operation")
	}
	var edited map[string]any
	json.Unmarshal([]byte(values["certcenter.server"]), &edited)
	edited["addr"] = "127.0.0.1:9098"
	raw, err := json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	values["certcenter.server"] = string(raw)
	delete(values, "certcenter.providers")
	mu.Unlock()
	restarted, err := LoadRemote(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Addr != "127.0.0.1:9098" || restarted.EncryptionKey != first.EncryptionKey {
		t.Fatal("defaults overwrote existing configuration")
	}
	if _, err := LoadRemote(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(values) != 2 || writes != 2 {
		t.Fatal("existing defaults were rewritten")
	}
}

func localSettingsPath(t *testing.T) string {
	t.Helper()
	directory, err := filepath.Abs(filepath.Join("..", "..", ".tmp", "local-config-"+rand.Text()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("CC_ENV_FILE", "../../.tmp/missing-bootstrap-env")
	t.Setenv("CC_CONFIGCENTER_URL", "")
	t.Setenv("CC_CONFIGCENTER_API_KEY", "")
	t.Setenv("CC_CONFIGCENTER_ENVIRONMENT", "production")
	path := filepath.Join(directory, "config.json")
	t.Setenv("CC_CONFIGCENTER_LOCAL_PATH", path)
	return path
}

func TestLocalStartupPersistsDefaultsAndEncryptionKey(t *testing.T) {
	path := localSettingsPath(t)
	first, err := LoadRemote(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Remote.IsLocal() || first.EncryptionKey == "" || first.OAuth.Enabled() {
		t.Fatal("invalid local startup defaults")
	}
	providers, err := first.Remote.Providers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if providers.ACME.AcceptTerms || len(providers.DNS) != 0 {
		t.Fatal("automatic external operations enabled by default")
	}
	first.Addr = "127.0.0.1:9095"
	if err := first.Remote.Write(t.Context(), "certcenter.server", first); err != nil {
		t.Fatal(err)
	}
	restarted, err := LoadRemote(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if restarted.EncryptionKey != first.EncryptionKey || restarted.Addr != first.Addr {
		t.Fatal("restart replaced local settings or encryption key")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("local settings permissions", err)
	}
}

func TestPartialConnectionDoesNotInitializeLocalSettings(t *testing.T) {
	path := localSettingsPath(t)
	t.Setenv("CC_CONFIGCENTER_URL", "https://config.example.com")
	if _, err := LoadRemote(t.Context()); err == nil {
		t.Fatal("partial configuration accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("partial configuration created a KV database")
	}
}
