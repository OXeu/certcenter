package dnsprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/go-acme/lego/v4/challenge/dns01"
)

func TestDelegatedChallengesPreserveApexAndWildcard(t *testing.T) {
	values := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/update" || r.Method != http.MethodPost || r.Header.Get("X-Api-User") != "user" || r.Header.Get("X-Api-Key") != "secret" {
			t.Error("wrong acme-dns request")
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["subdomain"] != "registration" {
			t.Error("wrong delegated record")
		}
		values = append(values, body["txt"])
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	provider, err := New(ctx, certconfig.DNS{Kind: "acme-dns", APIBase: server.URL, Accounts: map[string]certconfig.DNSAccount{"example.com": {Username: "user", Password: "secret", Subdomain: "registration"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Present("example.com", "token", "first.authorization"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Present("*.example.com", "token", "second.authorization"); err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0] != dns01.GetChallengeInfo("example.com", "first.authorization").Value || values[0] == values[1] {
		t.Fatal("challenge values overwritten or incorrectly derived")
	}
	if err := provider.CleanUp("example.com", "token", "first.authorization"); err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 {
		t.Fatal("cleanup must leave the two rolling delegated records intact")
	}
	cancel()
	if err := provider.Present("example.com", "token", "third.authorization"); err == nil {
		t.Fatal("cancelled challenge accepted")
	}
}
