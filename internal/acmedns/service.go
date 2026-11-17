// Package acmedns serves CNAME-delegated DNS-01 challenges within CertCenter.
package acmedns

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/dnsprovider"
	"github.com/OXeu/certcenter/internal/store"
)

// Service owns stable delegation names and expiring TXT records.
type Service struct {
	Config certconfig.ACMEDNS
	Store  *store.Store
}

// Record is the CNAME a domain owner must create at their DNS provider.
type Record struct {
	Name     string `json:"name"`
	Target   string `json:"target"`
	Verified *bool  `json:"verified,omitempty"`
}

func delegationID(scope, name string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(scope+"\x00"+name)))
}

func scopedName(scope, domain string) (string, error) {
	name := strings.TrimPrefix(domain, "*.")
	if !certconfig.ValidDomain(scope) || !certconfig.ValidDomain(name) || (name != scope && !strings.HasSuffix(name, "."+scope)) {
		return "", errors.New("DNS challenge is outside the managed domain")
	}
	return name, nil
}

// Ensure allocates a persistent random target without any registration credentials.
func (s *Service) Ensure(ctx context.Context, scope, domain string) (Record, error) {
	name, err := scopedName(scope, domain)
	if err != nil {
		return Record{}, err
	}
	d, err := s.Store.EnsureDNSDelegation(ctx, store.DNSDelegation{ID: delegationID(scope, name), Scope: scope, Name: name, Label: strings.ToLower(rand.Text())})
	if err != nil {
		return Record{}, fmt.Errorf("allocate delegation: %w", err)
	}
	return Record{Name: "_acme-challenge." + name, Target: d.Label + "." + s.Config.Zone}, nil
}

type provider struct {
	present func(string, string, string) error
	cleanup func(string, string, string) error
}

// Present adds one challenge without overwriting concurrent apex or wildcard values.
func (p provider) Present(domain, token, key string) error { return p.present(domain, token, key) }

// CleanUp removes only the TXT value owned by the completed challenge.
func (p provider) CleanUp(domain, token, key string) error { return p.cleanup(domain, token, key) }

// Timeout bounds propagation checks for the authoritative DNS zone.
func (p provider) Timeout() (timeout, interval time.Duration) {
	return 180 * time.Second, 2 * time.Second
}

// Provider publishes challenge values only for names inside the authorized managed domain.
func (s *Service) Provider(ctx context.Context, scope string) dnsprovider.Provider {
	return provider{
		present: func(domain, _, key string) error {
			name, err := scopedName(scope, domain)
			if err != nil {
				return err
			}
			if _, err := s.Ensure(ctx, scope, name); err != nil {
				return err
			}
			value := challengeValue(key)
			if err := s.Store.PutDNSChallenge(ctx, store.DNSChallenge{DelegationID: delegationID(scope, name), Value: value, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}); err != nil {
				return fmt.Errorf("publish delegated challenge: %w", err)
			}
			return nil
		},
		cleanup: func(domain, _, key string) error {
			name, err := scopedName(scope, domain)
			if err != nil {
				return err
			}
			if err := s.Store.DeleteDNSChallenge(ctx, delegationID(scope, name), challengeValue(key)); err != nil {
				return fmt.Errorf("clean delegated challenge: %w", err)
			}
			return nil
		},
	}
}

func challengeValue(key string) string {
	digest := sha256.Sum256([]byte(key))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
