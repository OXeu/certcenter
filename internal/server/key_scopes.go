package server

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/store"
)

type keyScopeJSON struct {
	Project     string             `json:"project"`
	Permissions []store.Permission `json:"permissions"`
}

func canManageKey(id auth.Identity, key store.APIKey, permission store.Permission) bool {
	if id.User != nil {
		return id.User.Role == store.RoleAdmin || key.CreatedBy == id.User.ID
	}
	if id.APIKey == nil || !id.APIKey.Covers(key.Scopes) {
		return false
	}
	if len(key.Scopes) == 0 {
		return id.APIKey.Allows(0, permission) || (key.CreatedBy == id.ActorID() && id.APIKey.HasPermission(permission))
	}
	for _, scope := range key.Scopes {
		if !id.APIKey.Allows(scope.ProjectID, permission) {
			return false
		}
	}
	return true
}

func (s *Server) parseKeyScopes(c *gin.Context, input []keyScopeJSON) ([]store.KeyScope, bool) {
	if len(input) > 100 {
		s.fail(c, fmt.Errorf("%w: too many scopes", errBadRequest))
		return nil, false
	}
	id, _ := auth.FromContext(c.Request.Context())
	scopes := make([]store.KeyScope, 0, len(input))
	seen := map[string]bool{}
	for _, scope := range input {
		if scope.Project == "" || seen[scope.Project] || len(scope.Permissions) == 0 || len(scope.Permissions) > len(store.KeyPermissions()) {
			s.fail(c, fmt.Errorf("%w: scopes need a unique project and at least one permission", errBadRequest))
			return nil, false
		}
		seen[scope.Project] = true
		for index, permission := range scope.Permissions {
			if !permission.Valid() || slices.Contains(scope.Permissions[:index], permission) || (permission.GlobalOnly() && scope.Project != "*") {
				s.fail(c, fmt.Errorf("%w: unknown, duplicate or non-global administrative permission", errBadRequest))
				return nil, false
			}
		}
		var project store.Project
		if scope.Project != "*" {
			var err error
			project, err = s.store.ProjectBySlug(c.Request.Context(), scope.Project)
			if err != nil {
				s.fail(c, err)
				return nil, false
			}
		}
		if id.User != nil && id.User.Role != store.RoleAdmin && (project.ID == 0 || project.OwnerID != id.User.ID) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cannot grant access to this project"})
			return nil, false
		}
		scopes = append(scopes, store.KeyScope{ProjectID: project.ID, Permissions: scope.Permissions})
	}
	if id.APIKey != nil && !canManageKey(id, store.APIKey{Scopes: scopes, CreatedBy: id.ActorID()}, store.KeysWrite) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cannot grant scopes beyond this api key"})
		return nil, false
	}
	return scopes, true
}

func (s *Server) apiKeyScopes(c *gin.Context) {
	projects, err := s.store.ListProjects(c.Request.Context())
	if err != nil {
		s.fail(c, err)
		return
	}
	id, _ := auth.FromContext(c.Request.Context())
	available := make([]projectJSON, 0, len(projects))
	for _, project := range projects {
		if (id.APIKey != nil && id.APIKey.Allows(project.ID, store.ProjectsRead)) || (id.User != nil && (id.User.Role == store.RoleAdmin || project.OwnerID == id.User.ID)) {
			available = append(available, toProjectJSON(project))
		}
	}
	permissions := []store.Permission{}
	globalPermissions := []store.Permission{}
	for _, permission := range store.KeyPermissions() {
		if permission.GlobalOnly() {
			globalPermissions = append(globalPermissions, permission)
		}
		if (id.APIKey != nil && id.APIKey.HasPermission(permission)) || (id.User != nil && id.Allows(0, permission)) {
			permissions = append(permissions, permission)
		}
	}
	allProjects := id.User != nil && id.User.Role == store.RoleAdmin
	if id.APIKey != nil {
		allProjects = id.APIKey.Allows(0, store.KeysWrite)
	}
	c.JSON(http.StatusOK, gin.H{"projects": available, "permissions": permissions, "global_permissions": globalPermissions, "all_projects": allProjects})
}

func (s *Server) keyProject(c *gin.Context) (uint, bool) {
	slug := c.Query("project")
	if slug == "" {
		return 0, true
	}
	project, err := s.store.ProjectBySlug(c.Request.Context(), slug)
	if err != nil {
		s.fail(c, err)
		return 0, false
	}
	if !s.allowProject(c, project) {
		return 0, false
	}
	return project.ID, true
}
