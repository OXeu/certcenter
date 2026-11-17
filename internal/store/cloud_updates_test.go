package store

import (
	"testing"

	"github.com/glebarez/sqlite"
)

func TestPreviousDeploymentsStayWithinPolicyAndDestination(t *testing.T) {
	s, err := Open(t.Context(), sqlite.Open(":memory:"), "admin@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ domain, policy, target, revision, remote string }{
		{"example.com", "main", "cloud", "revision", "keep"},
		{"other.com", "main", "cloud", "revision", "other-domain"},
		{"example.com", "other", "cloud", "revision", "other-policy"},
		{"example.com", "main", "other", "revision", "other-target"},
		{"example.com", "main", "cloud", "other", "other-account"},
		{"example.com", "main", "cloud", "revision", "removed"},
	} {
		cert, err := s.SaveCertificate(t.Context(), Certificate{Domain: row.domain, PolicyID: row.policy, Names: []string{row.domain}, Fingerprint: row.remote, Material: []byte("encrypted")})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SaveDeployment(t.Context(), Deployment{CertificateID: cert.ID, Target: row.target, Revision: row.revision, RemoteID: row.remote}); err != nil {
			t.Fatal(err)
		}
	}
	current, err := s.SaveCertificate(t.Context(), Certificate{Domain: "example.com", PolicyID: "main", Names: []string{"example.com"}, Fingerprint: "current", Material: []byte("encrypted")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDeployment(t.Context(), Deployment{CertificateID: current.ID, Target: "cloud", Revision: "revision", RemoteID: "current"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCloudUpdate(t.Context(), CloudUpdate{CertificateID: current.ID, Target: "cloud", Revision: "revision", OldRemoteID: "removed", Deleted: true, State: []byte(`{"stage":"deleted"}`)}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.PreviousDeployments(t.Context(), current, "cloud", "revision")
	if err != nil || len(rows) != 1 || rows[0].RemoteID != "keep" || len(rows[0].Names) != 1 || rows[0].Names[0] != "example.com" {
		t.Fatal("unsafe prior certificate selection", rows, err)
	}
}
