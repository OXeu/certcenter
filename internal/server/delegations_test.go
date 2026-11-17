package server

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/OXeu/certcenter/internal/acmedns"
	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/miekg/dns"
)

func TestManagedCNAMEFlowWithoutRegistrationCredentials(t *testing.T) {
	f, s := certificateFlows(t)
	s.cfg.ACMEDNS = certconfig.ACMEDNS{Zone: "auth.example.net"}
	s.delegatedDNS = &acmedns.Service{Store: s.store, Config: s.cfg.ACMEDNS}
	if err := s.cfg.Remote.EnsureManagedDNS(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, body := f.do(http.MethodGet, "/api/v1/domains/example.com", nil)
	domain := body["domain"].(map[string]any)
	policies := domain["certificates"].([]any)
	policy := policies[0].(map[string]any)
	policy["dns_provider"] = certconfig.ManagedDNSProvider
	policy["names"] = []string{"example.com", "*.example.com", "sub.example.com"}
	status, reply := f.do(http.MethodPut, "/api/v1/domains/example.com", map[string]any{"version": body["version"], "certificates": policies})
	if status != http.StatusNoContent {
		t.Fatal("save domain without JSON registrations", status, reply)
	}
	_, body = f.do(http.MethodGet, "/api/v1/domains/example.com", nil)
	records := body["delegations"].([]any)
	if len(records) != 2 {
		t.Fatal("apex and wildcard should share CNAME", records)
	}
	target := records[0].(map[string]any)["target"].(string)
	if !strings.HasSuffix(target, ".auth.example.net") {
		t.Fatal("CNAME does not target this project", target)
	}
	_, again := f.do(http.MethodGet, "/api/v1/domains/example.com", nil)
	if again["delegations"].([]any)[0].(map[string]any)["target"] != target {
		t.Fatal("CNAME changed after refresh")
	}
	f.csrf = ""
	status, _ = f.do(http.MethodPost, "/api/v1/domains/example.com/delegations/check", nil)
	if status != http.StatusForbidden {
		t.Fatal("CNAME check bypassed CSRF")
	}
	f.csrf = "csrf"
	status, _ = f.do(http.MethodPost, "/api/v1/domains/other.example/delegations/check", nil)
	if status != http.StatusNotFound {
		t.Fatal("foreign domain CNAME check accepted")
	}
	providerAdmin(t, f, s)
	status, _ = f.do(http.MethodPost, "/api/v1/domains", map[string]any{"name": "new.example", "owner": "owner@example.com"})
	if status != http.StatusCreated {
		t.Fatal("create domain", status)
	}
	_, body = f.do(http.MethodGet, "/api/v1/domains/new.example", nil)
	if len(body["delegations"].([]any)) != 1 {
		t.Fatal("new domain did not receive a CNAME")
	}
	status, _ = f.do(http.MethodDelete, "/api/v1/dns-providers/"+certconfig.ManagedDNSProvider, map[string]any{"version": providerRevision(t, f, "/api/v1/dns-providers")})
	if status != http.StatusBadRequest {
		t.Fatal("removed server-managed provider", status)
	}
}

func TestUnconfiguredManagedDNSCannotBeSelectedOrCreated(t *testing.T) {
	f, s := certificateFlows(t)
	if err := s.cfg.Remote.EnsureManagedDNS(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, body := f.do(http.MethodGet, "/api/v1/domains/example.com", nil)
	for _, name := range body["dns_providers"].([]any) {
		if name == certconfig.ManagedDNSProvider {
			t.Fatal("unconfigured managed DNS offered")
		}
	}
	providerAdmin(t, f, s)
	status, _ := f.do(http.MethodPost, "/api/v1/dns-providers", map[string]any{"version": providerRevision(t, f, "/api/v1/dns-providers"), "name": "manual", "dns": map[string]string{"kind": "acme-dns"}})
	if status != http.StatusBadRequest {
		t.Fatal("unconfigured managed DNS accepted", status)
	}
}

func TestCNAMECheckUsesGeneratedTarget(t *testing.T) {
	f, s := certificateFlows(t)
	s.delegatedDNS = &acmedns.Service{Store: s.store, Config: certconfig.ACMEDNS{Zone: "auth.example.net"}}
	if err := s.cfg.Remote.EnsureManagedDNS(t.Context()); err != nil {
		t.Fatal(err)
	}
	domain, err := s.cfg.Remote.Domain(t.Context(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	domain.Certificates[0].DNSProvider = certconfig.ManagedDNSProvider
	if err := s.cfg.Remote.SaveDomain(t.Context(), domain); err != nil {
		t.Fatal(err)
	}
	record, err := s.delegatedDNS.Ensure(t.Context(), domain.Name, domain.Name)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var incorrect atomic.Bool
	ready := make(chan struct{})
	server := &dns.Server{PacketConn: packet, NotifyStartedFunc: func() { close(ready) }, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg).SetReply(r)
		if r.Question[0].Name == dns.Fqdn(record.Name) {
			target := record.Target
			if incorrect.Load() {
				target = "wrong.auth.example.net"
			}
			response.Answer = []dns.RR{&dns.CNAME{Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 30}, Target: dns.Fqdn(target)}}
		}
		if err := w.WriteMsg(response); err != nil {
			t.Error(err)
		}
	})}
	go func() {
		if err := server.ActivateAndServe(); err != nil {
			t.Error(err)
		}
	}()
	<-ready
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", packet.LocalAddr().String())
	}}
	t.Cleanup(func() {
		net.DefaultResolver = original
		if err := server.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	for _, valid := range []bool{true, false} {
		incorrect.Store(!valid)
		status, body := f.do(http.MethodPost, "/api/v1/domains/example.com/delegations/check", nil)
		if status != http.StatusOK {
			t.Fatal(status, body)
		}
		records := body["delegations"].([]any)
		if len(records) != 1 || records[0].(map[string]any)["verified"] != valid {
			t.Fatal("incorrect CNAME check", valid, body)
		}
	}
}
