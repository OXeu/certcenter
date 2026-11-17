package certificates

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/acme"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	legolog "github.com/go-acme/lego/v4/log"
	"github.com/go-acme/lego/v4/registration"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/dnsprovider"
	"github.com/OXeu/certcenter/internal/store"
)

// Material contains a full certificate chain and its corresponding private key.
type Material struct {
	Certificate []byte `json:"certificate"`
	PrivateKey  []byte `json:"private_key"`
}

// Issuer abstracts certificate authorities for deterministic lifecycle tests.
type Issuer interface {
	Issue(context.Context, certconfig.ACME, certconfig.DNS, []string) (Material, error)
}

// AccountStore persists encrypted ACME identities.
type AccountStore interface {
	Account(context.Context, string) (store.ACMEAccount, error)
	SaveAccount(context.Context, store.ACMEAccount) error
}

// ACMEIssuer uses lego with database-backed account keys and explicit DNS adapters.
type ACMEIssuer struct {
	Store AccountStore
	Vault *Vault
}

type acmeUser struct {
	email        string
	key          crypto.PrivateKey
	registration *registration.Resource
}

// GetEmail supplies the ACME contact.
func (u *acmeUser) GetEmail() string { return u.email }

// GetPrivateKey supplies the persistent ACME account key.
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey { return u.key }

// GetRegistration supplies the current ACME account registration.
func (u *acmeUser) GetRegistration() *registration.Resource { return u.registration }

type roundTrip func(*http.Request) (*http.Response, error)

// RoundTrip attaches operation cancellation to ACME requests.
func (fn roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func init() { legolog.Logger = log.New(io.Discard, "", 0) }

// Issue registers or reuses an ACME account and obtains a bundled DNS-01 certificate.
func (a *ACMEIssuer) Issue(ctx context.Context, cfg certconfig.ACME, dns certconfig.DNS, names []string) (Material, error) {
	if !cfg.AcceptTerms {
		return Material{}, errors.New("ACME terms must be accepted in ConfigCenter")
	}
	key, err := a.accountKey(ctx, cfg)
	if err != nil {
		return Material{}, err
	}
	user := &acmeUser{email: cfg.Email, key: key}
	client, err := lego.NewClient(&lego.Config{CADirURL: cfg.DirectoryURL, User: user, UserAgent: "certcenter", HTTPClient: &http.Client{Timeout: 60 * time.Second, Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(req.Clone(ctx))
		if err != nil {
			return nil, fmt.Errorf("ACME transport: %w", err)
		}
		return response, nil
	})}, Certificate: lego.CertificateConfig{KeyType: certcrypto.RSA2048, Timeout: 120 * time.Second}})
	if err != nil {
		return Material{}, errors.New("ACME discovery failed; check the directory URL")
	}
	provider, err := dnsprovider.New(ctx, dns)
	if err != nil {
		return Material{}, fmt.Errorf("configure DNS: %w", err)
	}
	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		return Material{}, errors.New("cannot configure DNS-01 challenge")
	}
	user.registration, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return Material{}, acmeFailure("registration", err)
	}
	resource, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: names, Bundle: true})
	if err != nil {
		return Material{}, acmeFailure("issuance", err)
	}
	return Material{Certificate: resource.Certificate, PrivateKey: resource.PrivateKey}, nil
}

var acmeErrorCode = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]{0,63}$`)

func acmeFailure(stage string, err error) error {
	var problem *acme.ProblemDetails
	if errors.As(err, &problem) {
		code := strings.TrimPrefix(problem.Type, "urn:ietf:params:acme:error:")
		if acmeErrorCode.MatchString(code) {
			return fmt.Errorf("ACME %s failed: %s (HTTP %d)", stage, code, problem.HTTPStatus)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errors.New("ACME operation cancelled or timed out")
	}
	return fmt.Errorf("ACME %s failed; check DNS propagation, delegation, credentials and CA availability", stage)
}

func (a *ACMEIssuer) accountKey(ctx context.Context, cfg certconfig.ACME) (crypto.PrivateKey, error) {
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(cfg.DirectoryURL+"\x00"+cfg.Email)))
	account, err := a.Store.Account(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		key, genErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if genErr != nil {
			return nil, fmt.Errorf("generate account key: %w", genErr)
		}
		der, marshalErr := x509.MarshalPKCS8PrivateKey(key)
		if marshalErr != nil {
			return nil, fmt.Errorf("encode account key: %w", marshalErr)
		}
		encrypted, sealErr := a.Vault.Seal("account:"+id, der)
		if sealErr != nil {
			return nil, sealErr
		}
		if saveErr := a.Store.SaveAccount(ctx, store.ACMEAccount{ID: id, Material: encrypted}); saveErr != nil {
			return nil, fmt.Errorf("persist account: %w", saveErr)
		}
		account, err = a.Store.Account(ctx, id)
	}
	if err != nil {
		return nil, fmt.Errorf("load account: %w", err)
	}
	der, err := a.Vault.Open("account:"+id, account.Material)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("decode account key: %w", err)
	}
	return key, nil
}

func materialJSON(material Material) ([]byte, error) {
	raw, err := json.Marshal(material)
	if err != nil {
		return nil, fmt.Errorf("encode certificate material: %w", err)
	}
	return raw, nil
}
