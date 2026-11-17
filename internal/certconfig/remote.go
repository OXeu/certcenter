package certconfig

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/OXeu/configcenter/sdk/configcenter"
)

// ErrMissing indicates a missing remote configuration record.
var ErrMissing = errors.New("configuration not found")

// DomainKey respects ConfigCenter's 191-character key limit for long DNS names.
func DomainKey(name string) string {
	key := "certcenter.domain." + name
	if len(key) > 191 {
		return fmt.Sprintf("certcenter.domain.sha256-%x", sha256.Sum256([]byte(name)))
	}
	return key
}

// Remote owns SDK access and serializes snapshot reads and writes.
type Remote struct {
	mu     sync.Mutex
	client *configcenter.Client
}

// RegistrationAllowed reads the live registration policy from server settings.
func (r *Remote) RegistrationAllowed(ctx context.Context) (bool, error) {
	var settings struct {
		Allowed bool `json:"allow_registration"`
	}
	if err := r.Read(ctx, "certcenter.server", &settings); err != nil {
		return false, err
	}
	return settings.Allowed, nil
}

// NewRemote connects a ConfigCenter SDK client.
func NewRemote(client *configcenter.Client) *Remote { return &Remote{client: client} }

// IsLocal reports whether settings are persisted in the SDK's local fallback.
func (r *Remote) IsLocal() bool { return r.client.IsLocal() }

// Read fetches a JSON setting directly from ConfigCenter.
func (r *Remote) Read(ctx context.Context, key string, out any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.client.Load(ctx); err != nil {
		return fmt.Errorf("load setting key %q: %w", key, err)
	}
	return r.decode(key, out)
}

func (r *Remote) decode(key string, out any) error {
	raw, ok := r.client.Get(key)
	if !ok {
		return fmt.Errorf("key %q: %w", key, ErrMissing)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("invalid JSON in %s", key)
	}
	return nil
}

// Providers reads and validates the provider registry.
func (r *Remote) Providers(ctx context.Context) (Providers, error) {
	var p Providers
	if err := r.Read(ctx, "certcenter.providers", &p); err != nil {
		return p, err
	}
	if err := p.Validate(); err != nil {
		return p, fmt.Errorf("validate key %q: %w", "certcenter.providers", err)
	}
	return p, nil
}

// Domains returns all validated domain policies from one consistent SDK snapshot.
func (r *Remote) Domains(ctx context.Context) ([]Domain, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.client.Load(ctx); err != nil {
		return nil, fmt.Errorf("load domains: %w", err)
	}
	var providers Providers
	if err := r.decode("certcenter.providers", &providers); err != nil {
		return nil, err
	}
	if err := providers.Validate(); err != nil {
		return nil, fmt.Errorf("validate key %q: %w", "certcenter.providers", err)
	}
	domains := []Domain{}
	for _, item := range r.client.All() {
		if !strings.HasPrefix(item.Key, "certcenter.domain.") {
			continue
		}
		var domain Domain
		if err := json.Unmarshal([]byte(item.Value), &domain); err != nil {
			return nil, fmt.Errorf("invalid JSON in key %q", item.Key)
		}
		if item.Key != DomainKey(domain.Name) {
			return nil, fmt.Errorf("key %q: domain name differs from key", item.Key)
		}
		if err := domain.Validate(providers); err != nil {
			return nil, fmt.Errorf("validate key %q: %w", item.Key, err)
		}
		domains = append(domains, domain)
	}
	return domains, nil
}

// Domain loads one domain by normalized name.
func (r *Remote) Domain(ctx context.Context, name string) (Domain, error) {
	var domain Domain
	if !ValidDomain(name) {
		return domain, fmt.Errorf("domain key %q: %w", DomainKey(name), ErrMissing)
	}
	if err := r.Read(ctx, DomainKey(name), &domain); err != nil {
		return domain, err
	}
	if domain.Name != name {
		return domain, fmt.Errorf("key %q: domain name differs from key", DomainKey(name))
	}
	return domain, nil
}

// SaveDomain validates and persists a domain policy through the SDK.
func (r *Remote) SaveDomain(ctx context.Context, domain Domain) error {
	p, err := r.Providers(ctx)
	if err != nil {
		return err
	}
	if err := domain.Validate(p); err != nil {
		return err
	}
	return r.Write(ctx, DomainKey(domain.Name), domain)
}

// Write stores a JSON setting through the ConfigCenter SDK.
func (r *Remote) Write(ctx context.Context, key string, value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode setting key %q: %w", key, err)
	}
	if err := r.client.Put(ctx, key, string(raw)); err != nil {
		return fmt.Errorf("save setting key %q: %w", key, err)
	}
	return nil
}
