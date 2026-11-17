package config

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OXeu/configcenter/sdk/configcenter"
)

func TestRemoteSettingsOverrideAllLocalOperationalVariables(t *testing.T) {
	settings := map[string]any{"addr": "127.0.0.1:9090", "database_driver": "sqlite", "database_dsn": "file:remote?mode=memory&cache=shared", "public_url": "https://cert.example.com", "encryption_key": base64.StdEncoding.EncodeToString(make([]byte, 32)), "allow_registration": true, "oauth": map[string]string{"issuer_url": "https://id.example.com", "client_id": "remote-client", "client_secret": "remote-secret"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "ApiKey cc_bootstrap" {
			t.Error("bootstrap key missing")
		}
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []configcenter.Item{{Key: "certcenter.server", Value: string(raw)}}})
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
	if _, err := LoadRemote(t.Context()); err == nil {
		t.Fatal("invalid encryption key accepted")
	}
}
