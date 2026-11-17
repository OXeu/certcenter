package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestProviderCompletesPlainOAuthLogin(t *testing.T) {
	t.Parallel()
	var issuer, challenge string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			body = map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
				"token_endpoint": issuer + "/token", "userinfo_endpoint": issuer + "/userinfo",
				"scopes_supported": []string{"profile", "email", "offline_access", "mcp"},
			}
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			id, secret, ok := r.BasicAuth()
			if !ok || id != "client" || secret != "secret" || r.Form.Get("code") != "good" ||
				r.Form.Get("redirect_uri") != "https://cert.example/oauth/callback" {
				t.Error("invalid token request")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(digest[:]) != challenge {
				t.Error("invalid PKCE verifier")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body = map[string]any{"access_token": "access", "token_type": "Bearer"}
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer access" {
				t.Error("missing access token")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			body = map[string]any{
				"sub": "user-1", "name": "Dana", "email": "dana@example.com",
				"picture": "https://images.example/dana.png",
			}
		default:
			t.Errorf("unexpected provider request: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	issuer = server.URL
	provider, err := NewProvider(t.Context(), Config{
		IssuerURL: issuer, ClientID: "client", ClientSecret: "secret",
		RedirectURL: "https://cert.example/oauth/callback", Scopes: []string{"openid", "profile", "email"},
	})
	if err != nil {
		t.Fatal(err)
	}
	redirect, blob, err := provider.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	location, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	if query.Get("scope") != "profile email" || query.Has("nonce") || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("invalid plain OAuth authorization parameters: %v", query)
	}
	challenge = query.Get("code_challenge")
	claims, err := provider.Finish(t.Context(), "good", query.Get("state"), blob)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != issuer || claims.Sub != "user-1" || claims.Email != "dana@example.com" ||
		claims.Name != "Dana" || claims.Picture != "https://images.example/dana.png" {
		t.Fatalf("unexpected userinfo claims: %+v", claims)
	}
}
