// Package dnsprovider adapts DNS services to lego's DNS-01 challenge interface.
package dnsprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/tencentcloud"
)

// Provider is the shared DNS challenge abstraction.
type Provider interface {
	challenge.Provider
	Timeout() (time.Duration, time.Duration)
}

type callbacks struct {
	present func(string, string, string) error
	cleanup func(string, string, string) error
	timeout func() (time.Duration, time.Duration)
}

// Present publishes a DNS-01 challenge.
func (p callbacks) Present(domain, token, key string) error { return p.present(domain, token, key) }

// CleanUp removes an owned DNS-01 challenge when supported.
func (p callbacks) CleanUp(domain, token, key string) error { return p.cleanup(domain, token, key) }

// Timeout bounds DNS propagation polling by the operation deadline.
func (p callbacks) Timeout() (timeout, interval time.Duration) { return p.timeout() }

// New constructs a provider without using process-wide environment variables.
func New(ctx context.Context, cfg certconfig.DNS) (Provider, error) {
	var provider challenge.Provider
	var err error
	switch cfg.Kind {
	case "alidns":
		provider, err = alidns.NewDNSProviderConfig(&alidns.Config{APIKey: cfg.AccessKey, SecretKey: cfg.SecretKey, RegionID: cfg.Region, TTL: 600, PropagationTimeout: 180 * time.Second, PollingInterval: 2 * time.Second, HTTPTimeout: 30 * time.Second})
	case "tencentcloud":
		provider, err = tencentcloud.NewDNSProviderConfig(&tencentcloud.Config{SecretID: cfg.AccessKey, SecretKey: cfg.SecretKey, Region: cfg.Region, TTL: 600, PropagationTimeout: 180 * time.Second, PollingInterval: 2 * time.Second, HTTPTimeout: 30 * time.Second})
	case "acme-dns":
		provider = callbacks{present: func(domain, _, key string) error { return presentDelegated(ctx, cfg, domain, key) }, cleanup: func(_, _, _ string) error { return nil }}
	default:
		return nil, errors.New("unsupported DNS provider")
	}
	if err != nil {
		return nil, errors.New("cannot initialize DNS provider; check credentials")
	}
	return callbacks{
		present: func(domain, token, key string) error {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("DNS challenge cancelled: %w", err)
			}
			if err := provider.Present(domain, token, key); err != nil {
				return errors.New("DNS challenge update failed; check provider access and CNAME delegation")
			}
			return nil
		},
		cleanup: func(domain, token, key string) error {
			if err := provider.CleanUp(domain, token, key); err != nil {
				return errors.New("DNS challenge cleanup failed")
			}
			return nil
		},
		timeout: func() (time.Duration, time.Duration) {
			duration := 180 * time.Second
			if deadline, ok := ctx.Deadline(); ok {
				duration = max(time.Millisecond, min(duration, time.Until(deadline)))
			}
			return duration, 2 * time.Second
		},
	}, nil
}

func presentDelegated(ctx context.Context, cfg certconfig.DNS, domain, key string) error {
	account, ok := cfg.Accounts[strings.TrimPrefix(domain, "*.")]
	if !ok {
		return errors.New("missing acme-dns registration")
	}
	info := dns01.GetChallengeInfo(domain, key)
	body, err := json.Marshal(map[string]string{"subdomain": account.Subdomain, "txt": info.Value})
	if err != nil {
		return fmt.Errorf("encode acme-dns update: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.APIBase, "/")+"/update", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create acme-dns request: %w", err)
	}
	req.Header.Set("X-Api-User", account.Username)
	req.Header.Set("X-Api-Key", account.Password)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("acme-dns connection failed")
	}
	if err := resp.Body.Close(); err != nil {
		return fmt.Errorf("close acme-dns response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("acme-dns update returned status %d", resp.StatusCode)
	}
	return nil
}
