package config

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/configcenter/sdk/configcenter"
	"github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
)

// LoadRemote reads only connection bootstrap variables locally; settings come from the SDK.
func LoadRemote(ctx context.Context) (Config, error) {
	lookup := os.LookupEnv
	if err := godotenv.Load(lookupOr(lookup, "CC_ENV_FILE", DefaultEnvFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, errors.New("cannot load bootstrap env file")
	}
	baseURL := lookupOr(lookup, "CC_CONFIGCENTER_URL", "")
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return Config{}, errors.New("ConfigCenter URL must be an HTTP(S) URL without credentials or query parameters")
	}
	client, err := configcenter.New(configcenter.Config{BaseURL: baseURL, APIKey: lookupOr(lookup, "CC_CONFIGCENTER_API_KEY", ""), Environment: lookupOr(lookup, "CC_CONFIGCENTER_ENVIRONMENT", "production")})
	if err != nil {
		return Config{}, errors.New("invalid ConfigCenter bootstrap connection")
	}
	remote := certconfig.NewRemote(client)
	cfg := Config{Addr: "127.0.0.1:8081", DatabaseDriver: "sqlite", DatabaseDSN: "file:.tmp/certcenter.db", PublicURL: "http://127.0.0.1:8081", SessionTTL: 24 * time.Hour, Languages: []string{"en", "zh-CN"}, Bootstrap: Bootstrap{AdminEmail: "admin@localhost", AdminName: "admin"}}
	if err := remote.Read(ctx, "certcenter.server", &cfg); err != nil {
		return Config{}, fmt.Errorf("load server settings: %w", err)
	}
	cfg.Remote, cfg.ConfigCenterURL = remote, baseURL
	if cfg.OAuth.RedirectURL == "" {
		cfg.OAuth.RedirectURL = strings.TrimRight(cfg.PublicURL, "/") + "/oauth/callback"
	}
	if _, port, err := net.SplitHostPort(cfg.Addr); err != nil || port == "" {
		return Config{}, errors.New("invalid listen address")
	}
	public, err := url.Parse(cfg.PublicURL)
	if err != nil || public.Host == "" || (public.Scheme != "https" && public.Scheme != "http") {
		return Config{}, errors.New("invalid public URL")
	}
	if cfg.SessionTTL <= 0 {
		return Config{}, errors.New("session TTL must be positive")
	}
	if cfg.DatabaseDriver == "mysql" {
		dsn, err := mysql.ParseDSN(cfg.DatabaseDSN)
		if err != nil || dsn.DBName == "" {
			return Config{}, errors.New("invalid MySQL database configuration")
		}
		dsn.ParseTime = true
		cfg.DatabaseDSN = dsn.FormatDSN()
	} else if cfg.DatabaseDriver != "sqlite" || !strings.HasPrefix(cfg.DatabaseDSN, "file:") {
		return Config{}, errors.New("database must use sqlite or mysql")
	}
	key, err := base64.StdEncoding.DecodeString(cfg.EncryptionKey)
	if err != nil || len(key) != 32 {
		return Config{}, errors.New("encryption_key must be a base64 encoded 32-byte key")
	}
	return cfg, nil
}
