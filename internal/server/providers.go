package server

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/gin-gonic/gin"
)

type providerView struct {
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields"`
}

type providerInput struct {
	Version  string              `json:"version"`
	Name     string              `json:"name"`
	DNS      certconfig.DNS      `json:"dns"`
	Upstream certconfig.Upstream `json:"upstream"`
}

func (s *Server) mountProviders(ui, api *gin.RouterGroup) {
	for _, route := range []struct {
		path, title string
		dns         bool
	}{
		{"dns-providers", "cert.dnsProviders", true},
		{"upstreams", "cert.upstreams", false},
	} {
		ui.GET("/"+route.path, auth.RequireAdmin(), func(c *gin.Context) {
			s.render(c, http.StatusOK, gin.H{"Page": route.path, "Title": route.title})
		})
		group := api.Group("/"+route.path, auth.RequireAdmin())
		group.GET("", func(c *gin.Context) { s.listProviders(c, route.dns) })
		group.POST("", func(c *gin.Context) { s.saveProvider(c, route.dns) })
		group.PUT("/:name", func(c *gin.Context) { s.saveProvider(c, route.dns) })
		group.DELETE("/:name", func(c *gin.Context) { s.saveProvider(c, route.dns) })
	}
}

func providersVersion(p certconfig.Providers) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v", p))))
}

func (s *Server) listProviders(c *gin.Context, dns bool) {
	p, err := s.cfg.Remote.Providers(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	dnsNames, upstreamNames := providerNames(p)
	views := []providerView{}
	if dns {
		for _, name := range dnsNames {
			d := p.DNS[name]
			views = append(views, providerView{Name: name, Fields: map[string]string{"kind": d.Kind, "region": d.Region, "api_base": d.APIBase}})
		}
	} else {
		for _, name := range upstreamNames {
			u := p.Upstreams[name]
			views = append(views, providerView{Name: name, Fields: map[string]string{
				"kind": u.Kind, "region": u.Region, "address": u.Address, "user": u.User,
				"host_key": u.HostKey, "certificate_path": u.CertificatePath, "key_path": u.KeyPath, "reload_command": u.ReloadCommand,
				"update_resources": strconv.FormatBool(u.UpdateResources), "delete_old": strconv.FormatBool(u.DeleteOld), "contact_ids": u.ContactIDs,
			}})
		}
	}
	c.JSON(http.StatusOK, gin.H{"version": providersVersion(p), "providers": views, "delegation_zone": s.cfg.ACMEDNS.Zone})
}

func (s *Server) saveProvider(c *gin.Context, dns bool) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	var in providerInput
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	if err := bindJSON(c, &in); err != nil {
		s.fail(c, err)
		return
	}
	p, err := s.cfg.Remote.Providers(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	if in.Version != providersVersion(p) {
		c.JSON(http.StatusConflict, gin.H{"error": "provider settings changed; refresh before saving"})
		return
	}
	name := c.Param("name")
	if c.Request.Method == http.MethodPost {
		name = in.Name
	}
	if dns && name == certconfig.ManagedDNSProvider {
		c.JSON(http.StatusBadRequest, gin.H{"error": "built-in DNS provider is managed by server settings"})
		return
	}
	_, exists := p.Upstreams[name]
	if dns {
		_, exists = p.DNS[name]
	}
	if c.Request.Method == http.MethodPost && exists {
		c.JSON(http.StatusConflict, gin.H{"error": "provider already exists"})
		return
	}
	if c.Request.Method != http.MethodPost && !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
		return
	}
	switch {
	case c.Request.Method == http.MethodDelete:
		if dns {
			delete(p.DNS, name)
		} else {
			delete(p.Upstreams, name)
		}
	case dns:
		old := p.DNS[name]
		if exists && old.Kind != in.DNS.Kind {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provider kind cannot change"})
			return
		}
		if in.DNS.AccessKey == "" {
			in.DNS.AccessKey = old.AccessKey
		}
		if in.DNS.APIToken == "" {
			in.DNS.APIToken = old.APIToken
		}
		if in.DNS.SecretKey == "" {
			in.DNS.SecretKey = old.SecretKey
		}
		if in.DNS.Kind == "acme-dns" {
			if in.DNS.APIBase != "" || in.DNS.Accounts != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "CNAME validation is managed by CertCenter; registration credentials are not accepted"})
				return
			}
			in.DNS.APIBase, in.DNS.Accounts = old.APIBase, old.Accounts
			if old.APIBase == "" && s.delegatedDNS == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "configure the built-in acme_dns listener first"})
				return
			}
		}
		if p.DNS == nil {
			p.DNS = map[string]certconfig.DNS{}
		}
		p.DNS[name] = in.DNS
	default:
		old := p.Upstreams[name]
		if exists && old.Kind != in.Upstream.Kind {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provider kind cannot change"})
			return
		}
		if in.Upstream.AccessKey == "" {
			in.Upstream.AccessKey = old.AccessKey
		}
		if in.Upstream.SecretKey == "" {
			in.Upstream.SecretKey = old.SecretKey
		}
		if in.Upstream.PrivateKey == "" {
			in.Upstream.PrivateKey = old.PrivateKey
		}
		if p.Upstreams == nil {
			p.Upstreams = map[string]certconfig.Upstream{}
		}
		p.Upstreams[name] = in.Upstream
	}
	if err := p.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	domains, err := s.cfg.Remote.Domains(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	for _, domain := range domains {
		if err := domain.Validate(p); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "provider is referenced by a certificate policy; update the policy first"})
			return
		}
	}
	if err := s.cfg.Remote.Write(c.Request.Context(), "certcenter.providers", p); err != nil {
		s.certificateError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
