package store

import (
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
)

func TestCertificateQueueLeaseLifecycle(t *testing.T) {
	st, err := Open(t.Context(), sqlite.Open(":memory:"), "admin@example.com", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	key := "domain-policy"
	job, err := st.EnqueueCertificate(t.Context(), CertificateJob{Domain: "example.com", PolicyID: "main", Action: "issue", ActiveKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	var unleased int64
	if err := st.db.WithContext(t.Context()).Model(&CertificateJob{}).Where("id = ? AND lease_until IS NULL", job.ID).Count(&unleased).Error; err != nil {
		t.Fatal(err)
	}
	if unleased != 1 {
		t.Fatal("unclaimed jobs must store NULL, not a zero date")
	}
	now := time.Now()
	first, err := st.ClaimCertificateJob(t.Context(), "first", now)
	if err != nil {
		t.Fatal(err)
	}
	if first.LeaseUntil == nil || !first.LeaseUntil.Equal(now.Add(20*time.Minute)) {
		t.Fatal("claim did not return its lease")
	}
	if _, err := st.ClaimCertificateJob(t.Context(), "second", now.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatal("active lease was claimed", err)
	}
	second, err := st.ClaimCertificateJob(t.Context(), "second", now.Add(21*time.Minute))
	if err != nil || second.ID != job.ID {
		t.Fatal("expired lease was not recovered", err)
	}
	if err := st.FinishCertificateJob(t.Context(), first, "succeeded"); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale worker finished job", err)
	}
	if err := st.FinishCertificateJob(t.Context(), second, "succeeded"); err != nil {
		t.Fatal(err)
	}
	last, err := st.LastCertificateJob(t.Context(), job.Domain, job.PolicyID)
	if err != nil || last.LeaseUntil != nil || last.ActiveKey != nil || last.Status != "succeeded" {
		t.Fatal("finished job retained its lease", err)
	}
	if _, err := st.EnqueueCertificate(t.Context(), CertificateJob{Domain: job.Domain, PolicyID: job.PolicyID, Action: "issue", ActiveKey: &key}); err != nil {
		t.Fatal("finished policy cannot be reissued", err)
	}
}
