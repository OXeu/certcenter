package certificates

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/OXeu/certcenter/internal/upstream"
)

type updatingUpstream struct {
	uploads int
	updates int
	fail    bool
	oldIDs  []string
}

func (f *updatingUpstream) Upload(_ context.Context, _ certconfig.Upstream, b upstream.Bundle) (string, error) {
	f.uploads++
	return b.Fingerprint, nil
}

func (f *updatingUpstream) Advance(_ context.Context, cfg certconfig.Upstream, oldID, newID string, p upstream.UpdateProgress) (upstream.UpdateProgress, error) {
	f.updates++
	f.oldIDs = append(f.oldIDs, oldID)
	if oldID == newID {
		return p, errors.New("cannot delete the active certificate")
	}
	if p.Stage == "" {
		p.Stage = "updating"
		p.TaskID = "persisted-task"
		return p, nil
	}
	if p.TaskID != "persisted-task" {
		return p, errors.New("lost cloud task checkpoint")
	}
	if f.fail {
		return p, errors.New("cloud resource update failed")
	}
	if p.Stage == "updating" {
		p.Stage = "updated"
		return p, nil
	}
	if p.Stage == "updated" && cfg.DeleteOld {
		p.Stage = "deleted"
		return p, nil
	}
	return p, nil
}

