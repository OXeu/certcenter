package config

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/configcenter"
	"github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
)

// LoadRemote initializes missing defaults and reads settings through ConfigCenter.
func LoadRemote(ctx context.Context) (Config, error) {
	lookup := os.LookupEnv
	if err := godotenv.Load(lookupOr(lookup, "CC_ENV_FILE", DefaultEnvFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, errors.New("cannot load bootstrap env file")
	}
	baseURL := lookupOr(lookup, "CC_CONFIGCENTER_URL", "")
	apiKey := lookupOr(lookup, "CC_CONFIGCENTER_API_KEY", "")
	cfg := DefaultServer()
	defaults, err := configurationDefaults(cfg)
	if err != nil {
		return Config{}, err
	}
	client, err := configcenter.New(configcenter.Config{BaseURL: baseURL, APIKey: apiKey, Environment: lookupOr(lookup, "CC_CONFIGCENTER_ENVIRONMENT", "production"), LocalPath: lookupOr(lookup, "CC_CONFIGCENTER_LOCAL_PATH", ".tmp/certcenter-config.kv")})
	if err != nil {
		return Config{}, fmt.Errorf("configure settings storage using CC_CONFIGCENTER_URL, CC_CONFIGCENTER_API_KEY and CC_CONFIGCENTER_LOCAL_PATH: %w", err)
	}
	if err := client.EnsureDefaults(ctx, defaults); err != nil {
		return Config{}, fmt.Errorf("initialize settings: %w", err)
	}
	remote := certconfig.NewRemote(client)
	if err := remote.Read(ctx, "certcenter.server", &cfg); err != nil {
		return Config{}, fmt.Errorf("load server settings: %w", err)
	}
	cfg.Remote, cfg.ConfigCenterURL = remote, baseURL
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate key %q: %w", "certcenter.server", err)
	}
	return cfg, nil
}

// Validate checks and normalizes settings before startup or persistence.
func (cfg *Config) Validate() error {
	if err := cfg.ACMEDNS.Validate(); err != nil {
		return fmt.Errorf("validate acme_dns: %w", err)
	}
	if cfg.OAuth.RedirectURL == "" {
		cfg.OAuth.RedirectURL = strings.TrimRight(cfg.PublicURL, "/") + "/oauth/callback"
	}
	if _, port, err := net.SplitHostPort(cfg.Addr); err != nil || port == "" {
		return errors.New("invalid listen address")
	}
	public, err := url.Parse(cfg.PublicURL)
	if err != nil || public.Host == "" || (public.Scheme != "https" && public.Scheme != "http") {
		return errors.New("invalid public URL")
	}
	if cfg.SessionTTL <= 0 {
		return errors.New("session TTL must be positive")
	}
	if cfg.DatabaseDriver == "mysql" {
		dsn, err := mysql.ParseDSN(cfg.DatabaseDSN)
		if err != nil || dsn.DBName == "" {
			return errors.New("invalid MySQL database configuration")
		}
		dsn.ParseTime = true
		cfg.DatabaseDSN = dsn.FormatDSN()
	} else if cfg.DatabaseDriver != "sqlite" || !strings.HasPrefix(cfg.DatabaseDSN, "file:") {
		return errors.New("database must use sqlite or mysql")
	}
	key, err := base64.StdEncoding.DecodeString(cfg.EncryptionKey)
	if err != nil || len(key) != 32 {
		return errors.New("encryption_key must be a base64 encoded 32-byte key")
	}
	return nil
}

func configurationDefaults(cfg Config) (map[string]string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate default encryption key: %w", err)
	}
	cfg.EncryptionKey = base64.StdEncoding.EncodeToString(key)
	server, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode default key certcenter.server: %w", err)
	}
	providers, err := json.Marshal(certconfig.Providers{ACME: certconfig.ACME{DirectoryURL: "https://acme-v02.api.letsencrypt.org/directory", Email: cfg.Bootstrap.AdminEmail}, DNS: map[string]certconfig.DNS{}, Upstreams: map[string]certconfig.Upstream{}})
	if err != nil {
		return nil, fmt.Errorf("encode default key certcenter.providers: %w", err)
	}
	return map[string]string{"certcenter.server": string(server), "certcenter.providers": string(providers)}, nil
}

// DefaultServer returns fresh operational defaults before stored settings are applied.
func DefaultServer() Config {
	cfg := Config{Addr: "127.0.0.1:8081", DatabaseDriver: "sqlite", DatabaseDSN: "file:.tmp/certcenter.db", PublicURL: "http://127.0.0.1:8081", SessionTTL: 24 * time.Hour, Languages: []string{"en", "zh-CN"}, Bootstrap: Bootstrap{AdminEmail: "admin@localhost", AdminName: "admin"}}
	cfg.OAuth = auth.Config{Scopes: []string{"openid", "profile", "email", "groups"}, GroupsClaim: "groups"}
	cfg.Groups = auth.GroupPolicy{AdminGroups: []string{"certcenter-admin"}, Mapping: map[string]string{}}
	return cfg
}
