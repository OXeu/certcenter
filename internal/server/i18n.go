package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/OXeu/certcenter/internal/i18n"
)

const (
	languageCookie = "cc_lang"
	languageQuery  = "lang"
)

// languageMiddleware resolves the request language from ?lang=, the cookie
// and Accept-Language, in that order, then stores the translator.
func (s *Server) languageMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tag := s.catalog.Negotiate(s.cfg.Languages, c.GetHeader("Accept-Language"))
		for _, candidate := range []string{c.Query(languageQuery), cookieValue(c, languageCookie)} {
			if s.catalog.Supported(candidate, s.cfg.Languages) {
				tag = candidate
				break
			}
		}
		tr := s.catalog.Translator(tag)
		c.Set("cc_lang", tr.Lang())
		c.Set("cc_t", tr)
		c.Next()
	}
}

func cookieValue(c *gin.Context, name string) string {
	raw, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return raw
}

// translator returns the request translator, falling back to negotiation for
// handlers that run outside the middleware chain.
func (s *Server) translator(c *gin.Context) *i18n.Translator {
	if value, ok := c.Get("cc_t"); ok {
		if tr, isTranslator := value.(*i18n.Translator); isTranslator {
			return tr
		}
	}
	tag := s.catalog.Negotiate(s.cfg.Languages, c.GetHeader("Accept-Language"))
	return s.catalog.Translator(tag)
}

// MountLanguage registers the switcher endpoint. It only accepts enabled
// languages, stores the choice in a cookie and redirects back to the page
// the request came from.
func (s *Server) MountLanguage(r *gin.Engine) {
	r.GET("/language", func(c *gin.Context) {
		tag := c.Query(languageQuery)
		if !s.catalog.Supported(tag, s.cfg.Languages) {
			tag = s.catalog.Resolve(s.cfg.Languages)
		}
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     languageCookie,
			Value:    tag,
			Path:     "/",
			MaxAge:   int((365 * 24 * 60 * 60)),
			HttpOnly: true,
			Secure:   c.Request.TLS != nil,
			SameSite: http.SameSiteLaxMode,
		})
		c.Redirect(http.StatusFound, nextPage(c.GetHeader("Referer")))
	})
}

// nextPage turns a same-origin referer into a local path; anything that is
// not a clean absolute path falls back to the projects page, so the cookie
// endpoint cannot be used as an open redirect.
func nextPage(referer string) string {
	u, err := url.Parse(referer)
	if err != nil || u.Host == "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/projects"
	}
	return u.RequestURI()
}