func issueJob(t *testing.T, s *Service, st *store.Store) store.CertificateJob {
	t.Helper()
	if _, err := s.Queue(t.Context(), "example.com", "wildcard", "issue"); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimCertificateJob(t.Context(), "worker", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestCloudUpdateResumesWithoutUploadingAgain(t *testing.T) {
	s, settings, issuer, _, st := testService(t)
	settings.domain.Certificates[0].Upstreams = []string{"tencent"}
	settings.providers.Upstreams["tencent"] = certconfig.Upstream{Kind: "tencentcloud", UpdateResources: true, DeleteOld: true}
	cloud := &updatingUpstream{}
	s.Upstream = cloud
	now := time.Now()
	issuer.material = testMaterial(t, []string{"example.com", "*.example.com", "WWW.example.com"}, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	s.execute(t.Context(), issueJob(t, s, st))
	old, err := st.LatestCertificate(t.Context(), "example.com", "wildcard")
	if err != nil {
		t.Fatal(err)
	}
	issuer.material = testMaterial(t, []string{"example.com", "*.example.com"}, now.Add(-time.Minute), now.Add(90*24*time.Hour))
	job := issueJob(t, s, st)
	s.execute(t.Context(), job)
	if cloud.uploads != 2 || cloud.updates != 1 {
		t.Fatal("unexpected initial deployment", cloud)
	}
	queued, err := st.LastCertificateJob(t.Context(), job.Domain, job.PolicyID)
	if err != nil || queued.Status != "queued" || queued.ActiveKey == nil || queued.AvailableAt == nil {
		t.Fatal("pending update released policy lock", queued, err)
	}
	if _, err := st.ClaimCertificateJob(t.Context(), "early", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("cloud polling has no delay", err)
	}
	if _, err := s.Queue(t.Context(), job.Domain, job.PolicyID, "issue"); !errors.Is(err, store.ErrBusy) {
		t.Fatal("issuance overlapped pending replacement", err)
	}
	if err := s.Schedule(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	restarted := *s
	for i := 0; i < 3; i++ {
		next, err := st.ClaimCertificateJob(t.Context(), "restarted", time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		restarted.execute(t.Context(), next)
	}
	finished, err := st.LastCertificateJob(t.Context(), job.Domain, job.PolicyID)
	if err != nil || finished.Status != "succeeded" {
		t.Fatal("replacement did not complete", finished, err)
	}
	if cloud.uploads != 2 || cloud.updates != 3 {
		t.Fatal("restart re-uploaded or repeated a finished update", cloud)
	}
	for _, id := range cloud.oldIDs {
		if id != old.Fingerprint {
			t.Fatal("updated an unrelated cloud certificate")
		}
	}
	current, err := st.LatestCertificate(t.Context(), job.Domain, job.PolicyID)
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := st.Deployments(t.Context(), current.ID)
	if err != nil || len(receipts) != 1 || !receipts[0].ResourcesUpdated || !receipts[0].OldDeleted {
		t.Fatal("completion not persisted", receipts, err)
	}
	versions, err := st.Certificates(t.Context(), job.Domain)
	if err != nil || len(versions) != 2 {
		t.Fatal("cloud cleanup removed local certificate history", err)
	}
	if err := s.Process(t.Context(), store.CertificateJob{Domain: job.Domain, PolicyID: job.PolicyID, Action: "deploy"}); err != nil {
		t.Fatal(err)
	}
	if cloud.uploads != 2 || cloud.updates != 3 {
		t.Fatal("manual deploy duplicated cloud resources")
	}
}

func TestFailedCloudUpdateRetriesTheSavedUpload(t *testing.T) {
	s, settings, issuer, _, st := testService(t)
	settings.domain.Certificates[0].Upstreams = []string{"tencent"}
	settings.providers.Upstreams["tencent"] = certconfig.Upstream{Kind: "tencentcloud", UpdateResources: true}
	cloud := &updatingUpstream{}
	s.Upstream = cloud
	s.execute(t.Context(), issueJob(t, s, st))
	issuer.material = testMaterial(t, []string{"example.com", "*.example.com"}, time.Now().Add(-time.Minute), time.Now().Add(90*24*time.Hour))
	s.execute(t.Context(), issueJob(t, s, st))
	cloud.fail = true
	job, err := st.ClaimCertificateJob(t.Context(), "worker", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	s.execute(t.Context(), job)
	last, err := st.LastCertificateJob(t.Context(), job.Domain, job.PolicyID)
	if err != nil || last.Status != "failed" {
		t.Fatal("failed update was reported as deployed", err)
	}
	if err := s.Schedule(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimCertificateJob(t.Context(), "worker", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failure backoff bypassed", err)
	}
	cloud.fail = false
	if err := s.Schedule(t.Context(), time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		job, err = st.ClaimCertificateJob(t.Context(), "retry", time.Now().Add(time.Minute))
		if err != nil || job.Action != "deploy" {
			t.Fatal("retry should resume deployment", err)
		}
		s.execute(t.Context(), job)
	}
	if cloud.uploads != 2 || issuer.calls != 2 {
		t.Fatal("retry reissued or reuploaded material")
	}
}

func TestResourceUpdateRejectsReducedNames(t *testing.T) {
	s, settings, issuer, _, st := testService(t)
	settings.domain.Certificates[0].Upstreams = []string{"tencent"}
	settings.providers.Upstreams["tencent"] = certconfig.Upstream{Kind: "tencentcloud"}
	cloud := &updatingUpstream{}
	s.Upstream = cloud
	s.execute(t.Context(), issueJob(t, s, st))
	older, err := st.LatestCertificate(t.Context(), "example.com", "wildcard")
	if err != nil {
		t.Fatal(err)
	}
	settings.domain.Certificates[0].Names = []string{"example.com"}
	now := time.Now()
	issuer.material = testMaterial(t, []string{"example.com"}, now.Add(-time.Minute), now.Add(90*24*time.Hour))
	s.execute(t.Context(), issueJob(t, s, st))
	if cloud.uploads != 2 {
		t.Fatal("expected two prior deployments")
	}
	settings.providers.Upstreams["tencent"] = certconfig.Upstream{Kind: "tencentcloud", UpdateResources: true, DeleteOld: true}
	issuer.material = testMaterial(t, []string{"example.com"}, now.Add(-time.Minute), now.Add(90*24*time.Hour))
	err = s.Process(t.Context(), issueJob(t, s, st))
	if err == nil || !strings.Contains(err.Error(), older.Fingerprint) || !strings.Contains(err.Error(), "*.example.com") {
		t.Fatalf("expected the blocked replacement to identify the old certificate and missing wildcard, got %v", err)
	}
	if cloud.updates != 0 {
		t.Fatal("cloud replacement started before all old certificates were checked")
	}
}
