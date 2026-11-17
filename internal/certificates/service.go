package certificates

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/OXeu/certcenter/internal/upstream"
)

// Repository abstracts certificate persistence independently of the database driver.
type Repository interface {
	CertificateForJob(context.Context, uint) (store.Certificate, error)
	SaveCertificate(context.Context, store.Certificate) (store.Certificate, error)
	LatestCertificate(context.Context, string, string) (store.Certificate, error)
	EnqueueCertificate(context.Context, store.CertificateJob) (store.CertificateJob, error)
	ClaimCertificateJob(context.Context, string, time.Time) (store.CertificateJob, error)
	FinishCertificateJob(context.Context, store.CertificateJob, string) error
	LastCertificateJob(context.Context, string, string) (store.CertificateJob, error)
	AddCertificateEvent(context.Context, store.CertificateEvent) error
	Deployments(context.Context, uint) ([]store.Deployment, error)
	SaveDeployment(context.Context, store.Deployment) error
	PreviousDeployments(context.Context, store.Certificate, string, string) ([]store.DeployedCertificate, error)
	CloudUpdates(context.Context, uint) ([]store.CloudUpdate, error)
	SaveCloudUpdate(context.Context, store.CloudUpdate) error
	DeferCertificateJob(context.Context, store.CertificateJob, time.Time) error
}

// Settings is the live configuration interface implemented by the ConfigCenter SDK adapter.
type Settings interface {
	Domain(context.Context, string) (certconfig.Domain, error)
	Domains(context.Context) ([]certconfig.Domain, error)
	Providers(context.Context) (certconfig.Providers, error)
}

// Service coordinates immutable certificate versions and durable operation jobs.
type Service struct {
	Store    Repository
	Settings Settings
	Issuer   Issuer
	Upstream upstream.Provider
	Vault    *Vault
	Log      *slog.Logger
}

// ErrPolicyNotFound indicates that the requested policy no longer exists.
var ErrPolicyNotFound = errors.New("certificate policy not found; refresh the domain and choose an existing policy")

// ErrPolicyDisabled indicates that issuance and deployment are disabled for a policy.
var ErrPolicyDisabled = errors.New("certificate policy is disabled; enable it before issuing or deploying")

// ErrNoUpstreams indicates that a deployment has no destination.
var ErrNoUpstreams = errors.New("no deployment upstreams configured; add a target before deploying")

// Queue validates an operation against current remote policies before creating a job.
func (s *Service) Queue(ctx context.Context, domain, policyID, action string) (store.CertificateJob, error) {
	return s.queue(ctx, domain, policyID, action, false)
}

func (s *Service) queue(ctx context.Context, domain, policyID, action string, automatic bool) (store.CertificateJob, error) {
	if action != "issue" && action != "deploy" {
		return store.CertificateJob{}, errors.New("unsupported certificate action")
	}
	d, err := s.Settings.Domain(ctx, domain)
	if err != nil {
		return store.CertificateJob{}, fmt.Errorf("read domain policy: %w", err)
	}
	p, err := findPolicy(d, policyID)
	if err != nil {
		return store.CertificateJob{}, err
	}
	if !p.Enabled {
		return store.CertificateJob{}, ErrPolicyDisabled
	}
	if action == "deploy" && len(p.Upstreams) == 0 {
		return store.CertificateJob{}, ErrNoUpstreams
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(domain+"\x00"+policyID)))
	job, err := s.Store.EnqueueCertificate(ctx, store.CertificateJob{Domain: domain, PolicyID: policyID, Action: action, ActiveKey: &key, Automatic: automatic})
	if err != nil {
		return job, fmt.Errorf("enqueue operation: %w", err)
	}
	return job, nil
}

func findPolicy(domain certconfig.Domain, id string) (certconfig.Policy, error) {
	for _, p := range domain.Certificates {
		if p.ID == id {
			return p, nil
		}
	}
	return certconfig.Policy{}, ErrPolicyNotFound
}

func (s *Service) event(ctx context.Context, job store.CertificateJob, stage, target, message string) error {
	if err := s.Store.AddCertificateEvent(ctx, store.CertificateEvent{JobID: job.ID, Domain: job.Domain, PolicyID: job.PolicyID, Stage: stage, Target: target, Message: message}); err != nil {
		return fmt.Errorf("record operation: %w", err)
	}
	return nil
}

