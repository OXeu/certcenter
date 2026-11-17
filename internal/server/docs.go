package server

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/OXeu/certcenter"
)

func (s *Server) mountDocs(r *gin.Engine) {
	r.GET("/docs", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/docs/") })
	r.GET("/docs/*page", s.serveDocs)
}

func (s *Server) serveDocs(c *gin.Context) {
	name := strings.TrimPrefix(c.Param("page"), "/")
	raw := strings.HasSuffix(name, ".md")
	name = strings.TrimSuffix(name, ".md")
	if name == "" {
		name = "index"
	}
	switch name {
	case "index", "api", "sdk", "mcp", "config", "operations":
	default:
		c.Status(http.StatusNotFound)
		return
	}
	source, err := certcenter.Documentation.ReadFile("docs/" + name + ".md")
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	if raw {
		c.Data(http.StatusOK, "text/markdown; charset=utf-8", source)
		return
	}
	body, err := certcenter.RenderedDocumentation.ReadFile(".tmp/docs/" + name + ".html")
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	s.render(c, http.StatusOK, gin.H{
		"Page": "docs", "Title": "nav.docs", "DocPage": name,
		"DocumentHTML": template.HTML(body), //nolint:gosec // Only build-time rendered documentation is embedded; the Markdown renderer disables raw HTML.
	})
}

func serveDocStyles(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=3600")
	serveStaticFile(c, certcenter.RenderedDocumentation, ".tmp/docs/shiki.css")
}
