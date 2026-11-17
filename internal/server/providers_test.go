package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/store"
)

func providerAdmin(t *testing.T, f *flows, s *Server) {
	t.Helper()
	admin, err := s.store.UserByEmail(t.Context(), "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.CreateSession(t.Context(), store.Session{ID: "provider-admin", UserID: admin.ID, CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f.cookies["cc_session"].Value = "provider-admin"
}

func providerRevision(t *testing.T, f *flows, path string) any {
	t.Helper()
	status, body := f.do(http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("read providers: %d %+v", status, body)
	}
	return body["version"]
}

func TestProviderAdministrationPermissionsAndRedaction(t *testing.T) {
	f, s := certificateFlows(t)
	for _, path := range []string{"/dns-providers", "/upstreams"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			target := "/api/v1" + path
			if method == http.MethodPut || method == http.MethodDelete {
				target += "/dns"
			}
			status, _ := f.do(method, target, map[string]any{})
			if status != http.StatusForbidden {
				t.Fatalf("non-admin %s %s: %d", method, target, status)
			}
		}
		status, _ := f.do(http.MethodGet, path, nil)
		if status != http.StatusForbidden {
			t.Fatalf("non-admin page %s: %d", path, status)
		}
	}
	providerAdmin(t, f, s)
	for _, path := range []string{"/dns-providers", "/upstreams"} {
		status, _ := f.do(http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("admin page %s: %d", path, status)
		}
		status, body := f.do(http.MethodGet, "/api/v1"+path, nil)
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusOK {
			t.Fatal(status)
		}
		for _, secret := range []string{"secret-id", "do-not-expose", "cloud-id", "cloud-secret", "access_key", "secret_key", "private_key", "accounts"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("provider view exposes %s", secret)
			}
		}
		f.csrf = ""
		status, _ = f.do(http.MethodPost, "/api/v1"+path, map[string]any{})
		if status != http.StatusForbidden {
			t.Fatal("provider write bypassed CSRF", status)
		}
		f.csrf = "csrf"
	}
	status, _ := f.do(http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	if status != http.StatusNotFound {
		t.Fatal("CertCenter unexpectedly exposes MCP", status)
	}
}

func TestProviderWritesPreserveSecretsReferencesAndRevisions(t *testing.T) {
	f, s := certificateFlows(t)
	providerAdmin(t, f, s)
	for _, tc := range []struct{ path, name, field, kind string }{
		{"/api/v1/dns-providers", "dns", "dns", "alidns"},
		{"/api/v1/upstreams", "ali", "upstream", "aliyun"},
	} {
		version := providerRevision(t, f, tc.path)
		status, _ := f.do(http.MethodDelete, tc.path+"/"+tc.name, map[string]any{"version": version})
		if status != http.StatusConflict {
			t.Fatal("deleted a referenced provider", status)
		}
		update := map[string]any{"version": version, tc.field: map[string]string{"kind": tc.kind, "region": "updated"}}
		status, _ = f.do(http.MethodPut, tc.path+"/"+tc.name, update)
		if status != http.StatusNoContent {
			t.Fatal("provider update failed", status)
		}
		status, _ = f.do(http.MethodPut, tc.path+"/"+tc.name, update)
		if status != http.StatusConflict {
			t.Fatal("stale provider overwrite", status)
		}
		status, _ = f.do(http.MethodPost, tc.path, map[string]any{"version": providerRevision(t, f, tc.path), "name": tc.name})
		if status != http.StatusConflict {
			t.Fatal("duplicate provider accepted", status)
		}
	}
	p, err := s.cfg.Remote.Providers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p.DNS["dns"].AccessKey != "secret-id" || p.DNS["dns"].SecretKey != "do-not-expose" || p.Upstreams["ali"].AccessKey != "cloud-id" || p.Upstreams["ali"].SecretKey != "cloud-secret" {
		t.Fatal("blank edit replaced stored credentials")
	}
	if p.ACME.DirectoryURL != "https://ca.example/directory" {
		t.Fatal("provider write changed ACME")
	}
}

func TestProviderCreationSelectionAndDeletion(t *testing.T) {
	f, s := certificateFlows(t)
	providerAdmin(t, f, s)
	for _, tc := range []struct {
		path, name, field string
		value             any
	}{
		{"/api/v1/dns-providers", "new-dns", "dns", certconfig.DNS{Kind: "tencentcloud", AccessKey: "new-id", SecretKey: "new-secret"}},
		{"/api/v1/upstreams", "new-cloud", "upstream", certconfig.Upstream{Kind: "tencentcloud", AccessKey: "new-id", SecretKey: "new-secret"}},
		{"/api/v1/upstreams", "new-ssh", "upstream", certconfig.Upstream{Kind: "ssh", Address: "host:22", User: "deploy", PrivateKey: "ssh-secret", HostKey: "ssh-host-public", CertificatePath: "/cert.pem", KeyPath: "/key.pem"}},
		{"/api/v1/dns-providers", "delegated", "dns", certconfig.DNS{Kind: "acme-dns", APIBase: "https://auth.example.com", Accounts: map[string]certconfig.DNSAccount{"example.com": {Username: "acme-user", Password: "acme-secret", Subdomain: "sub", FullDomain: "sub.auth.example.com"}}}},
	} {
		status, body := f.do(http.MethodPost, tc.path, map[string]any{"version": providerRevision(t, f, tc.path), "name": tc.name, tc.field: tc.value})
		if status != http.StatusNoContent {
			t.Fatalf("create %s: %d %+v", tc.name, status, body)
		}
		_, detail := f.do(http.MethodGet, "/api/v1/domains/example.com", nil)
		key := "upstreams"
		if tc.field == "dns" {
			key = "dns_providers"
		}
		found := false
		for _, name := range detail[key].([]any) {
			found = found || name == tc.name
		}
		if !found {
			t.Fatal("new provider missing from policy choices", tc.name)
		}
		_, listing := f.do(http.MethodGet, tc.path, nil)
		raw, err := json.Marshal(listing)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"ssh-secret", "acme-secret", "acme-user", "new-id", "new-secret"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("credential leaked", secret)
			}
		}
		var fields map[string]any
		for _, item := range listing["providers"].([]any) {
			view := item.(map[string]any)
			if view["name"] == tc.name {
				fields = view["fields"].(map[string]any)
			}
		}
		status, _ = f.do(http.MethodPut, tc.path+"/"+tc.name, map[string]any{"version": listing["version"], tc.field: fields})
		if status != http.StatusNoContent {
			t.Fatal("edit without credentials failed", status)
		}
		stored, err := s.cfg.Remote.Providers(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		switch tc.name {
		case "new-ssh":
			if stored.Upstreams[tc.name].PrivateKey != "ssh-secret" {
				t.Fatal("SSH key was discarded")
			}
		case "delegated":
			if stored.DNS[tc.name].Accounts["example.com"].Password != "acme-secret" {
				t.Fatal("acme-dns registration was discarded")
			}
		}
		status, _ = f.do(http.MethodDelete, tc.path+"/"+tc.name, map[string]any{"version": providerRevision(t, f, tc.path)})
		if status != http.StatusNoContent {
			t.Fatal("unused provider deletion failed", status)
		}
	}
	status, _ := f.do(http.MethodPost, "/api/v1/dns-providers", map[string]any{"version": providerRevision(t, f, "/api/v1/dns-providers"), "name": "invalid", "dns": certconfig.DNS{Kind: "alidns"}})
	if status != http.StatusBadRequest {
		t.Fatal("missing credentials accepted", status)
	}
}
