package server

import (
	"net/http"
	"strconv"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/gin-gonic/gin"
)

func (s *Server) allowProject(c *gin.Context, project store.Project) bool {
	id, _ := auth.FromContext(c.Request.Context())
	if id.APIKey != nil && id.APIKey.HasProject(project.ID) {
		return true
	}
	me, ok := s.me(c)
	if !ok || (me.Role != store.RoleAdmin && project.OwnerID != me.ID) {
		c.AbortWithStatus(http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) apiDeleteUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	actor, _ := auth.FromContext(c.Request.Context())
	if err != nil || id == 0 || uint(id) == actor.ActorID() {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteUser(c.Request.Context(), uint(id), actor.ActorID()); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
