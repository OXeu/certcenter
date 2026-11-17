package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/OXeu/certcenter/internal/store"
)

func mcpCall(t *testing.T, router http.Handler, token, name string, args map[string]any, want int) map[string]any {
	t.Helper()
	rec := mcpRequest(t, router, token, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}, http.StatusOK)
	var reply struct {
		Error  *rpcError `json:"error"`
		Result struct {
			IsError    bool           `json:"isError"`
			Structured map[string]any `json:"structuredContent"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error != nil {
		t.Fatalf("%s: RPC error %d: %s", name, reply.Error.Code, reply.Error.Message)
	}
	if reply.Result.Structured["http_status"] != float64(want) || reply.Result.IsError != (want >= 400) {
		t.Fatalf("%s: HTTP status %v, want %d", name, reply.Result.Structured["http_status"], want)
	}
	if len(reply.Result.Content) != 1 || reply.Result.Content[0].Type != "text" || !json.Valid([]byte(reply.Result.Content[0].Text)) {
		t.Fatalf("%s: missing JSON text result", name)
	}
	return reply.Result.Structured
}

func TestMCPCompleteAPI(t *testing.T) {
	s, router, _ := testServer(t, false)
	admin, err := s.store.UserByEmail(t.Context(), "admin@localhost")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.CreateSession(t.Context(), store.Session{ID: "admin-mcp", UserID: admin.ID, CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f := &flows{t: t, router: router, csrf: "csrf", cookies: map[string]*http.Cookie{"cc_session": {Name: "cc_session", Value: "admin-mcp"}}}
	token := authorizeMCP(t, f).Access
	seen := map[string]bool{}
	call := func(name string, args map[string]any, want int) map[string]any {
		t.Helper()
		seen[name] = true
		return mcpCall(t, router, token, name, args, want)
	}
	call("get_identity", nil, http.StatusOK)
	call("get_key_scopes", nil, http.StatusOK)
	call("list_projects", nil, http.StatusOK)
	call("create_project", map[string]any{"slug": "billing", "name": "Billing"}, http.StatusCreated)
	call("update_project", map[string]any{"project": "billing", "name": "Billing updated", "description": "managed"}, http.StatusOK)
	call("create_environment", map[string]any{"project": "billing", "slug": "staging", "name": "Staging"}, http.StatusCreated)
	call("list_environments", map[string]any{"project": "billing"}, http.StatusOK)
	call("get_project", map[string]any{"project": "billing"}, http.StatusOK)
	config := map[string]any{"project": "billing", "environment": "staging", "key": "db/pool", "value": ""}
	call("set_config", config, http.StatusOK)
	delete(config, "value")
	if got := call("get_config", config, http.StatusOK); got["value"] != "" || got["key"] != "db/pool" {
		t.Fatal("encoded key or empty value changed")
	}
	call("list_config", map[string]any{"project": "billing", "environment": "staging", "prefix": "db/"}, http.StatusOK)
	call("list_values", map[string]any{"project": "billing", "environment": "staging"}, http.StatusOK)
	config["value"], config["new_key"] = "20", "db.pool"
	config["new_key"] = "invalid key"
	call("set_config", config, http.StatusBadRequest)
	config["new_key"] = "db.pool"
	call("set_config", config, http.StatusOK)
	delete(config, "value")
	delete(config, "new_key")
	call("get_config", config, http.StatusNotFound)
	config["key"] = "db.pool"
	proposal := call("propose_config", map[string]any{"project": "billing", "environment": "staging", "key": "db.pool", "action": "set", "value": "5", "reason": "tune"}, http.StatusCreated)
	if proposal["status"] != store.StatusPending {
		t.Fatal("proposal status was lost")
	}
	call("list_proposals", map[string]any{"project": "billing", "status": "pending"}, http.StatusOK)
	call("review_proposal", map[string]any{"project": "billing", "id": proposal["id"], "approve": true}, http.StatusOK)
	if got := call("get_config", config, http.StatusOK); got["value"] != "5" {
		t.Fatal("proposal was not applied")
	}
	proposal = call("propose_config", map[string]any{"project": "billing", "environment": "staging", "key": "db.pool", "action": "delete"}, http.StatusCreated)
	call("review_proposal", map[string]any{"project": "billing", "id": proposal["id"], "approve": false}, http.StatusOK)
	call("get_config", config, http.StatusOK)
	child := call("create_key", scopeBody("child", keyScopeJSON{Project: "billing", Permissions: []store.Permission{store.ConfigRead}}), http.StatusCreated)
	childToken, _ := child["token"].(string)
	childKey, _ := child["key"].(map[string]any)
	call("list_keys", map[string]any{"project": "billing"}, http.StatusOK)
	update := scopeBody("disabled")
	update["id"] = childKey["id"]
	call("update_key", update, http.StatusOK)
	keyRequest(t, router, childToken, http.MethodGet, "/api/v1/projects/billing/environments/staging/items/db.pool", nil, http.StatusForbidden)
	rotated := call("rotate_key", map[string]any{"id": childKey["id"]}, http.StatusOK)
	newToken, _ := rotated["token"].(string)
	if newToken == "" || newToken == childToken {
		t.Fatal("rotation did not replace token")
	}
	keyRequest(t, router, childToken, http.MethodPost, "/mcp", nil, http.StatusUnauthorized)
	call("delete_key", map[string]any{"id": childKey["id"]}, http.StatusNoContent)
	keyRequest(t, router, newToken, http.MethodPost, "/mcp", nil, http.StatusUnauthorized)
	user, err := s.store.CreateUser(t.Context(), store.User{Email: "managed@example.com", Name: "Managed", Role: store.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	call("list_users", nil, http.StatusOK)
	call("update_user_role", map[string]any{"id": user.ID, "role": "admin"}, http.StatusOK)
	updated, err := s.store.UserByID(t.Context(), user.ID)
	if err != nil || updated.Role != store.RoleAdmin {
		t.Fatal("role did not change")
	}
	call("update_user_role", map[string]any{"id": admin.ID, "role": "user"}, http.StatusBadRequest)
	call("delete_user", map[string]any{"id": admin.ID}, http.StatusBadRequest)
	call("delete_user", map[string]any{"id": user.ID}, http.StatusNoContent)
	call("get_settings", nil, http.StatusOK)
	call("update_settings", map[string]any{"allow_registration": false}, http.StatusNoContent)
	allowed, err := s.store.RegistrationAllowed(t.Context())
	if err != nil || allowed {
		t.Fatal("false setting was not saved")
	}
	call("delete_config", config, http.StatusNoContent)
	call("delete_environment", map[string]any{"project": "billing", "environment": "staging"}, http.StatusNoContent)
	call("delete_project", map[string]any{"project": "billing"}, http.StatusNoContent)
	for _, op := range s.apiOperations() {
		if !seen[op.Name] {
			t.Errorf("API operation %s was not exercised over MCP", op.Name)
		}
	}
	for _, route := range router.Routes() {
		if strings.HasPrefix(route.Path, "/api/v1/") && !slices.ContainsFunc(s.apiOperations(), func(op apiOperation) bool { return "/api/v1"+op.Path == route.Path && op.Method == route.Method }) {
			t.Errorf("API route %s %s has no MCP operation", route.Method, route.Path)
		}
	}
}

func TestMCPTransport(t *testing.T) {
	f := newFlows(t)
	token := authorizeMCP(t, f).Access
	for _, tc := range []struct {
		method, origin, version string
		want                    int
	}{
		{http.MethodPost, "http://cc.test", mcpProtocol, http.StatusOK},
		{http.MethodPost, "https://untrusted.example", mcpProtocol, http.StatusForbidden},
		{http.MethodPost, "null", mcpProtocol, http.StatusForbidden},
		{http.MethodPost, "", "unsupported", http.StatusBadRequest},
		{http.MethodGet, "http://cc.test", "", http.StatusMethodNotAllowed},
		{http.MethodDelete, "http://cc.test", "", http.StatusMethodNotAllowed},
		{http.MethodGet, "https://untrusted.example", "", http.StatusForbidden},
	} {
		req := httptest.NewRequest(tc.method, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("MCP-Protocol-Version", tc.version)
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s %s: %d, want %d", tc.method, tc.origin, rec.Code, tc.want)
		}
	}
}

func TestMCPOAuthProjectOwnership(t *testing.T) {
	s, router, _ := testServer(t, false)
	user, err := s.store.CreateUser(t.Context(), store.User{Email: "owner@example.com", Name: "Owner", Role: store.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.CreateSession(t.Context(), store.Session{ID: "owner-session", UserID: user.ID, CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f := &flows{t: t, router: router, csrf: "csrf", cookies: map[string]*http.Cookie{"cc_session": {Name: "cc_session", Value: "owner-session"}}}
	createKeyProject(t, f, "owned")
	token := authorizeMCP(t, f).Access
	for _, tc := range []struct {
		name      string
		args      map[string]any
		forbidden bool
	}{
		{"set_config", map[string]any{"project": "owned", "environment": "production", "key": "x", "value": "1"}, false},
		{"set_config", map[string]any{"project": "configcenter", "environment": "production", "key": "x", "value": "1"}, true},
		{"list_users", nil, true},
		{"get_project", map[string]any{"project": "configcenter"}, true},
		{"get_project", map[string]any{"project": "owned"}, false},
		{"list_values", map[string]any{"project": "configcenter", "environment": "production"}, true},
		{"get_settings", nil, true},
		{"create_key", scopeBody("forbidden", keyScopeJSON{Project: "*", Permissions: []store.Permission{store.SettingsWrite}}), true},
	} {
		want := http.StatusOK
		if tc.forbidden {
			want = http.StatusForbidden
		}
		mcpCall(t, router, token, tc.name, tc.args, want)
	}
}
