package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/OXeu/certcenter/internal/store"
)

type testOAuthGrant struct{ Client, Redirect, Resource, Verifier, Request, Code, Access, Refresh string }

func oauthPost(t *testing.T, router http.Handler, path string, form url.Values, want int) map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
	}
	var result map[string]any
	if response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func oauthConsentRequest(t *testing.T, f *flows) testOAuthGrant {
	t.Helper()
	_, metadata := f.do(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil)
	resource, _ := metadata["resource"].(string)
	code, client := f.do(http.MethodPost, "/oauth/register", map[string]any{"client_name": "Test MCP client", "redirect_uris": []string{"http://127.0.0.1:9876/callback"}, "token_endpoint_auth_method": "none"})
	if code != 201 {
		t.Fatal("registration", code, client)
	}
	grant := testOAuthGrant{Client: client["client_id"].(string), Redirect: "http://127.0.0.1:9876/callback", Resource: resource, Verifier: strings.Repeat("v", 43)}
	request := httptest.NewRequest(http.MethodGet, grant.authorizeURL(), http.NoBody)
	request.AddCookie(f.cookies["cc_session"])
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal("authorize", response.Code, response.Body.String())
	}
	_, tail, found := strings.Cut(response.Body.String(), `<script id="page-data" type="application/json">`)
	if !found {
		t.Fatal("missing consent page")
	}
	raw, _, _ := strings.Cut(tail, "</script>")
	var page struct {
		Page    string
		Consent struct{ Request, Client, RedirectURI, Resource string }
	}
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatal(err)
	}
	if page.Page != "mcp-consent" || page.Consent.Client != "Test MCP client" || page.Consent.Resource != resource {
		t.Fatal("invalid consent", page)
	}
	grant.Request = page.Consent.Request
	return grant
}

func (g testOAuthGrant) authorizeURL() string {
	hash := sha256.Sum256([]byte(g.Verifier))
	return "/oauth/authorize?" + url.Values{"client_id": {g.Client}, "redirect_uri": {g.Redirect}, "resource": {g.Resource}, "scope": {"mcp"}, "state": {"client-state"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}}.Encode()
}

func (g testOAuthGrant) exchangeForm() url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "client_id": {g.Client}, "redirect_uri": {g.Redirect}, "resource": {g.Resource}, "code": {g.Code}, "code_verifier": {g.Verifier}}
}

func approveMCP(t *testing.T, f *flows, g testOAuthGrant) testOAuthGrant {
	t.Helper()
	status, result := f.do(http.MethodPost, "/oauth/authorize", map[string]any{"request": g.Request, "approve": true})
	if status != 200 {
		t.Fatal("consent", status, result)
	}
	target, err := url.Parse(result["redirect"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if target.Query().Get("state") != "client-state" {
		t.Fatal("state lost")
	}
	g.Code = target.Query().Get("code")
	return g
}

func authorizeMCP(t *testing.T, f *flows) testOAuthGrant {
	t.Helper()
	g := approveMCP(t, f, oauthConsentRequest(t, f))
	tokens := oauthPost(t, f.router, "/oauth/token", g.exchangeForm(), 200)
	g.Access, g.Refresh = tokens["access_token"].(string), tokens["refresh_token"].(string)
	return g
}

func mcpRequest(t *testing.T, router http.Handler, token string, body any, want int) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(raw)))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("MCP status %d, want %d: %s", response.Code, want, response.Body.String())
	}
	return response
}

