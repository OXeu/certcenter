package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/certificates"
	"github.com/OXeu/certcenter/internal/config"
	"github.com/OXeu/certcenter/internal/configcenter"
	"github.com/OXeu/certcenter/internal/store"
)

type unavailableCertificateQueue struct {
	certificates.Repository
}

func (s unavailableCertificateQueue) EnqueueCertificate(context.Context, store.CertificateJob) (store.CertificateJob, error) {
	return store.CertificateJob{}, errors.New("database unavailable: private connection detail")
}

func TestCertificateQueueExplainsPolicyFailures(t *testing.T) {
	for _, tc := range []struct {
		name, policy, action string
		enabled              bool
		upstreams            []string
		status               int
		message              string
	}{
		{"missing", "missing", "issue", true, nil, 404, certificates.ErrPolicyNotFound.Error()},
		{"disabled", "main", "issue", false, nil, 400, certificates.ErrPolicyDisabled.Error()},
		{"no targets", "main", "deploy", true, nil, 400, certificates.ErrNoUpstreams.Error()},
		{"issuance without deployment", "main", "issue", true, nil, 202, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, s := certificateFlows(t)
			domain, err := s.cfg.Remote.Domain(t.Context(), "example.com")
			if err != nil {
				t.Fatal(err)
			}
			domain.Certificates[0].Enabled = tc.enabled
			domain.Certificates[0].Upstreams = tc.upstreams
			if err := s.cfg.Remote.SaveDomain(t.Context(), domain); err != nil {
				t.Fatal(err)
			}
			status, body := f.do(http.MethodPost, "/api/v1/domains/example.com/certificates/"+tc.policy+"/"+tc.action, nil)
			if status != tc.status || (tc.message != "" && body["error"] != tc.message) {
				t.Fatalf("queue response: %d %+v", status, body)
			}
		})
	}
}

func TestCertificateQueueLogsStorageFailureWithoutMisclassifyingPolicy(t *testing.T) {
	f, s := certificateFlows(t)
	var logs bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	s.certificates.Store = unavailableCertificateQueue{Repository: s.store}
	status, body := f.do(http.MethodPost, "/api/v1/domains/example.com/certificates/main/issue", nil)
	message, ok := body["error"].(string)
	if status != http.StatusServiceUnavailable || !ok || !strings.Contains(message, "database connectivity") || strings.Contains(message, "private connection detail") {
		t.Fatalf("storage failure response: %d %+v", status, body)
	}
	if !strings.Contains(logs.String(), "enqueue operation: database unavailable") {
		t.Fatal("queue failure missing from server logs")
	}
}

