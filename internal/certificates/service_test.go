package certificates

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/OXeu/certcenter/internal/upstream"
	"github.com/glebarez/sqlite"
)

type fakeSettings struct {
	domain    certconfig.Domain
	providers certconfig.Providers
}

func (f *fakeSettings) Domain(context.Context, string) (certconfig.Domain, error) {
	return f.domain, nil
}

func (f *fakeSettings) Domains(context.Context) ([]certconfig.Domain, error) {
	return []certconfig.Domain{f.domain}, nil
}

func (f *fakeSettings) Providers(context.Context) (certconfig.Providers, error) {
	return f.providers, nil
}

type fakeIssuer struct {
	material Material
	err      error
	calls    int
}

func (f *fakeIssuer) Issue(context.Context, certconfig.ACME, certconfig.DNS, []string) (Material, error) {
	f.calls++
	return f.material, f.err
}

type fakeUpstream struct {
	calls map[string]int
	fail  bool
}

func (f *fakeUpstream) Upload(_ context.Context, cfg certconfig.Upstream, material upstream.Bundle) (string, error) {
	f.calls[cfg.Kind]++
	if len(material.PrivateKey) == 0 || len(material.Certificate) == 0 {
		return "", errors.New("empty certificate material")
	}
	if cfg.Kind == "aliyun" && f.fail {
		return "", errors.New("temporary upstream failure")
	}
	return "receipt-" + cfg.Kind, nil
}

func testMaterial(t *testing.T, names []string, notBefore, notAfter time.Time) Material {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: names, NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return Material{Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})}
}