func TestMCPOAuthConsentBindingsAndReplay(t *testing.T) {
	f, _ := certificateFlows(t)
	grant := oauthConsentRequest(t, f)
	csrf := f.csrf
	f.csrf = ""
	if status, _ := f.do(http.MethodPost, "/oauth/authorize", map[string]any{"request": grant.Request, "approve": true}); status != 403 {
		t.Fatal("consent skipped csrf")
	}
	f.csrf = csrf
	grant = approveMCP(t, f, grant)
	for _, field := range []string{"client_id", "redirect_uri", "resource", "code_verifier"} {
		form := grant.exchangeForm()
		form.Set(field, "invalid")
		oauthPost(t, f.router, "/oauth/token", form, 400)
	}
	tokens := oauthPost(t, f.router, "/oauth/token", grant.exchangeForm(), 200)
	token := tokens["access_token"].(string)
	mcpCall(t, f.router, token, "list_domains", nil, 200)
	oauthPost(t, f.router, "/oauth/token", grant.exchangeForm(), 400)
	mcpRequest(t, f.router, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}, 401)
	denied := oauthConsentRequest(t, f)
	status, result := f.do(http.MethodPost, "/oauth/authorize", map[string]any{"request": denied.Request, "approve": false})
	if status != 200 || !strings.Contains(result["redirect"].(string), "error=access_denied") {
		t.Fatal("denial", status, result)
	}
	if status, _ := f.do(http.MethodPost, "/oauth/authorize", map[string]any{"request": denied.Request, "approve": true}); status != 400 {
		t.Fatal("consent replay")
	}
}

func TestMCPOAuthRefreshRevocationAndCurrentPermissions(t *testing.T) {
	f, s := certificateFlows(t)
	grant := authorizeMCP(t, f)
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {grant.Client}, "resource": {grant.Resource}, "refresh_token": {grant.Refresh}}
	wrong := url.Values{}
	for k, v := range form {
		wrong[k] = append([]string{}, v...)
	}
	wrong.Set("resource", "https://wrong.example/mcp")
	oauthPost(t, f.router, "/oauth/token", wrong, 400)
	tokens := oauthPost(t, f.router, "/oauth/token", form, 200)
	current := tokens["access_token"].(string)
	mcpCall(t, f.router, current, "list_domains", nil, 200)
	mcpRequest(t, f.router, grant.Access, nil, 401)
	oauthPost(t, f.router, "/oauth/token", form, 400)
	mcpRequest(t, f.router, current, nil, 401)
	grant = authorizeMCP(t, f)
	oauthPost(t, f.router, "/oauth/revoke", url.Values{"client_id": {grant.Client}, "token": {grant.Refresh}}, 200)
	mcpRequest(t, f.router, grant.Access, nil, 401)
	providerAdmin(t, f, s)
	grant = authorizeMCP(t, f)
	mcpCall(t, f.router, grant.Access, "list_users", nil, 200)
	admin, err := s.store.UserByEmail(t.Context(), "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetUserRole(t.Context(), admin.ID, store.RoleUser); err != nil {
		t.Fatal(err)
	}
	mcpCall(t, f.router, grant.Access, "list_users", nil, 403)
	owner, err := s.store.UserByEmail(t.Context(), "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.DeleteUser(t.Context(), admin.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	mcpRequest(t, f.router, grant.Access, nil, 401)
}

