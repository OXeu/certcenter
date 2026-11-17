package acmedns

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/glebarez/sqlite"
	"github.com/miekg/dns"
)

func testService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(t.Context(), sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), "admin@example.com", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Store: st, Config: certconfig.ACMEDNS{Zone: "auth.example.net", Nameserver: "ns.auth.example.net", Addresses: []string{"192.0.2.1", "2001:db8::1"}, ListenAddress: "127.0.0.1:0"}}
}

func query(name string, kind uint16) *dns.Msg { return new(dns.Msg).SetQuestion(dns.Fqdn(name), kind) }

func TestScopedPersistentDelegationsAndConcurrentChallenges(t *testing.T) {
	s := testService(t)
	ctx := t.Context()
	root, err := s.Ensure(ctx, "example.com", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	wildcard, err := s.Ensure(ctx, "example.com", "*.example.com")
	if err != nil || wildcard.Target != root.Target {
		t.Fatal("apex and wildcard must share a target", err)
	}
	restarted := &Service{Store: s.Store, Config: s.Config}
	again, err := restarted.Ensure(ctx, "example.com", "example.com")
	if err != nil || again.Target != root.Target {
		t.Fatal("delegation changed on restart", err)
	}
	child, err := s.Ensure(ctx, "example.com", "sub.example.com")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Ensure(ctx, "sub.example.com", "sub.example.com")
	if err != nil || child.Target == other.Target {
		t.Fatal("overlapping owners share a delegation", err)
	}
	p := s.Provider(ctx, "example.com")
	for _, challenge := range []struct{ name, key string }{{"example.com", "first"}, {"*.example.com", "second"}, {"example.com", "third"}} {
		if err := p.Present(challenge.name, "", challenge.key); err != nil {
			t.Fatal(err)
		}
	}
	answer := s.answer(ctx, query(root.Target, dns.TypeTXT))
	if len(answer.Answer) != 3 || !answer.Authoritative {
		t.Fatal("concurrent challenge lost", answer)
	}
	if err := p.CleanUp("example.com", "", "first"); err != nil {
		t.Fatal(err)
	}
	if len(s.answer(ctx, query(root.Target, dns.TypeTXT)).Answer) != 2 {
		t.Fatal("cleanup deleted another job's TXT")
	}
	if err := p.Present("outside.example", "", "invalid"); err == nil {
		t.Fatal("outside-scope write accepted")
	}
	if len(s.answer(ctx, query(other.Target, dns.TypeTXT)).Answer) != 0 {
		t.Fatal("another owner's target was modified")
	}
	if err := s.Store.PutDNSChallenge(ctx, store.DNSChallenge{DelegationID: delegationID("example.com", "example.com"), Value: "expired", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	for _, rr := range s.answer(ctx, query(root.Target, dns.TypeTXT)).Answer {
		if strings.Contains(rr.String(), "expired") {
			t.Fatal("expired TXT served")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Provider(cancelled, "example.com").Present("example.com", "", "cancelled"); err == nil {
		t.Fatal("cancelled write accepted")
	}
}

func TestAuthoritativeDNSOverUDPAndTCP(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := s.Start(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	record, err := s.Ensure(ctx, "example.com", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Provider(ctx, "example.com").Present("example.com", "", "authorization"); err != nil {
		t.Fatal(err)
	}
	for _, network := range []string{"udp", "tcp"} {
		client := &dns.Client{Net: network, Timeout: time.Second}
		for _, tc := range []struct {
			name         string
			kind         uint16
			rcode, count int
		}{
			{record.Target, dns.TypeTXT, dns.RcodeSuccess, 1},
			{record.Target, dns.TypeA, dns.RcodeSuccess, 0},
			{s.Config.Zone, dns.TypeSOA, dns.RcodeSuccess, 1},
			{s.Config.Zone, dns.TypeNS, dns.RcodeSuccess, 1},
			{s.Config.Nameserver, dns.TypeA, dns.RcodeSuccess, 1},
			{s.Config.Nameserver, dns.TypeAAAA, dns.RcodeSuccess, 1},
			{"unknown.auth.example.net", dns.TypeTXT, dns.RcodeNameError, 0},
			{"outside.example", dns.TypeA, dns.RcodeRefused, 0},
			{s.Config.Zone, dns.TypeAXFR, dns.RcodeRefused, 0},
		} {
			response, _, err := client.ExchangeContext(ctx, query(tc.name, tc.kind), s.Config.ListenAddress)
			if err != nil {
				t.Fatal(err)
			}
			if response.Rcode != tc.rcode || len(response.Answer) != tc.count || response.RecursionAvailable {
				t.Fatalf("%s %s: %+v", network, tc.name, response)
			}
		}
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, _, err := (&dns.Client{Net: "tcp", Timeout: 20 * time.Millisecond}).Exchange(query(record.Target, dns.TypeTXT), s.Config.ListenAddress)
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("DNS listener survived process cancellation")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