func certificateFlows(t *testing.T) (*flows, *Server) {
	t.Helper()
	providers := certconfig.Providers{ACME: certconfig.ACME{DirectoryURL: "https://ca.example/directory", Email: "admin@example.com"}, DNS: map[string]certconfig.DNS{"dns": {Kind: "alidns", AccessKey: "secret-id", SecretKey: "do-not-expose"}}, Upstreams: map[string]certconfig.Upstream{"ali": {Kind: "aliyun", AccessKey: "cloud-id", SecretKey: "cloud-secret"}}}
	domain := certconfig.Domain{Name: "example.com", Owner: "owner@example.com", Certificates: []certconfig.Policy{{ID: "main", Names: []string{"example.com"}, DNSProvider: "dns", Enabled: true, RenewBeforeDays: 30, Upstreams: []string{"ali"}}}}
	other := certconfig.Domain{Name: "other.example", Owner: "other@example.com"}
	values := map[string]any{"certcenter.providers": providers, "certcenter.domain.example.com": domain, "certcenter.domain.other.example": other}
	var mu sync.Mutex
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer cc_test" {
			t.Error("SDK key missing")
		}
		if r.URL.Path == "/api/v1/agent/project" {
			json.NewEncoder(w).Encode(map[string]any{"project": map[string]string{"slug": "certcenter"}})
			return
		}
		if r.Method == http.MethodPut {
			var in struct {
				Value string `json:"value"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			var value any
			if err := json.Unmarshal([]byte(in.Value), &value); err != nil {
				t.Error(err)
			}
			key := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			values[key] = value
			w.WriteHeader(http.StatusOK)
			return
		}
		items := []configcenter.Item{}
		for key, value := range values {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Error(err)
			}
			items = append(items, configcenter.Item{Key: key, Value: string(raw)})
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	t.Cleanup(remoteServer.Close)
	client, err := configcenter.New(configcenter.Config{BaseURL: remoteServer.URL, APIKey: "cc_test"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := newServer(t.Context(), config.Config{PublicURL: "http://cc.test", DatabaseDSN: "file:" + t.Name() + "?mode=memory&cache=shared", Languages: []string{"en", "zh-CN"}, SessionTTL: time.Hour, Bootstrap: config.Bootstrap{AdminEmail: "admin@example.com", AdminName: "admin"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Remote = certconfig.NewRemote(client)
	s.cfg.ConfigCenterURL = remoteServer.URL
	s.certificates = &certificates.Service{Store: s.store, Settings: s.cfg.Remote}
	owner, err := s.store.CreateUser(t.Context(), store.User{Email: "owner@example.com", Name: "owner", Role: store.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.CreateSession(t.Context(), store.Session{ID: "owner-session", UserID: owner.ID, CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return &flows{t: t, router: s.router(), cookies: map[string]*http.Cookie{"cc_session": {Name: "cc_session", Value: "owner-session"}}, csrf: "csrf"}, s
}

func TestCertificateDomainIsolationCSRFAndQueue(t *testing.T) {
	f, s := certificateFlows(t)
	status, body := f.do(http.MethodGet, "/api/v1/domains", nil)
	if status != 200 || len(body["domains"].([]any)) != 1 {
		t.Fatalf("domain isolation: %d %+v", status, body)
	}
	status, _ = f.do(http.MethodGet, "/api/v1/domains/other.example", nil)
	if status != 404 {
		t.Fatal("foreign domain visible", status)
	}
	status, _ = f.do(http.MethodPost, "/api/v1/domains/other.example/certificates/main/issue", nil)
	if status != 404 {
		t.Fatal("foreign domain mutation", status)
	}
	f.csrf = ""
	status, _ = f.do(http.MethodPost, "/api/v1/domains/example.com/certificates/main/issue", nil)
	if status != 403 {
		t.Fatal("CSRF bypass", status)
	}
	f.csrf = "csrf"
	status, _ = f.do(http.MethodPost, "/api/v1/domains/example.com/certificates/main/issue", nil)
	if status != 202 {
		t.Fatal("queue rejected", status)
	}
	status, _ = f.do(http.MethodPost, "/api/v1/domains/example.com/certificates/main/issue", nil)
	if status != 409 {
		t.Fatal("duplicate accepted", status)
	}
	status, _ = f.do(http.MethodPost, "/api/v1/domains", map[string]string{"name": "new.example", "owner": "owner@example.com"})
	if status != 403 {
		t.Fatal("non-admin domain creation", status)
	}
	status, _ = f.do(http.MethodGet, "/api/v1/certificate-settings", nil)
	if status != 403 {
		t.Fatal("non-admin provider settings", status)
	}
	if _, err := s.store.SaveCertificate(t.Context(), store.Certificate{Domain: "example.com", PolicyID: "main", Material: []byte("PRIVATE KEY MATERIAL"), Fingerprint: "fingerprint"}); err != nil {
		t.Fatal(err)
	}
	status, body = f.do(http.MethodGet, "/api/v1/domains/example.com", nil)
	raw, _ := json.Marshal(body)
	if status != 200 || strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "do-not-expose") || strings.Contains(string(raw), "secret-id") {
		t.Fatal("certificate view leaked credentials", status)
	}
	status, _ = f.do(http.MethodGet, "/api/v1/projects", nil)
	if status != 404 {
		t.Fatal("legacy configuration API exposed", status)
	}
}

func TestCertificatePolicyWritesUseSDKAndRejectStaleOrForeignNames(t *testing.T) {
	f, _ := certificateFlows(t)
	_, body := f.do(http.MethodGet, "/api/v1/domains/example.com", nil)
	domain := body["domain"].(map[string]any)
	policies := domain["certificates"].([]any)
	policy := policies[0].(map[string]any)
	policy["names"] = []string{"evil.example"}
	status, _ := f.do(http.MethodPut, "/api/v1/domains/example.com", map[string]any{"version": body["version"], "certificates": policies})
	if status != 400 {
		t.Fatal("foreign certificate name accepted", status)
	}
	policy["names"] = []string{"example.com", "*.example.com"}
	status, _ = f.do(http.MethodPut, "/api/v1/domains/example.com", map[string]any{"version": body["version"], "certificates": policies})
	if status != 204 {
		t.Fatal("SDK write failed", status)
	}
	status, _ = f.do(http.MethodPut, "/api/v1/domains/example.com", map[string]any{"version": body["version"], "certificates": policies})
	if status != 409 {
		t.Fatal("stale policy overwrite accepted", status)
	}
}