func TestMCPOAuthRejectsUntrustedRedirectsAndSessions(t *testing.T) {
	f, s := certificateFlows(t)
	for _, uri := range []string{"https://client.example/cb#fragment", "http://client.example/cb", "javascript:alert(1)", "https://user:secret@client.example/cb", "//client.example/cb"} {
		if status, _ := f.do(http.MethodPost, "/oauth/register", map[string]any{"redirect_uris": []string{uri}}); status != 400 {
			t.Fatal("unsafe redirect", uri)
		}
	}
	grant := oauthConsentRequest(t, f)
	for _, change := range []struct{ key, value string }{{"redirect_uri", "https://evil.example/cb"}, {"code_challenge_method", "plain"}, {"resource", "https://evil.example/mcp"}, {"scope", "admin"}} {
		u, _ := url.Parse(grant.authorizeURL())
		q := u.Query()
		q.Set(change.key, change.value)
		u.RawQuery = q.Encode()
		if status, _ := f.do(http.MethodGet, u.String(), nil); status != 400 {
			t.Fatal("accepted invalid authorization", change.key, status)
		}
	}
	user, err := s.store.UserByEmail(t.Context(), "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.CreateSession(t.Context(), store.Session{ID: "second-session", UserID: user.ID, CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f.cookies["cc_session"].Value = "second-session"
	if status, _ := f.do(http.MethodPost, "/oauth/authorize", map[string]any{"request": grant.Request, "approve": true}); status != 400 {
		t.Fatal("consent crossed sessions")
	}
	response := mcpRequest(t, f.router, "", nil, 401)
	if !strings.Contains(response.Header().Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatal("missing discovery challenge")
	}
	if status, _ := f.do(http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}); status != 401 {
		t.Fatal("session accepted at MCP")
	}
	key, _ := issueKey(t, f, "/api/v1/keys", scopeBody("key", keyScopeJSON{Domain: "example.com", Permissions: []store.Permission{store.DomainsRead}}))
	keyRequest(t, f.router, key, http.MethodPost, "/mcp", nil, 401)
	mcpRequest(t, f.router, key, nil, 401)
	expired := "expired-access"
	if err := s.store.CreateOAuthToken(t.Context(), store.OAuthToken{AccessHash: credentialHash(expired), RefreshHash: credentialHash("expired-refresh"), UserID: user.ID, Resource: s.mcpResource(), AccessExpiresAt: time.Now().Add(-time.Second), RefreshExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	mcpRequest(t, f.router, expired, nil, 401)
}

func TestMCPAuthorizationResumesAfterIdentityProviderLogin(t *testing.T) {
	s, router, idp := testServer(t, true)
	client := store.OAuthClient{ID: "login-client", Name: "Login client", RedirectURIs: []string{"http://127.0.0.1:9876/callback"}}
	if err := s.store.CreateOAuthClient(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	grant := testOAuthGrant{Client: client.ID, Redirect: client.RedirectURIs[0], Resource: s.mcpResource(), Verifier: strings.Repeat("v", 43)}
	cookies := &jar{cookies: map[string]*http.Cookie{}}
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		request.Header.Set("Cookie", cookies.header())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		cookies.add(response.Result())
		return response
	}
	response := get(grant.authorizeURL())
	if response.Code != 302 || response.Header().Get("Location") != "/auth/login" {
		t.Fatal("login not started")
	}
	get("/auth/login")
	f := &flows{t: t, router: router, cookies: cookies.cookies}
	state, nonce := startState(t, f)
	idp.lastNonce = nonce
	response = get("/oauth/callback?code=good&state=" + url.QueryEscape(state))
	if response.Code != 302 || response.Header().Get("Location") != grant.authorizeURL() {
		t.Fatal("MCP authorization was not resumed", response.Header().Get("Location"))
	}
	response = get(response.Header().Get("Location"))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"page":"mcp-consent"`) {
		t.Fatal("consent not shown after login")
	}
	if strings.Contains(response.Body.String(), "access_token") {
		t.Fatal("token issued before consent")
	}
}

func TestMCPProtocolParsingAndNegotiation(t *testing.T) {
	f, _ := certificateFlows(t)
	token := authorizeMCP(t, f).Access
	for _, version := range []string{mcpProtocol, "2025-06-18", "2025-03-26", "future-version"} {
		reply := mcpRequest(t, f.router, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": version}}, 200)
		expected := version
		if version == "future-version" {
			expected = mcpProtocol
		}
		if !strings.Contains(reply.Body.String(), `"protocolVersion":"`+expected+`"`) {
			t.Fatal("version not negotiated")
		}
	}
	for _, payload := range []string{"{", "{} {}", "[]"} {
		request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(payload))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		f.router.ServeHTTP(response, request)
		if response.Code != 400 {
			t.Fatal("malformed request accepted")
		}
	}
	reply := mcpRequest(t, f.router, token, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, 202)
	if reply.Body.Len() != 0 {
		t.Fatal("notification response has a body")
	}
	mcpRequest(t, f.router, token, map[string]any{"jsonrpc": "2.0", "id": true, "method": "ping"}, 200)
}
