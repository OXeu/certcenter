package server

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/gin-gonic/gin"
)

func (s *Server) mountCertificates(r *gin.Engine) {
	ui := r.Group("", auth.RequireUser())
	ui.GET("/domains", func(c *gin.Context) { s.render(c, http.StatusOK, gin.H{"Page": "domains", "Title": "cert.domains"}) })
	ui.GET("/domains/:domain", func(c *gin.Context) {
		domain, ok := s.authorizeDomain(c)
		if !ok {
			return
		}
		s.render(c, http.StatusOK, gin.H{"Page": "certificates", "Title": domain.Name, "Domain": domain.Name})
	})
	ui.GET("/settings", auth.RequireAdmin(), func(c *gin.Context) {
		s.render(c, http.StatusOK, gin.H{"Page": "certificate-settings", "Title": "title.settings"})
	})
	ui.GET("/users", auth.RequireAdmin(), s.usersPage)
	r.GET("/projects", func(c *gin.Context) { c.Redirect(http.StatusFound, "/domains") })
	api := r.Group("/api/v1", auth.RequireUser())
	api.GET("/domains", s.certificateDomains)
	api.POST("/domains", auth.RequireAdmin(), s.createCertificateDomain)
	api.GET("/domains/:domain", s.certificateDomain)
	api.PUT("/domains/:domain", s.saveCertificateDomain)
	api.POST("/domains/:domain/certificates/:policy/:action", s.queueCertificate)
	api.GET("/certificate-settings", auth.RequireAdmin(), s.certificateSettings)
	api.GET("/users", auth.RequireAdmin(), s.apiListUsers)
	api.PATCH("/users/:id", auth.RequireAdmin(), s.apiSetRole)
	api.DELETE("/users/:id", auth.RequireAdmin(), s.apiDeleteUser)
}

func (s *Server) authorizeDomain(c *gin.Context) (certconfig.Domain, bool) {
	domain, err := s.cfg.Remote.Domain(c.Request.Context(), c.Param("domain"))
	if err != nil {
		s.certificateError(c, err)
		return domain, false
	}
	me, ok := s.me(c)
	if !ok || (me.Role != store.RoleAdmin && !strings.EqualFold(me.Email, domain.Owner)) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "domain not found"})
		return domain, false
	}
	return domain, true
}

func (s *Server) certificateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, certconfig.ErrMissing), errors.Is(err, store.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	case errors.Is(err, store.ErrBusy):
		c.JSON(http.StatusConflict, gin.H{"error": "certificate operation already queued or running"})
	default:
		s.log.Error("certificate request failed", "path", c.Request.URL.Path)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "certificate service unavailable; check ConfigCenter and database connectivity"})
	}
}

func (s *Server) certificateDomains(c *gin.Context) {
	domains, err := s.cfg.Remote.Domains(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	me, _ := s.me(c)
	out := []gin.H{}
	for _, d := range domains {
		if me.Role != store.RoleAdmin && !strings.EqualFold(me.Email, d.Owner) {
			continue
		}
		out = append(out, gin.H{"name": d.Name, "owner": d.Owner, "count": len(d.Certificates)})
	}
	c.JSON(http.StatusOK, gin.H{"domains": out})
}

func (s *Server) createCertificateDomain(c *gin.Context) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	var in certconfig.Domain
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := bindJSON(c, &in); err != nil {
		s.fail(c, err)
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Owner = strings.ToLower(strings.TrimSpace(in.Owner))
	if !certconfig.ValidDomain(in.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid domain"})
		return
	}
	if _, err := s.cfg.Remote.Domain(c.Request.Context(), in.Name); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "domain already exists"})
		return
	} else if !errors.Is(err, certconfig.ErrMissing) {
		s.certificateError(c, err)
		return
	}
	if err := s.cfg.Remote.SaveDomain(c.Request.Context(), in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot save domain; check domain, owner and ConfigCenter write access"})
		return
	}
	c.JSON(http.StatusCreated, in)
}

func domainVersion(d certconfig.Domain) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v", d))))
}

func (s *Server) certificateDomain(c *gin.Context) {
	domain, ok := s.authorizeDomain(c)
	if !ok {
		return
	}
	providers, err := s.cfg.Remote.Providers(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	certificates, err := s.store.Certificates(c.Request.Context(), domain.Name)
	if err != nil {
		s.certificateError(c, err)
		return
	}
	jobs, err := s.store.CertificateJobs(c.Request.Context(), domain.Name)
	if err != nil {
		s.certificateError(c, err)
		return
	}
	events, err := s.store.CertificateEvents(c.Request.Context(), domain.Name)
	if err != nil {
		s.certificateError(c, err)
		return
	}
	dns, upstreams := providerNames(providers)
	delegations := []gin.H{}
	for _, p := range domain.Certificates {
		provider := providers.DNS[p.DNSProvider]
		if provider.Kind != "acme-dns" {
			continue
		}
		for _, name := range p.Names {
			base := strings.TrimPrefix(name, "*.")
			if a, ok := provider.Accounts[base]; ok {
				delegations = append(delegations, gin.H{"name": "_acme-challenge." + base, "target": a.FullDomain})
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"domain": domain, "version": domainVersion(domain), "certificates": certificates, "jobs": jobs, "events": events, "dns_providers": dns, "upstreams": upstreams, "delegations": delegations})
}

func (s *Server) saveCertificateDomain(c *gin.Context) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	domain, ok := s.authorizeDomain(c)
	if !ok {
		return
	}
	var in struct {
		Version      string              `json:"version"`
		Certificates []certconfig.Policy `json:"certificates"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	if err := bindJSON(c, &in); err != nil {
		s.fail(c, err)
		return
	}
	if in.Version != domainVersion(domain) {
		c.JSON(http.StatusConflict, gin.H{"error": "domain settings changed; refresh before saving"})
		return
	}
	domain.Certificates = in.Certificates
	providers, err := s.cfg.Remote.Providers(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	if err := domain.Validate(providers); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.cfg.Remote.SaveDomain(c.Request.Context(), domain); err != nil {
		s.certificateError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) queueCertificate(c *gin.Context) {
	domain, ok := s.authorizeDomain(c)
	if !ok {
		return
	}
	action := c.Param("action")
	if action != "issue" && action != "deploy" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid action"})
		return
	}
	job, err := s.certificates.Queue(c.Request.Context(), domain.Name, c.Param("policy"), action)
	if err != nil {
		if errors.Is(err, store.ErrBusy) {
			s.certificateError(c, err)
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot queue operation; check certificate policy and deployment targets"})
		return
	}
	c.JSON(http.StatusAccepted, job)
}

func providerNames(providers certconfig.Providers) (dns, upstreams []string) {
	dns, upstreams = []string{}, []string{}
	for name := range providers.DNS {
		dns = append(dns, name)
	}
	for name := range providers.Upstreams {
		upstreams = append(upstreams, name)
	}
	sort.Strings(dns)
	sort.Strings(upstreams)
	return dns, upstreams
}

func (s *Server) certificateSettings(c *gin.Context) {
	providers, err := s.cfg.Remote.Providers(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	dns, upstreams := providerNames(providers)
	c.JSON(http.StatusOK, gin.H{"configcenter_url": s.cfg.ConfigCenterURL, "dns_providers": dns, "upstreams": upstreams, "directory_url": providers.ACME.DirectoryURL, "email": providers.ACME.Email, "accept_terms": providers.ACME.AcceptTerms})
}
