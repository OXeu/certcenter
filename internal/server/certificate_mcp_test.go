package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/OXeu/certcenter/internal/store"
	"github.com/gin-gonic/gin"
)

func TestCertificateMCPMatchesEveryAPIOperation(t *testing.T) {
	f, s := settingsFlows(t, false)
	grant := authorizeMCP(t, f)
	seen := map[string]bool{}
	call := func(name string, args map[string]any, want int) map[string]any {
		t.Helper()
		seen[name] = true
		return mcpCall(t, f.router, grant.Access, name, args, want)
	}
	call("get_identity", nil, 200)
	call("list_domains", nil, 200)
	call("create_domain", map[string]any{"name": "new.example", "owner": "admin@example.com"}, 201)
	view := call("get_domain", map[string]any{"domain": "example.com"}, 200)
	policy := view["domain"].(map[string]any)["certificates"]
	call("update_domain", map[string]any{"domain": "example.com", "version": view["version"], "certificates": policy}, 204)
	call("update_domain", map[string]any{"domain": "example.com", "version": "stale", "certificates": policy}, 409)
	call("check_delegations", map[string]any{"domain": "example.com"}, 200)
	call("queue_certificate", map[string]any{"domain": "example.com", "policy": "main", "action": "issue"}, 202)
	call("queue_certificate", map[string]any{"domain": "example.com", "policy": "main", "action": "issue"}, 409)
	empty := call("get_domain", map[string]any{"domain": "new.example"}, 200)
	call("update_domain", map[string]any{"domain": "new.example", "version": empty["version"], "certificates": nil}, 204)
	settings := call("get_certificate_settings", nil, 200)
	call("update_certificate_settings", map[string]any{"section": "access", "version": settings["server_version"], "values": map[string]any{"allow_registration": false}}, 200)
	for _, provider := range []struct {
		plural, singular, field string
		value                   map[string]any
	}{
		{"dns_providers", "dns_provider", "dns", map[string]any{"kind": "alidns", "access_key": "id", "secret_key": "secret"}},
		{"upstreams", "upstream", "upstream", map[string]any{"kind": "aliyun", "access_key": "id", "secret_key": "secret", "update_resources": false, "delete_old": false}},
	} {
		list := call("list_"+provider.plural, nil, 200)
		call("create_"+provider.singular, map[string]any{"version": list["version"], "name": "mcp-test", provider.field: provider.value}, 204)
		list = call("list_"+provider.plural, nil, 200)
		call("update_"+provider.singular, map[string]any{"version": list["version"], "name": "mcp-test", provider.field: provider.value}, 204)
		list = call("list_"+provider.plural, nil, 200)
		call("delete_"+provider.singular, map[string]any{"version": list["version"], "name": "mcp-test"}, 204)
	}
	call("get_key_scopes", nil, 200)
	created := call("create_key", scopeBody("mcp-key", keyScopeJSON{Domain: "example.com", Permissions: []store.Permission{store.DomainsRead}}), 201)
	key := created["key"].(map[string]any)
	call("list_keys", nil, 200)
	call("update_key", map[string]any{"id": key["id"], "name": "disabled", "scopes": []any{}}, 200)
	call("rotate_key", map[string]any{"id": key["id"]}, 200)
	call("delete_key", map[string]any{"id": key["id"]}, 204)
	user, err := s.store.CreateUser(t.Context(), store.User{Name: "Managed", Email: "managed@example.com", Role: store.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	call("list_users", nil, 200)
	call("update_user_role", map[string]any{"id": user.ID, "role": "admin"}, 200)
	call("delete_user", map[string]any{"id": user.ID}, 204)
	listed := mcpRequest(t, f.router, grant.Access, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, 200)
	var tools struct{ Result struct{ Tools []mcpTool } }
	if err := json.Unmarshal(listed.Body.Bytes(), &tools); err != nil {
		t.Fatal(err)
	}
	ops := s.apiOperations()
	if len(tools.Result.Tools) != len(ops) {
		t.Fatal("discovery incomplete")
	}
	for _, op := range ops {
		if !seen[op.Name] {
			t.Errorf("API operation %s not exercised over MCP", op.Name)
		}
	}
	router, ok := f.router.(*gin.Engine)
	if !ok {
		t.Fatal("router type")
	}
	for _, route := range router.Routes() {
		if strings.HasPrefix(route.Path, "/api/v1/") && !slices.ContainsFunc(ops, func(op apiOperation) bool { return "/api/v1"+op.Path == route.Path && op.Method == route.Method }) {
			t.Errorf("API route missing from MCP: %s %s", route.Method, route.Path)
		}
	}
}

func TestCertificateMCPIsolationAndValidation(t *testing.T) {
	f, _ := certificateFlows(t)
	token := authorizeMCP(t, f).Access
	mcpCall(t, f.router, token, "get_domain", map[string]any{"domain": "example.com"}, 200)
	mcpCall(t, f.router, token, "get_domain", map[string]any{"domain": "other.example"}, 404)
	mcpCall(t, f.router, token, "queue_certificate", map[string]any{"domain": "other.example", "policy": "main", "action": "issue"}, 404)
	for _, name := range []string{"list_users", "get_certificate_settings", "list_dns_providers", "list_upstreams"} {
		mcpCall(t, f.router, token, name, nil, 403)
	}
	listed := mcpRequest(t, f.router, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, 200)
	if strings.Contains(listed.Body.String(), `"name":"list_users"`) {
		t.Fatal("admin tools exposed")
	}
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{"unknown", nil},
		{"get_domain", nil},
		{"get_domain", map[string]any{"domain": true}},
		{"list_domains", map[string]any{"url": "https://evil.example"}},
		{"queue_certificate", map[string]any{"domain": "example.com", "policy": "main", "action": "remove"}},
		{"update_domain", map[string]any{"domain": "example.com", "version": "x", "certificates": []any{map[string]any{"names": []any{true}}}}},
	} {
		response := mcpRequest(t, f.router, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": test.name, "arguments": test.args}}, 200)
		var reply struct{ Error *rpcError }
		if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		if reply.Error == nil || reply.Error.Code != codeInvalidParams {
			t.Fatal("invalid arguments accepted", test.name)
		}
	}
	keyRequest(t, f.router, token, http.MethodGet, "/api/v1/domains", nil, 401)
}
