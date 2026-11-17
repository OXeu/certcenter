package server

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"

	"github.com/OXeu/certcenter/internal/config"
)

func settingsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func (s *Server) validateEditableSettings(cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate server settings: %w", err)
	}
	if !settingsURL(cfg.PublicURL) {
		return errors.New("invalid public URL")
	}
	if len(cfg.Languages) == 0 {
		return errors.New("at least one language is required")
	}
	seen := map[string]bool{}
	for _, language := range cfg.Languages {
		if seen[language] || !s.catalog.Supported(language, cfg.Languages) {
			return errors.New("invalid enabled languages")
		}
		seen[language] = true
	}
	if cfg.OAuth.IssuerURL != "" || cfg.OAuth.ClientID != "" {
		if !cfg.OAuth.Enabled() || !settingsURL(cfg.OAuth.IssuerURL) || !settingsURL(cfg.OAuth.RedirectURL) {
			return errors.New("incomplete OAuth configuration")
		}
	}
	for group, role := range cfg.Groups.Mapping {
		if strings.TrimSpace(group) == "" || (role != "admin" && role != "user") {
			return errors.New("invalid group role mapping")
		}
	}
	for _, group := range cfg.Groups.AdminGroups {
		if strings.TrimSpace(group) == "" {
			return errors.New("invalid administrator group")
		}
	}
	address, err := mail.ParseAddress(cfg.Bootstrap.AdminEmail)
	if err != nil || address.Address != cfg.Bootstrap.AdminEmail || strings.TrimSpace(cfg.Bootstrap.AdminName) == "" {
		return errors.New("invalid bootstrap administrator")
	}
	return nil
}
