package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/config"
	"github.com/gin-gonic/gin"
)

type settingsDocument map[string]json.RawMessage

type generalSettings struct {
	Addr       string   `json:"addr"`
	PublicURL  string   `json:"public_url"`
	SessionTTL string   `json:"session_ttl"`
	Languages  []string `json:"languages"`
}

type accessSettings struct {
	AllowRegistration bool              `json:"allow_registration"`
	AdminGroups       []string          `json:"admin_groups"`
	Mapping           map[string]string `json:"mapping"`
	Bootstrap         config.Bootstrap  `json:"bootstrap"`
}

type databaseSettings struct {
	Driver string `json:"driver"`
	DSN    string `json:"dsn,omitempty"`
}

type oauthSettings struct {
	IssuerURL        string   `json:"issuer_url"`
	ClientID         string   `json:"client_id"`
	Scopes           []string `json:"scopes"`
	RedirectURL      string   `json:"redirect_url"`
	GroupsClaim      string   `json:"groups_claim"`
	SecretConfigured bool     `json:"secret_configured"`
}

type settingsSnapshot struct {
	server    settingsDocument
	providers settingsDocument
	cfg       config.Config
	registry  certconfig.Providers
	acme      certconfig.ACME
}

func (s *Server) settingsSnapshot(ctx context.Context) (settingsSnapshot, error) {
	var snapshot settingsSnapshot
	if err := s.cfg.Remote.Read(ctx, "certcenter.server", &snapshot.server); err != nil {
		return snapshot, fmt.Errorf("read server settings: %w", err)
	}
	if err := s.cfg.Remote.Read(ctx, "certcenter.providers", &snapshot.providers); err != nil {
		return snapshot, fmt.Errorf("read provider settings: %w", err)
	}
	if snapshot.server == nil || snapshot.providers == nil {
		return snapshot, errors.New("settings must be JSON objects")
	}
	snapshot.cfg = config.DefaultServer()
	raw, err := json.Marshal(snapshot.server)
	if err != nil {
		return snapshot, fmt.Errorf("encode stored server settings: %w", err)
	}
	if err := json.Unmarshal(raw, &snapshot.cfg); err != nil {
		return snapshot, errors.New("invalid stored server settings")
	}
	rawProviders, err := json.Marshal(snapshot.providers)
	if err != nil {
		return snapshot, fmt.Errorf("encode stored provider settings: %w", err)
	}
	if err := json.Unmarshal(rawProviders, &snapshot.registry); err != nil {
		return snapshot, errors.New("invalid stored provider settings")
	}
	snapshot.acme = snapshot.registry.ACME
	return snapshot, nil
}

func settingsVersion(document settingsDocument) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v", document))))
}

func generalFrom(cfg config.Config) generalSettings {
	return generalSettings{Addr: cfg.Addr, PublicURL: cfg.PublicURL, SessionTTL: cfg.SessionTTL.String(), Languages: cfg.Languages}
}

func accessFrom(cfg config.Config) accessSettings {
	return accessSettings{AllowRegistration: cfg.AllowRegistration, AdminGroups: cfg.Groups.AdminGroups, Mapping: cfg.Groups.Mapping, Bootstrap: cfg.Bootstrap}
}

func requiresRestart(current, stored config.Config) bool {
	current.AllowRegistration, stored.AllowRegistration = false, false
	current.Remote, stored.Remote = nil, nil
	current.ConfigCenterURL, stored.ConfigCenterURL = "", ""
	if err := stored.Validate(); err != nil {
		return true
	}
	for _, cfg := range []*config.Config{&current, &stored} {
		if len(cfg.Groups.Mapping) == 0 {
			cfg.Groups.Mapping = nil
		}
		if len(cfg.Groups.AdminGroups) == 0 {
			cfg.Groups.AdminGroups = nil
		}
		if len(cfg.OAuth.Scopes) == 0 {
			cfg.OAuth.Scopes = nil
		}
		if len(cfg.ACMEDNS.Addresses) == 0 {
			cfg.ACMEDNS.Addresses = nil
		}
	}
	return fmt.Sprintf("%#v", current) != fmt.Sprintf("%#v", stored)
}

func (s *Server) settingsJSON(snapshot settingsSnapshot) gin.H {
	cfg := snapshot.cfg
	dns, upstreams := providerNames(snapshot.registry)
	return gin.H{
		"local": s.cfg.Remote.IsLocal(), "configcenter_url": s.cfg.ConfigCenterURL, "dns_providers": dns, "upstreams": upstreams,
		"directory_url": snapshot.acme.DirectoryURL, "email": snapshot.acme.Email, "accept_terms": snapshot.acme.AcceptTerms, "delegation_zone": s.cfg.ACMEDNS.Zone,
		"server_version": settingsVersion(snapshot.server), "providers_version": settingsVersion(snapshot.providers),
		"restart_required": requiresRestart(s.cfg, cfg),
		"general":          generalFrom(cfg), "access": accessFrom(cfg), "acme": snapshot.acme, "dns": cfg.ACMEDNS,
		"database":              gin.H{"driver": cfg.DatabaseDriver, "configured": cfg.DatabaseDSN != ""},
		"oauth":                 oauthSettings{IssuerURL: cfg.OAuth.IssuerURL, ClientID: cfg.OAuth.ClientID, Scopes: cfg.OAuth.Scopes, RedirectURL: cfg.OAuth.RedirectURL, GroupsClaim: cfg.OAuth.GroupsClaim, SecretConfigured: cfg.OAuth.ClientSecret != ""},
		"encryption_configured": cfg.EncryptionKey != "",
	}
}

