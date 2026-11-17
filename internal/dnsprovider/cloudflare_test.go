package dnsprovider

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/miekg/dns"
)

func TestCloudflareTokenCreatesAndDeletesOnlyItsTXTRecord(t *testing.T) {
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	server := &dns.Server{PacketConn: packet, NotifyStartedFunc: func() { close(ready) }, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg).SetReply(r)
		if r.Question[0].Qtype == dns.TypeSOA {
			response.Answer = []dns.RR{&dns.SOA{Hdr: dns.RR_Header{Name: "example.test.", Rrtype: dns.TypeSOA, Class: dns.ClassINET}, Ns: "ns.example.test.", Mbox: "hostmaster.example.test.", Refresh: 60}}
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
	t.Cleanup(func() {
		if err := server.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	resolvers, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	if err := dns01.AddRecursiveNameservers([]string{packet.LocalAddr().String()})(nil); err != nil {
		t.Fatal(err)
	}
	dns01.ClearFqdnCache()
	t.Cleanup(func() {
		dns01.ClearFqdnCache()
		if err := dns01.AddRecursiveNameservers(resolvers.Servers)(nil); err != nil {
			t.Error(err)
		}
	})
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	calls := []string{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	http.DefaultTransport = transport(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer cf-test-token" {
			t.Error("Cloudflare token missing")
		}
		if req.Context() != ctx {
			t.Error("request lost operation context")
		}
		calls = append(calls, req.Method+" "+req.URL.Path)
		body := `{"success":true,"result":{"id":"created-record"}}`
		switch req.Method {
		case http.MethodGet:
			body = `{"success":true,"result":[{"id":"zone-id","name":"example.test"}]}`
		case http.MethodPost:
			var record map[string]any
			if err := json.NewDecoder(req.Body).Decode(&record); err != nil {
				t.Fatal(err)
			}
			if record["type"] != "TXT" || record["name"] != "_acme-challenge.example.test" || record["content"] == "" {
				t.Fatal("wrong TXT record", record)
			}
		case http.MethodDelete:
			if !strings.HasSuffix(req.URL.Path, "/created-record") {
				t.Fatal("deleted an unowned record")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}, nil
	})
	provider, err := New(ctx, certconfig.DNS{Kind: "cloudflare", APIToken: "cf-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Present("example.test", "token", "authorization"); err != nil {
		t.Fatal(err)
	}
	if err := provider.CleanUp("example.test", "token", "authorization"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0] != "GET /client/v4/zones" || calls[1] != "POST /client/v4/zones/zone-id/dns_records" || calls[2] != "DELETE /client/v4/zones/zone-id/dns_records/created-record" {
		t.Fatal("unexpected Cloudflare operations", calls)
	}
	cancel()
	if err := provider.Present("example.test", "next", "authorization"); err == nil {
		t.Fatal("cancelled challenge accepted")
	}
}
