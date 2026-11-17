package server

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/gin-gonic/gin"
)

func certificateUser(c *gin.Context) (*store.User, bool) {
	id, _ := auth.FromContext(c.Request.Context())
	if id.User != nil {
		return id.User, true
	}
	return id.KeyUser, id.KeyUser != nil
}

func (s *Server) certificateKeyIdentity(c *gin.Context) {
	id, _ := auth.FromContext(c.Request.Context())
	if id.APIKey != nil {
		user, err := s.store.UserByID(c.Request.Context(), id.ActorID())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key owner"})
			return
		}
		id.KeyUser = &user
		c.Request = c.Request.WithContext(auth.WithIdentity(c.Request.Context(), id))
	}
	c.Next()
}

func (s *Server) keyInMode(key store.APIKey) bool {
	for _, scope := range key.Scopes {
		if (scope.Domain != "") != (s.cfg.Remote != nil) {
			return false
		}
	}
	return true
}

func (op apiOperation) certificateAllowed(id auth.Identity) bool {
	user := id.User
	if id.APIKey != nil {
		user = id.KeyUser
	}
	if user == nil || !user.Role.CanWrite() || (op.Admin && user.Role != store.RoleAdmin) {
		return false
	}
	if id.APIKey == nil {
		return true
	}
	if op.Name == "queue_certificate" {
		return id.APIKey.HasPermission(store.CertificatesIssue) || id.APIKey.HasPermission(store.CertificatesDeploy)
	}
	for _, permission := range op.Permissions {
		if op.Admin {
			if !id.APIKey.AllowsDomain("*", permission) {
				return false
			}
		} else if !slices.ContainsFunc(id.APIKey.Scopes, func(scope store.KeyScope) bool {
			return scope.Domain != "" && slices.Contains(scope.Permissions, permission)
		}) {
			return false
		}
	}
	return true
}

func (s *Server) authorizeCertificateOperation(op apiOperation) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := auth.FromContext(c.Request.Context())
		permission := store.Permission("")
		if len(op.Permissions) > 0 {
			permission = op.Permissions[0]
		}
		if op.Name == "queue_certificate" {
			permission = store.CertificatesIssue
			if c.Param("action") == "deploy" {
				permission = store.CertificatesDeploy
			}
		}
		if id.APIKey != nil && c.Param("domain") != "" && !id.APIKey.AllowsDomain(c.Param("domain"), permission) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient api key scope"})
			return
		}
		c.Next()
	}
}

func (s *Server) parseDomainKeyScopes(c *gin.Context, input []keyScopeJSON) ([]store.KeyScope, bool) {
	if len(input) > 100 {
		s.fail(c, fmt.Errorf("%w: too many scopes", errBadRequest))
		return nil, false
	}
	id, _ := auth.FromContext(c.Request.Context())
	user, ok := certificateUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return nil, false
	}
	scopes := make([]store.KeyScope, 0, len(input))
	seen := map[string]bool{}
	for _, scope := range input {
		if scope.Project != "" || scope.Domain == "" || seen[scope.Domain] || len(scope.Permissions) == 0 || len(scope.Permissions) > len(store.CertificatePermissions()) {
			s.fail(c, fmt.Errorf("%w: scopes need a unique domain and permissions", errBadRequest))
			return nil, false
		}
		seen[scope.Domain] = true
		for index, permission := range scope.Permissions {
			if !slices.Contains(store.CertificatePermissions(), permission) || slices.Contains(scope.Permissions[:index], permission) || (permission.GlobalOnly() && scope.Domain != "*") {
				s.fail(c, fmt.Errorf("%w: invalid scope permission", errBadRequest))
				return nil, false
			}
		}
		if scope.Domain == "*" {
			if user.Role != store.RoleAdmin {
				c.AbortWithStatus(http.StatusForbidden)
				return nil, false
			}
		} else {
			domain, err := s.cfg.Remote.Domain(c.Request.Context(), scope.Domain)
			if err != nil {
				s.certificateError(c, err)
				return nil, false
			}
			if user.Role != store.RoleAdmin && !strings.EqualFold(user.Email, domain.Owner) {
				c.AbortWithStatus(http.StatusForbidden)
				return nil, false
			}
		}
		scopes = append(scopes, store.KeyScope{Domain: scope.Domain, Permissions: scope.Permissions})
	}
	if id.APIKey != nil && !canManageKey(id, store.APIKey{Scopes: scopes, CreatedBy: id.ActorID()}, store.KeysWrite) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cannot grant scopes beyond this api key"})
		return nil, false
	}
	return scopes, true
}

func (s *Server) domainKeyScopes(c *gin.Context) {
	domains, err := s.cfg.Remote.Domains(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	id, _ := auth.FromContext(c.Request.Context())
	user, _ := certificateUser(c)
	available := []gin.H{}
	for _, domain := range domains {
		if user.Role != store.RoleAdmin && !strings.EqualFold(user.Email, domain.Owner) {
			continue
		}
		if id.APIKey != nil && !id.APIKey.AllowsDomain(domain.Name, store.KeysWrite) {
			continue
		}
		available = append(available, gin.H{"name": domain.Name})
	}
	permissions, global := []store.Permission{}, []store.Permission{}
	for _, permission := range store.CertificatePermissions() {
		if permission.GlobalOnly() {
			global = append(global, permission)
			if user.Role != store.RoleAdmin {
				continue
			}
		}
		if id.APIKey == nil || id.APIKey.HasPermission(permission) {
			permissions = append(permissions, permission)
		}
	}
	all := user.Role == store.RoleAdmin && (id.APIKey == nil || id.APIKey.AllowsDomain("*", store.KeysWrite))
	c.JSON(http.StatusOK, gin.H{"domains": available, "permissions": permissions, "global_permissions": global, "all_domains": all})
}
