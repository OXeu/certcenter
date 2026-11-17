package server

import (
	"net/http"
	"reflect"
	"strings"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/gin-gonic/gin"
)

func objectArgument(name string, value any, required bool) mcpArgument {
	arg := argument(name, "body", required)
	arg.Schema = typeSchema(reflect.TypeOf(value))
	return arg
}

func typeSchema(t reflect.Type) map[string]any {
	switch {
	case t.Kind() == reflect.Bool:
		return map[string]any{"type": "boolean"}
	case t.Kind() == reflect.Int || t.Kind() == reflect.Uint:
		return map[string]any{"type": "integer"}
	case t.Kind() == reflect.Slice:
		return map[string]any{"type": []string{"array", "null"}, "items": typeSchema(t.Elem())}
	case t.Kind() == reflect.Map:
		return map[string]any{"type": []string{"object", "null"}, "properties": map[string]any{}, "required": []string{}, "additionalProperties": typeSchema(t.Elem())}
	case t.Kind() == reflect.Struct:
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				props[name] = typeSchema(field.Type)
			}
		}
		return schema(props)
	default:
		return map[string]any{"type": "string"}
	}
}

func (s *Server) certificateOperations() []apiOperation {
	domain := argument("domain", "path", true)
	version := argument("version", "body", true)
	id := mcpArgument{Name: "id", Target: "id", Location: "path", Required: true, Schema: map[string]any{"type": "integer", "minimum": 1}}
	section := argument("section", "path", true)
	section.Schema["enum"] = []string{"general", "access", "oauth", "dns", "database", "acme"}
	values := typedArgument("values", "object", true)
	values.Schema = map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "additionalProperties": true}
	action := argument("action", "path", true)
	action.Schema["enum"] = []string{"issue", "deploy"}
	ops := []apiOperation{
		{Name: "get_identity", Description: "Read the current identity.", Method: http.MethodGet, Path: "/me", Handler: s.apiMe},
		{Name: "list_domains", Description: "List accessible certificate domains.", Method: http.MethodGet, Path: "/domains", Handler: s.certificateDomains, Permissions: []store.Permission{store.DomainsRead}},
		{Name: "create_domain", Description: "Create a certificate domain and assign its owner.", Method: http.MethodPost, Path: "/domains", Handler: s.createCertificateDomain, Admin: true, Permissions: []store.Permission{store.DomainsWrite}, Arguments: []mcpArgument{argument("name", "body", true), argument("owner", "body", true), objectArgument("certificates", []certconfig.Policy{}, false)}},
		{Name: "get_domain", Description: "Read domain policies, certificates, jobs, events and DNS delegations.", Method: http.MethodGet, Path: "/domains/:domain", Handler: s.certificateDomain, Permissions: []store.Permission{store.DomainsRead}, Arguments: []mcpArgument{domain}},
		{Name: "update_domain", Description: "Replace certificate policies using the current domain version.", Method: http.MethodPut, Path: "/domains/:domain", Handler: s.saveCertificateDomain, Permissions: []store.Permission{store.DomainsWrite}, Arguments: []mcpArgument{domain, version, objectArgument("certificates", []certconfig.Policy{}, true)}},
		{Name: "check_delegations", Description: "Check DNS delegation records for a domain.", Method: http.MethodPost, Path: "/domains/:domain/delegations/check", Handler: s.checkDomainDelegations, Permissions: []store.Permission{store.DomainsRead}, Arguments: []mcpArgument{domain}},
		{Name: "queue_certificate", Description: "Queue certificate issuance or deployment for a policy.", Method: http.MethodPost, Path: "/domains/:domain/certificates/:policy/:action", Handler: s.queueCertificate, Arguments: []mcpArgument{domain, argument("policy", "path", true), action}},
		{Name: "get_certificate_settings", Description: "Read server settings with credentials redacted.", Method: http.MethodGet, Path: "/certificate-settings", Handler: s.certificateSettings, Admin: true, Permissions: []store.Permission{store.SettingsRead}},
		{Name: "update_certificate_settings", Description: "Update a settings section using its current version. Values contain the section's API fields.", Method: http.MethodPut, Path: "/certificate-settings/:section", Handler: s.saveCertificateSettings, Admin: true, Permissions: []store.Permission{store.SettingsWrite}, Arguments: []mcpArgument{section, version, values}},
		{Name: "list_users", Description: "List user accounts.", Method: http.MethodGet, Path: "/users", Handler: s.apiListUsers, Admin: true, Permissions: []store.Permission{store.UsersRead}},
		{Name: "update_user_role", Description: "Change another user's role.", Method: http.MethodPatch, Path: "/users/:id", Handler: s.apiSetRole, Admin: true, Permissions: []store.Permission{store.UsersWrite}, Arguments: []mcpArgument{id, enumArgument("role", "user", "admin")}},
		{Name: "delete_user", Description: "Delete another user and revoke their credentials.", Method: http.MethodDelete, Path: "/users/:id", Handler: s.apiDeleteUser, Admin: true, Permissions: []store.Permission{store.UsersDelete}, Arguments: []mcpArgument{id}},
	}
	for _, provider := range []struct {
		path, name, field string
		dns               bool
		model             any
	}{
		{"dns-providers", "dns_provider", "dns", true, certconfig.DNS{}}, {"upstreams", "upstream", "upstream", false, certconfig.Upstream{}},
	} {
		ops = append(ops, apiOperation{Name: "list_" + provider.name + "s", Description: "List provider settings with credentials redacted.", Method: http.MethodGet, Path: "/" + provider.path, Admin: true, Permissions: []store.Permission{store.ProvidersRead}, Handler: func(c *gin.Context) { s.listProviders(c, provider.dns) }})
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			name, path, args := "create_"+provider.name, "/"+provider.path, []mcpArgument{version, argument("name", "body", true)}
			if method != http.MethodPost {
				name, path, args = "update_"+provider.name, path+"/:name", []mcpArgument{version, argument("name", "path", true)}
			}
			if method == http.MethodDelete {
				name = "delete_" + provider.name
			} else {
				args = append(args, objectArgument(provider.field, provider.model, true))
			}
			ops = append(ops, apiOperation{Name: name, Description: "Manage a provider using the current provider settings version.", Method: method, Path: path, Admin: true, Permissions: []store.Permission{store.ProvidersWrite}, Arguments: args, Handler: func(c *gin.Context) { s.saveProvider(c, provider.dns) }})
		}
	}
	for _, op := range s.configOperations() {
		if op.Path != "/key-scopes" && !strings.HasPrefix(op.Path, "/keys") {
			continue
		}
		if op.Name == "list_keys" {
			op.Arguments = nil
		}
		for i, arg := range op.Arguments {
			if arg.Name == "scopes" {
				permissions := []string{}
				for _, p := range store.CertificatePermissions() {
					permissions = append(permissions, string(p))
				}
				op.Arguments[i].Schema = map[string]any{"type": "array", "maxItems": 100, "items": schema(map[string]any{"domain": map[string]any{"type": "string"}, "permissions": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "enum": permissions}}}, "domain", "permissions")}
			}
		}
		ops = append(ops, op)
	}
	for i := range ops {
		ops[i].Certificate = true
	}
	return ops
}
