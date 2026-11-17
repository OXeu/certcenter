package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/OXeu/certcenter/internal/apikey"
	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/store"
)

type keyJSON struct {
	ID         uint           `json:"id"`
	Name       string         `json:"name"`
	Prefix     string         `json:"prefix"`
	Scopes     []keyScopeJSON `json:"scopes"`
	CreatedAt  time.Time      `json:"created_at"`
	LastUsedAt *time.Time     `json:"last_used_at"`
	ExpiresAt  *time.Time     `json:"expires_at"`
}

type keyInput struct {
	Name      string          `json:"name"`
	Scopes    *[]keyScopeJSON `json:"scopes"`
	ExpiresAt *time.Time      `json:"expires_at"`
}

func (s *Server) keysPage(c *gin.Context) {
	s.render(c, http.StatusOK, gin.H{"Page": "keys", "Title": "title.keys"})
}

func (s *Server) keyViews(c *gin.Context, keys []store.APIKey) ([]keyJSON, bool) {
	projects, err := s.store.ListProjects(c.Request.Context())
	if err != nil {
		s.fail(c, err)
		return nil, false
	}
	names := map[uint]string{0: "*"}
	for _, project := range projects {
		names[project.ID] = project.Slug
	}
	out := make([]keyJSON, 0, len(keys))
	for _, key := range keys {
		scopes := make([]keyScopeJSON, 0, len(key.Scopes))
		for _, scope := range key.Scopes {
			project, ok := names[scope.ProjectID]
			if !ok {
				project = fmt.Sprintf("#%d", scope.ProjectID)
			}
			scopes = append(scopes, keyScopeJSON{Project: project, Permissions: scope.Permissions})
		}
		out = append(out, keyJSON{ID: key.ID, Name: key.Name, Prefix: key.Prefix, Scopes: scopes, CreatedAt: key.CreatedAt, LastUsedAt: key.LastUsedAt, ExpiresAt: key.ExpiresAt})
	}
	return out, true
}

func (s *Server) apiListKeys(c *gin.Context) {
	projectID, ok := s.keyProject(c)
	if !ok {
		return
	}
	id, _ := auth.FromContext(c.Request.Context())
	if id.APIKey != nil && !id.APIKey.HasPermission(store.KeysRead) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "keys:read scope required"})
		return
	}
	keys, err := s.store.ListAPIKeys(c.Request.Context())
	if err != nil {
		s.fail(c, err)
		return
	}
	allowed := make([]store.APIKey, 0, len(keys))
	for _, key := range keys {
		if (projectID == 0 || key.HasProject(projectID)) && canManageKey(id, key, store.KeysRead) {
			allowed = append(allowed, key)
		}
	}
	if views, ok := s.keyViews(c, allowed); ok {
		c.JSON(http.StatusOK, gin.H{"keys": views})
	}
}

func (s *Server) keyInput(c *gin.Context) (keyInput, []store.KeyScope, bool) {
	var in keyInput
	if err := bindJSON(c, &in); err != nil {
		s.fail(c, err)
		return in, nil, false
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 128 {
		s.fail(c, fmt.Errorf("%w: key name is required and must not exceed 128 bytes", errBadRequest))
		return in, nil, false
	}
	id, _ := auth.FromContext(c.Request.Context())
	if id.User != nil && !id.User.Role.CanWrite() {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "write access required"})
		return in, nil, false
	}
	if in.Scopes == nil {
		s.fail(c, fmt.Errorf("%w: scopes are required", errBadRequest))
		return in, nil, false
	}
	scopes, ok := s.parseKeyScopes(c, *in.Scopes)
	return in, scopes, ok
}

func (s *Server) apiCreateKey(c *gin.Context) {
	in, scopes, ok := s.keyInput(c)
	if !ok {
		return
	}
	token, digest, err := apikey.Generate()
	if err != nil {
		s.fail(c, err)
		return
	}
	id, _ := auth.FromContext(c.Request.Context())
	key, err := s.store.CreateAPIKey(c.Request.Context(), store.APIKey{Name: in.Name, Scopes: scopes, Digest: digest[:], Prefix: apikey.Display(token), CreatedBy: id.ActorID(), CreatedAt: time.Now().UTC(), ExpiresAt: in.ExpiresAt})
	if err != nil {
		s.fail(c, err)
		return
	}
	if views, ok := s.keyViews(c, []store.APIKey{key}); ok {
		c.JSON(http.StatusCreated, gin.H{"key": views[0], "token": token})
	}
}

func (s *Server) managedKey(c *gin.Context) (store.APIKey, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		s.fail(c, fmt.Errorf("%w: invalid key id", errBadRequest))
		return store.APIKey{}, false
	}
	key, err := s.store.APIKeyByID(c.Request.Context(), uint(id))
	if err != nil {
		s.fail(c, err)
		return key, false
	}
	projectID, ok := s.keyProject(c)
	if !ok {
		return key, false
	}
	if projectID != 0 && !key.HasProject(projectID) {
		s.fail(c, store.ErrNotFound)
		return key, false
	}
	identity, _ := auth.FromContext(c.Request.Context())
	if !canManageKey(identity, key, store.KeysWrite) || (identity.User != nil && !identity.User.Role.CanWrite()) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cannot manage this api key"})
		return key, false
	}
	return key, true
}

func (s *Server) apiUpdateKey(c *gin.Context) {
	key, ok := s.managedKey(c)
	if !ok {
		return
	}
	in, scopes, ok := s.keyInput(c)
	if !ok {
		return
	}
	if err := s.store.UpdateAPIKey(c.Request.Context(), key.ID, key.Revision, in.Name, scopes); err != nil {
		s.fail(c, err)
		return
	}
	key.Name, key.Scopes = in.Name, scopes
	if views, ok := s.keyViews(c, []store.APIKey{key}); ok {
		c.JSON(http.StatusOK, gin.H{"key": views[0]})
	}
}

func (s *Server) apiDeleteKey(c *gin.Context) {
	key, ok := s.managedKey(c)
	if !ok {
		return
	}
	if err := s.store.DeleteAPIKey(c.Request.Context(), key.ID, key.Revision); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) apiRotateKey(c *gin.Context) {
	key, ok := s.managedKey(c)
	if !ok {
		return
	}
	token, digest, err := apikey.Generate()
	if err != nil {
		s.fail(c, err)
		return
	}
	if err := s.store.RotateAPIKey(c.Request.Context(), key.ID, key.Revision, digest[:], apikey.Display(token)); err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token})
}
