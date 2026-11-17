package server

import (
	"net/http"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/gin-gonic/gin"
)

func (s *Server) mountMCPAPI() {
	s.mcpAPI = gin.New()
	s.mcpAPI.UseRawPath = true
	s.mcpAPI.UnescapePathValues = true
	for _, op := range s.apiOperations() {
		handlers := []gin.HandlerFunc{op.authorize}
		switch {
		case op.Certificate:
			handlers = append(handlers, s.authorizeCertificateOperation(op), op.Handler)
		case op.ProjectHandler != nil:
			handlers = append(handlers, s.withProject(op.Permissions[0], op.ProjectHandler))
		case op.Agent:
			handlers = append(handlers, s.mcpProject(op), op.Handler)
		default:
			handlers = append(handlers, op.Handler)
		}
		s.mcpAPI.Handle(op.Method, "/api/v1"+op.Path, handlers...)
	}
}

func (s *Server) mcpProject(op apiOperation) gin.HandlerFunc {
	return func(c *gin.Context) {
		project, err := s.store.ProjectBySlug(c.Request.Context(), c.Query("project"))
		if err != nil {
			s.fail(c, err)
			c.Abort()
			return
		}
		if !s.allowProject(c, project) {
			c.Abort()
			return
		}
		id, _ := auth.FromContext(c.Request.Context())
		for _, p := range op.Permissions {
			if !id.Allows(project.ID, p) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
		}
		id.Project = &project
		c.Request = c.Request.WithContext(auth.WithIdentity(c.Request.Context(), id))
		c.Next()
	}
}
