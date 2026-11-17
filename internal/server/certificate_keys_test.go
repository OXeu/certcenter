package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/OXeu/certcenter/internal/store"
)

func TestCertificateKeysScopesLifecycleAndDelegation(t *testing.T) {
	f, s := certificateFlows(t)
	if status, _ := f.do(http.MethodGet, "/keys", nil); status != 200 {
		t.Fatal("missing key management page")
	}
	parent, parentID := issueKey(t, f, "/api/v1/keys", scopeBody("automation", keyScopeJSON{Domain: "example.com", Permissions: []store.Permission{store.DomainsRead, store.KeysRead, store.KeysWrite, store.CertificatesIssue}}))
	read := keyRequest(t, f.router, parent, http.MethodGet, "/api/v1/domains", nil, 200)
	if strings.Contains(read.Body.String(), "other.example") {
		t.Fatal("key leaked another domain")
	}
	keyRequest(t, f.router, parent, http.MethodGet, "/api/v1/domains/example.com", nil, 200)
	keyRequest(t, f.router, parent, http.MethodGet, "/api/v1/domains/other.example", nil, 403)
	keyRequest(t, f.router, parent, http.MethodPut, "/api/v1/domains/example.com", map[string]any{}, 403)
	keyRequest(t, f.router, parent, http.MethodPost, "/api/v1/domains/example.com/certificates/main/deploy", nil, 403)
	keyRequest(t, f.router, parent, http.MethodPost, "/api/v1/domains/example.com/certificates/main/issue", nil, 202)
	for _, path := range []string{"/api/v1/dns-providers", "/api/v1/upstreams", "/api/v1/users", "/api/v1/certificate-settings"} {
		keyRequest(t, f.router, parent, http.MethodGet, path, nil, 403)
	}
	for _, scope := range []keyScopeJSON{
		{Domain: "other.example", Permissions: []store.Permission{store.DomainsRead}},
		{Domain: "*", Permissions: []store.Permission{store.DomainsRead}},
		{Domain: "example.com", Permissions: []store.Permission{store.CertificatesDeploy}},
	} {
		keyRequest(t, f.router, parent, http.MethodPost, "/api/v1/keys", scopeBody("escalate", scope), 403)
	}
	result := keyRequest(t, f.router, parent, http.MethodPost, "/api/v1/keys", scopeBody("child", keyScopeJSON{Domain: "example.com", Permissions: []store.Permission{store.DomainsRead}}), 201)
	var child struct {
		Token string
		Key   struct{ ID uint }
	}
	if err := json.Unmarshal(result.Body.Bytes(), &child); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/keys/%d", child.Key.ID)
	keyRequest(t, f.router, child.Token, http.MethodGet, "/api/v1/domains/example.com", nil, 200)
	keyRequest(t, f.router, parent, http.MethodPatch, path, scopeBody("disabled"), 200)
	keyRequest(t, f.router, child.Token, http.MethodGet, "/api/v1/domains", nil, 403)
	result = keyRequest(t, f.router, parent, http.MethodPost, path+"/rotate", nil, 200)
	var rotated struct{ Token string }
	if err := json.Unmarshal(result.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.Token == "" || rotated.Token == child.Token {
		t.Fatal("rotation reused secret")
	}
	keyRequest(t, f.router, child.Token, http.MethodGet, "/api/v1/domains", nil, 401)
	keyRequest(t, f.router, parent, http.MethodDelete, path, nil, 204)
	keyRequest(t, f.router, rotated.Token, http.MethodGet, "/api/v1/domains", nil, 401)
	list := keyRequest(t, f.router, parent, http.MethodGet, "/api/v1/keys", nil, 200)
	if strings.Contains(list.Body.String(), parent) || strings.Contains(list.Body.String(), "digest") {
		t.Fatal("key secret exposed")
	}
	f.csrf = ""
	if status, _ := f.do(http.MethodDelete, fmt.Sprintf("/api/v1/keys/%d", parentID), nil); status != 403 {
		t.Fatal("key mutation skipped CSRF")
	}
	f.csrf = "csrf"
	domain, err := s.cfg.Remote.Domain(t.Context(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	domain.Owner = "other@example.com"
	if err := s.cfg.Remote.SaveDomain(t.Context(), domain); err != nil {
		t.Fatal(err)
	}
	keyRequest(t, f.router, parent, http.MethodGet, "/api/v1/domains/example.com", nil, 404)
}

func TestCertificateKeysSeparateDomainsFromProjectsAndOwners(t *testing.T) {
	f, s := certificateFlows(t)
	for _, scope := range []keyScopeJSON{
		{Project: "*", Permissions: []store.Permission{store.DomainsRead}},
		{Domain: "example.com", Permissions: []store.Permission{store.ConfigRead}},
		{Domain: "example.com", Permissions: []store.Permission{store.UsersWrite}},
		{Domain: "example.com", Permissions: []store.Permission{store.DomainsRead, store.DomainsRead}},
	} {
		if status, _ := f.do(http.MethodPost, "/api/v1/keys", scopeBody("invalid", scope)); status != 400 {
			t.Fatal("invalid domain scope accepted", status)
		}
	}
	ownerKey, _ := issueKey(t, f, "/api/v1/keys", scopeBody("owner", keyScopeJSON{Domain: "example.com", Permissions: []store.Permission{store.DomainsRead}}))
	providerAdmin(t, f, s)
	adminKey, adminID := issueKey(t, f, "/api/v1/keys", scopeBody("admin", keyScopeJSON{Domain: "*", Permissions: store.CertificatePermissions()}))
	keyRequest(t, f.router, adminKey, http.MethodGet, "/api/v1/users", nil, 200)
	f.cookies["cc_session"].Value = "owner-session"
	path := fmt.Sprintf("/api/v1/keys/%d", adminID)
	if status, _ := f.do(http.MethodDelete, path, nil); status != 403 {
		t.Fatal("owner revoked administrator key")
	}
	admin, err := s.store.UserByEmail(t.Context(), "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetUserRole(t.Context(), admin.ID, store.RoleUser); err != nil {
		t.Fatal(err)
	}
	keyRequest(t, f.router, adminKey, http.MethodGet, "/api/v1/users", nil, 403)
	keyRequest(t, f.router, ownerKey, http.MethodGet, "/api/v1/domains/example.com", nil, 200)
	key := store.APIKey{Scopes: []store.KeyScope{{Domain: "example.com", Permissions: []store.Permission{store.DomainsRead}}}}
	if key.Allows(1, store.DomainsRead) || key.HasProject(1) {
		t.Fatal("domain grant became wildcard project grant")
	}
}