func (s *Server) certificateSettings(c *gin.Context) {
	snapshot, err := s.settingsSnapshot(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	c.JSON(http.StatusOK, s.settingsJSON(snapshot))
}

func decodeSettingsValues(raw json.RawMessage, out any) error {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return errors.New("settings values must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return errors.New("invalid settings fields")
	}
	return nil
}

func (d settingsDocument) merge(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode settings field: %w", err)
	}
	if bytes.HasPrefix(raw, []byte("{")) {
		nested := settingsDocument{}
		if old := d[key]; len(old) > 0 && string(old) != "null" {
			if err := json.Unmarshal(old, &nested); err != nil {
				return errors.New("invalid stored settings object")
			}
		}
		var fields settingsDocument
		if err := json.Unmarshal(raw, &fields); err != nil {
			return fmt.Errorf("decode settings object: %w", err)
		}
		for name, data := range fields {
			nested[name] = data
		}
		raw, err = json.Marshal(nested)
		if err != nil {
			return fmt.Errorf("encode merged settings object: %w", err)
		}
	}
	d[key] = raw
	return nil
}

func (s *Server) saveCertificateSettings(c *gin.Context) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	var input struct {
		Version string          `json:"version"`
		Values  json.RawMessage `json:"values"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := bindJSON(c, &input); err != nil {
		s.fail(c, err)
		return
	}
	section := c.Param("section")
	switch section {
	case "general", "access", "oauth", "dns", "database", "acme":
	default:
		c.Status(http.StatusNotFound)
		return
	}
	snapshot, err := s.settingsSnapshot(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	document, key := snapshot.server, "certcenter.server"
	if section == "acme" {
		document, key = snapshot.providers, "certcenter.providers"
	}
	if input.Version != settingsVersion(document) {
		c.JSON(http.StatusConflict, gin.H{"error": "settings changed; refresh before saving"})
		return
	}
	if err := s.applySettings(&snapshot, section, input.Values); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.cfg.Remote.Write(c.Request.Context(), key, document); err != nil {
		s.certificateError(c, err)
		return
	}
	c.JSON(http.StatusOK, s.settingsJSON(snapshot))
}

func (s *Server) applySettings(snapshot *settingsSnapshot, section string, raw json.RawMessage) error {
	cfg := &snapshot.cfg
	values := map[string]any{}
	switch section {
	case "general":
		in := generalFrom(*cfg)
		if err := decodeSettingsValues(raw, &in); err != nil {
			return err
		}
		ttl, err := time.ParseDuration(in.SessionTTL)
		if err != nil {
			return errors.New("invalid session duration")
		}
		cfg.Addr, cfg.PublicURL, cfg.SessionTTL, cfg.Languages = in.Addr, in.PublicURL, ttl, in.Languages
		values = map[string]any{"addr": in.Addr, "public_url": in.PublicURL, "session_ttl": ttl, "languages": in.Languages}
	case "access":
		in := accessFrom(*cfg)
		var supplied settingsDocument
		if err := json.Unmarshal(raw, &supplied); err != nil {
			return errors.New("invalid access settings")
		}
		if _, present := supplied["mapping"]; present {
			in.Mapping = nil
		}
		if err := decodeSettingsValues(raw, &in); err != nil {
			return err
		}
		cfg.AllowRegistration, cfg.Groups, cfg.Bootstrap = in.AllowRegistration, auth.GroupPolicy{AdminGroups: in.AdminGroups, Mapping: in.Mapping}, in.Bootstrap
		values = map[string]any{"allow_registration": in.AllowRegistration, "groups": cfg.Groups, "bootstrap": cfg.Bootstrap}
	case "oauth":
		in := cfg.OAuth
		if err := decodeSettingsValues(raw, &in); err != nil {
			return err
		}
		if in.ClientSecret == "" {
			in.ClientSecret = cfg.OAuth.ClientSecret
		}
		cfg.OAuth = in
		values["oauth"] = in
	case "dns":
		in := cfg.ACMEDNS
		if err := decodeSettingsValues(raw, &in); err != nil {
			return err
		}
		cfg.ACMEDNS = in
		values["acme_dns"] = in
	case "database":
		in := databaseSettings{Driver: cfg.DatabaseDriver}
		if err := decodeSettingsValues(raw, &in); err != nil {
			return err
		}
		if in.DSN != "" {
			cfg.DatabaseDSN = in.DSN
		}
		cfg.DatabaseDriver = in.Driver
		values = map[string]any{"database_driver": cfg.DatabaseDriver, "database_dsn": cfg.DatabaseDSN}
	case "acme":
		in := snapshot.acme
		if err := decodeSettingsValues(raw, &in); err != nil {
			return err
		}
		if err := (certconfig.Providers{ACME: in}).Validate(); err != nil {
			return fmt.Errorf("validate ACME settings: %w", err)
		}
		snapshot.acme = in
		return snapshot.providers.merge("acme", in)
	}
	if err := s.validateEditableSettings(*cfg); err != nil {
		return err
	}
	for name, value := range values {
		if err := snapshot.server.merge(name, value); err != nil {
			return err
		}
	}
	return nil
}
