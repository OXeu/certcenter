package certconfig

import (
	"strings"
	"testing"
)

func TestDomainScopeValidation(t *testing.T) {
	providers := Providers{DNS: map[string]DNS{"dns": {Kind: "alidns"}}, Upstreams: map[string]Upstream{"cloud": {Kind: "aliyun"}}}
	policy := Policy{ID: "main", Names: []string{"example.com", "*.example.com", "nested.example.com"}, DNSProvider: "dns", RenewBeforeDays: 30, Upstreams: []string{"cloud"}}
	domain := Domain{Name: "example.com", Owner: "admin@example.com", Certificates: []Policy{policy}}
	if err := domain.Validate(providers); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"evil.com", "example.com.evil.com", "127.0.0.1", "*.*.example.com", "-bad.example.com"} {
		domain.Certificates[0].Names = []string{name}
		if err := domain.Validate(providers); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	domain.Certificates[0] = policy
	domain.Certificates[0].RenewBeforeDays = 0
	if err := domain.Validate(providers); err == nil {
		t.Fatal("unbounded renewal window")
	}
}

func TestDelegatedDNSRequiresAllRegistrations(t *testing.T) {
	p := Providers{DNS: map[string]DNS{"delegated": {Kind: "acme-dns", Accounts: map[string]DNSAccount{"example.com": {FullDomain: "id.auth.example.net"}}}}}
	d := Domain{Name: "example.com", Owner: "admin@example.com", Certificates: []Policy{{ID: "wildcard", Names: []string{"example.com", "*.example.com"}, DNSProvider: "delegated", RenewBeforeDays: 30}}}
	if err := d.Validate(p); err != nil {
		t.Fatal(err)
	}
	d.Certificates[0].Names = append(d.Certificates[0].Names, "sub.example.com")
	if err := d.Validate(p); err == nil {
		t.Fatal("missing delegated account accepted")
	}
}

func TestLongDomainKeysRespectConfigCenterLimit(t *testing.T) {
	name := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + ".example"
	if !ValidDomain(name) {
		t.Fatal("valid long DNS name rejected")
	}
	if key := DomainKey(name); len(key) > 191 || !strings.HasPrefix(key, "certcenter.domain.sha256-") {
		t.Fatal("long name exceeds ConfigCenter key limit")
	}
	if DomainKey("example.com") != "certcenter.domain.example.com" {
		t.Fatal("short domain keys changed")
	}
}
