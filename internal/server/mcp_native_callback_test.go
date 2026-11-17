package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestMCPOAuthClientRedirects(t *testing.T) {
	f, s := certificateFlows(t)
	redirects := []string{
		"cursor://anysphere.cursor-mcp/oauth/callback",
		"https://www.cursor.com/agents/mcp/oauth/callback",
		"http://localhost:8787/callback",
		"http://client.example/callback",
		"my-app://login/complete",
		"cursor://another-app/other-command",
		"my-app://login/complete?mode=oauth#done",
		"/oauth/client-callback",
		"my-app:oauth/callback",
	}
	status, registered := f.do(http.MethodPost, "/oauth/register", map[string]any{
		"client_name": "MCP client", "redirect_uris": redirects,
		"grant_types":    []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"}, "token_endpoint_auth_method": "none",
	})
	if status != http.StatusCreated {
		t.Fatalf("client registration: %d %v", status, registered)
	}
	clientID, ok := registered["client_id"].(string)
	if !ok {
		t.Fatal("registration did not return a client ID")
	}
	client, err := s.store.OAuthClientByID(t.Context(), clientID)
	if err != nil || !slices.Equal(client.RedirectURIs, redirects) {
		t.Fatal("registration did not preserve every callback")
	}
	for _, callback := range redirects {
		t.Run(callback, func(t *testing.T) {
			grant := testOAuthGrant{Client: clientID, Redirect: callback, Resource: s.mcpResource(), Verifier: strings.Repeat("v", 43)}
			request := httptest.NewRequest(http.MethodGet, grant.authorizeURL(), http.NoBody)
			request.AddCookie(f.cookies["cc_session"])
			response := httptest.NewRecorder()
			f.router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("registered callback did not reach consent: %d", response.Code)
			}
			_, tail, found := strings.Cut(response.Body.String(), `<script id="page-data" type="application/json">`)
			if !found {
				t.Fatal("missing consent page")
			}
			raw, _, _ := strings.Cut(tail, "</script>")
			var page struct {
				Consent struct{ Request, RedirectURI string }
			}
			if err := json.Unmarshal([]byte(raw), &page); err != nil {
				t.Fatal(err)
			}
			if page.Consent.RedirectURI != callback {
				t.Fatal("consent displayed a different callback")
			}
			grant.Request = page.Consent.Request
			status, result := f.do(http.MethodPost, "/oauth/authorize", map[string]any{"request": grant.Request, "approve": true})
			if status != http.StatusOK {
				t.Fatal("consent failed")
			}
			target, err := url.Parse(result["redirect"].(string))
			if err != nil {
				t.Fatal(err)
			}
			query := target.Query()
			grant.Code = query.Get("code")
			if query.Get("state") != "client-state" {
				t.Fatal("consent changed the state")
			}
			query.Del("code")
			query.Del("state")
			target.RawQuery = query.Encode()
			if target.String() != callback {
				t.Fatal("consent changed the registered callback or state")
			}
			form := grant.exchangeForm()
			form.Set("redirect_uri", "cursor://another-app/oauth/callback")
			oauthPost(t, f.router, "/oauth/token", form, http.StatusBadRequest)
			form.Set("redirect_uri", callback)
			form.Set("code_verifier", strings.Repeat("x", 43))
			oauthPost(t, f.router, "/oauth/token", form, http.StatusBadRequest)
			form.Set("code_verifier", grant.Verifier)
			tokens := oauthPost(t, f.router, "/oauth/token", form, http.StatusOK)
			mcpCall(t, f.router, tokens["access_token"].(string), "get_identity", nil, http.StatusOK)
		})
	}
}

func TestMCPOAuthRegistersCallbacksWithoutURIPolicy(t *testing.T) {
	f, s := certificateFlows(t)
	for _, callback := range []string{
		"cursor://another-app/oauth/callback",
		"cursor://anysphere.cursor-mcp/other-command",
		"cursor://anysphere.cursor-mcp:1234/oauth/callback",
		"cursor://user@anysphere.cursor-mcp/oauth/callback",
		"cursor://anysphere.cursor-mcp/oauth/callback#fragment",
		"cursor://anysphere.cursor-mcp/oauth/callback?command=other",
		"cursor://anysphere.cursor-mcp/%6fauth/callback",
		"javascript://anysphere.cursor-mcp/oauth/callback",
		"file://anysphere.cursor-mcp/oauth/callback",
		"data://anysphere.cursor-mcp/oauth/callback",
		"http://client.example/callback",
		"relative/callback",
		"not a URI",
		strings.Repeat("x", 4096),
	} {
		t.Run(callback, func(t *testing.T) {
			status, result := f.do(http.MethodPost, "/oauth/register", map[string]any{"redirect_uris": []string{callback}})
			if status != http.StatusCreated {
				t.Fatal("callback URI policy rejected registration")
			}
			client, err := s.store.OAuthClientByID(t.Context(), result["client_id"].(string))
			if err != nil || !slices.Equal(client.RedirectURIs, []string{callback}) {
				t.Fatal("registration did not preserve the supplied callback")
			}
		})
	}
}