// Process executes a claimed job and leaves previous valid certificates intact on failure.
func (s *Service) Process(ctx context.Context, job store.CertificateJob) error {
	domain, err := s.Settings.Domain(ctx, job.Domain)
	if err != nil {
		return fmt.Errorf("load domain settings: %w", err)
	}
	providers, err := s.Settings.Providers(ctx)
	if err != nil {
		return fmt.Errorf("load providers: %w", err)
	}
	if err := domain.Validate(providers); err != nil {
		return fmt.Errorf("validate policy: %w", err)
	}
	policy, err := findPolicy(domain, job.PolicyID)
	if err != nil {
		return err
	}
	if !policy.Enabled {
		return errors.New("certificate policy is disabled")
	}
	var cert store.Certificate
	switch job.Action {
	case "issue":
		cert, err = s.Store.CertificateForJob(ctx, job.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("recover issued version: %w", err)
		}
		if err == nil {
			if !policy.AutoDeploy {
				return nil
			}
			return s.deploy(ctx, job, policy, providers, cert)
		}
		if err := s.event(ctx, job, "issuing", policy.DNSProvider, "Preparing ACME account and DNS-01 validation"); err != nil {
			return err
		}
		dns := providers.DNS[policy.DNSProvider]
		dns.Scope = domain.Name
		material, err := s.Issuer.Issue(ctx, providers.ACME, dns, policy.Names)
		if err != nil {
			return fmt.Errorf("issue certificate: %w", err)
		}
		cert, err = s.save(ctx, job, policy, material)
		if err != nil {
			return err
		}
		if err := s.event(ctx, job, "issued", "", "Certificate saved; expires "+cert.NotAfter.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
		if !policy.AutoDeploy {
			return nil
		}
	case "deploy":
		cert, err = s.Store.LatestCertificate(ctx, job.Domain, job.PolicyID)
		if err != nil {
			return fmt.Errorf("load issued certificate: %w", err)
		}
	default:
		return errors.New("unsupported queued operation")
	}
	return s.deploy(ctx, job, policy, providers, cert)
}

func (s *Service) save(ctx context.Context, job store.CertificateJob, policy certconfig.Policy, material Material) (store.Certificate, error) {
	pair, err := tls.X509KeyPair(material.Certificate, material.PrivateKey)
	if err != nil || len(pair.Certificate) == 0 {
		return store.Certificate{}, errors.New("issuer returned an invalid certificate/key pair")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return store.Certificate{}, fmt.Errorf("parse issued certificate: %w", err)
	}
	for _, name := range policy.Names {
		if strings.HasPrefix(name, "*.") {
			if !slices.Contains(leaf.DNSNames, name) {
				return store.Certificate{}, errors.New("issued certificate is missing a wildcard name")
			}
		} else if err := leaf.VerifyHostname(name); err != nil {
			return store.Certificate{}, errors.New("issued certificate is missing a requested name")
		}
	}
	if !leaf.NotAfter.After(time.Now()) || leaf.NotBefore.After(time.Now().Add(5*time.Minute)) {
		return store.Certificate{}, errors.New("issued certificate has invalid validity dates")
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(leaf.Raw))
	plain, err := materialJSON(material)
	if err != nil {
		return store.Certificate{}, err
	}
	sealed, err := s.Vault.Seal(job.Domain+":"+job.PolicyID+":"+fingerprint, plain)
	if err != nil {
		return store.Certificate{}, err
	}
	cert, err := s.Store.SaveCertificate(ctx, store.Certificate{JobID: &job.ID, Domain: job.Domain, PolicyID: job.PolicyID, Names: leaf.DNSNames, Fingerprint: fingerprint, Material: sealed, NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter})
	if err != nil {
		return cert, fmt.Errorf("persist issued certificate: %w", err)
	}
	return cert, nil
}

func matchesPolicy(cert store.Certificate, policy certconfig.Policy) bool {
	for _, name := range policy.Names {
		if !slices.Contains(cert.Names, name) {
			return false
		}
	}
	return true
}