func testService(t *testing.T) (*Service, *fakeSettings, *fakeIssuer, *fakeUpstream, *store.Store) {
	t.Helper()
	st, err := store.Open(t.Context(), sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), "admin@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	vault, err := NewVault(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	settings := &fakeSettings{domain: certconfig.Domain{Name: "example.com", Owner: "admin@example.com", Certificates: []certconfig.Policy{{ID: "wildcard", Names: []string{"example.com", "*.example.com"}, DNSProvider: "dns", Enabled: true, AutoRenew: true, RenewBeforeDays: 30, AutoDeploy: true, Upstreams: []string{"tencent", "ali"}}}}, providers: certconfig.Providers{DNS: map[string]certconfig.DNS{"dns": {Kind: "alidns"}}, Upstreams: map[string]certconfig.Upstream{"tencent": {Kind: "tencentcloud"}, "ali": {Kind: "aliyun"}}}}
	issuer := &fakeIssuer{material: testMaterial(t, []string{"example.com", "*.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))}
	upload := &fakeUpstream{calls: map[string]int{}}
	return &Service{Store: st, Settings: settings, Issuer: issuer, Upstream: upload, Vault: vault, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, settings, issuer, upload, st
}

func TestLifecycleRetainsCertificateAndRetriesOnlyFailedUploads(t *testing.T) {
	s, _, issuer, upload, st := testService(t)
	upload.fail = true
	if _, err := s.Queue(t.Context(), "example.com", "wildcard", "issue"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Queue(t.Context(), "example.com", "wildcard", "issue"); !errors.Is(err, store.ErrBusy) {
		t.Fatalf("duplicate queue: %v", err)
	}
	job, err := st.ClaimCertificateJob(t.Context(), "worker", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.execute(t.Context(), job)
	cert, err := st.LatestCertificate(t.Context(), "example.com", "wildcard")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cert.Material), "PRIVATE KEY") {
		t.Fatal("plaintext key persisted")
	}
	if issuer.calls != 1 || upload.calls["tencentcloud"] != 1 || upload.calls["aliyun"] != 1 {
		t.Fatal("unexpected issuance/deployment count")
	}
	upload.fail = false
	if err := s.Schedule(t.Context(), time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	job, err = st.ClaimCertificateJob(t.Context(), "worker", time.Now())
	if err != nil || job.Action != "deploy" {
		t.Fatalf("retry should deploy existing certificate: %+v %v", job, err)
	}
	s.execute(t.Context(), job)
	if issuer.calls != 1 || upload.calls["tencentcloud"] != 1 || upload.calls["aliyun"] != 2 {
		t.Fatal("successful upload was repeated or certificate reissued")
	}
	if err := s.Schedule(t.Context(), time.Now().Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimCertificateJob(t.Context(), "worker", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unexpected duplicate automatic deployment", err)
	}
	events, err := st.CertificateEvents(t.Context(), "example.com")
	if err != nil || len(events) < 7 {
		t.Fatal("missing audit trail", err, len(events))
	}
}

func TestRenewalBackoffAndPreservation(t *testing.T) {
	s, settings, issuer, _, st := testService(t)
	settings.domain.Certificates[0].AutoDeploy = false
	issuer.material = testMaterial(t, []string{"example.com", "*.example.com"}, time.Now().Add(-80*24*time.Hour), time.Now().Add(10*24*time.Hour))
	if _, err := s.Queue(t.Context(), "example.com", "wildcard", "issue"); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimCertificateJob(t.Context(), "worker", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.execute(t.Context(), job)
	issuer.err = errors.New("CA unavailable")
	if err := s.Schedule(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err = st.ClaimCertificateJob(t.Context(), "worker", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.execute(t.Context(), job)
	if err := s.Schedule(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimCertificateJob(t.Context(), "worker", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failure backoff missing", err)
	}
	versions, err := st.Certificates(t.Context(), "example.com")
	if err != nil || len(versions) != 1 {
		t.Fatal("valid version was lost", err)
	}
	settings.domain.Certificates[0].Enabled = false
	if err := s.Schedule(t.Context(), time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimCertificateJob(t.Context(), "worker", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("disabled policy queued", err)
	}
}

func TestRejectWrongCertificateAndVaultTampering(t *testing.T) {
	s, _, issuer, _, st := testService(t)
	issuer.material = testMaterial(t, []string{"other.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	job := store.CertificateJob{Domain: "example.com", PolicyID: "wildcard", Action: "issue"}
	if err := s.Process(t.Context(), job); err == nil {
		t.Fatal("wrong SAN accepted")
	}
	if _, err := st.LatestCertificate(t.Context(), "example.com", "wildcard"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("invalid certificate stored")
	}
	sealed, err := s.Vault.Seal("one", []byte("private key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Vault.Open("two", sealed); err == nil {
		t.Fatal("identity substitution accepted")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := s.Vault.Open("one", sealed); err == nil {
		t.Fatal("tampering accepted")
	}
}

func TestLeaseRecoveryAndFencing(t *testing.T) {
	s, _, _, _, st := testService(t)
	if _, err := s.Queue(t.Context(), "example.com", "wildcard", "issue"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first, err := st.ClaimCertificateJob(t.Context(), "old", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimCertificateJob(t.Context(), "new", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("active lease stolen")
	}
	second, err := st.ClaimCertificateJob(t.Context(), "new", now.Add(21*time.Minute))
	if err != nil || second.ID != first.ID {
		t.Fatal("expired lease not recovered", err)
	}
	if err := st.FinishCertificateJob(t.Context(), first, "succeeded"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("stale worker completed job", err)
	}
	if err := st.FinishCertificateJob(t.Context(), second, "succeeded"); err != nil {
		t.Fatal(err)
	}
}

func TestRestartRecoversSavedCertificateWithoutReissuing(t *testing.T) {
	s, _, issuer, _, st := testService(t)
	if _, err := s.Queue(t.Context(), "example.com", "wildcard", "issue"); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimCertificateJob(t.Context(), "old-worker", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Process(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	recovered, err := st.ClaimCertificateJob(t.Context(), "new-worker", time.Now().Add(21*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Process(t.Context(), recovered); err != nil {
		t.Fatal(err)
	}
	if issuer.calls != 1 {
		t.Fatal("recovered job reissued certificate")
	}
}

func TestACMEAccountKeyIsStableAndEncrypted(t *testing.T) {
	s, _, _, _, st := testService(t)
	issuer := &ACMEIssuer{Store: st, Vault: s.Vault}
	cfg := certconfig.ACME{DirectoryURL: "https://acme.example/directory", Email: "admin@example.com"}
	first, err := issuer.accountKey(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := issuer.accountKey(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, err := x509.MarshalPKCS8PrivateKey(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := x509.MarshalPKCS8PrivateKey(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("ACME account key changed")
	}
	cfg.DirectoryURL = "https://other-ca.example/directory"
	third, err := issuer.accountKey(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.MarshalPKCS8PrivateKey(third)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(c) {
		t.Fatal("CA accounts share keys")
	}
}
