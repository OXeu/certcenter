package certificates

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/OXeu/certcenter/internal/upstream"
)

// Run polls the durable queue and periodically evaluates live renewal policies.
func (s *Service) Run(ctx context.Context) {
	worker := rand.Text()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	nextSchedule := time.Time{}
	for {
		if ctx.Err() != nil {
			return
		}
		if time.Now().After(nextSchedule) {
			if err := s.Schedule(ctx, time.Now()); err != nil {
				s.Log.Error("certificate scheduling failed", "error", err)
			}
			nextSchedule = time.Now().Add(time.Minute)
		}
		job, err := s.Store.ClaimCertificateJob(ctx, worker, time.Now())
		if err == nil {
			s.execute(ctx, job)
			continue
		}
		if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrBusy) && ctx.Err() == nil {
			s.Log.Error("certificate queue unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) execute(ctx context.Context, job store.CertificateJob) {
	operation, cancel := context.WithTimeout(ctx, 10*time.Minute)
	err := s.Process(operation, job)
	cancel()
	finish, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer finishCancel()
	if errors.Is(err, errCloudPending) {
		if deferErr := s.Store.DeferCertificateJob(finish, job, time.Now().Add(15*time.Second)); deferErr != nil {
			s.Log.Error("cannot defer cloud update", "job", job.ID)
		}
		return
	}
	status, message := "succeeded", "Operation completed"
	if err != nil {
		status, message = "failed", err.Error()
	}
	if eventErr := s.event(finish, job, status, "", message); eventErr != nil {
		s.Log.Error("cannot persist operation log", "job", job.ID)
	}
	if finishErr := s.Store.FinishCertificateJob(finish, job, status); finishErr != nil {
		s.Log.Error("cannot finish certificate job", "job", job.ID)
	}
}

// Schedule queues due renewals and incomplete uploads with an hourly failure backoff.
func (s *Service) Schedule(ctx context.Context, now time.Time) error {
	domains, err := s.Settings.Domains(ctx)
	if err != nil {
		return fmt.Errorf("refresh certificate settings: %w", err)
	}
	for _, domain := range domains {
		for _, policy := range domain.Certificates {
			if !policy.Enabled {
				continue
			}
			last, err := s.Store.LastCertificateJob(ctx, domain.Name, policy.ID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("read last operation: %w", err)
			}
			if err == nil && (last.Status == "running" || last.Status == "queued" || (last.Status == "failed" && now.Sub(last.UpdatedAt) < time.Hour)) {
				continue
			}
			action, err := s.due(ctx, domain.Name, policy, now)
			if err != nil {
				return err
			}
			if action == "" {
				continue
			}
			if _, err := s.queue(ctx, domain.Name, policy.ID, action, true); err != nil && !errors.Is(err, store.ErrBusy) {
				return err
			}
		}
	}
	return nil
}

func (s *Service) due(ctx context.Context, domain string, policy certconfig.Policy, now time.Time) (string, error) {
	cert, err := s.Store.LatestCertificate(ctx, domain, policy.ID)
	if errors.Is(err, store.ErrNotFound) {
		if policy.AutoRenew {
			return "issue", nil
		}
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load renewal candidate: %w", err)
	}
	window := min(time.Duration(policy.RenewBeforeDays)*24*time.Hour, cert.NotAfter.Sub(cert.NotBefore)/3)
	if policy.AutoRenew && (!matchesPolicy(cert, policy) || !cert.NotAfter.After(now.Add(window))) {
		return "issue", nil
	}
	if !policy.AutoDeploy || !cert.NotAfter.After(now) || !matchesPolicy(cert, policy) {
		return "", nil
	}
	receipts, err := s.Store.Deployments(ctx, cert.ID)
	if err != nil {
		return "", fmt.Errorf("read upload state: %w", err)
	}
	providers, err := s.Settings.Providers(ctx)
	if err != nil {
		return "", fmt.Errorf("read upstream settings: %w", err)
	}
	for _, target := range policy.Upstreams {
		revision := upstream.Revision(providers.Upstreams[target])
		found := false
		for _, receipt := range receipts {
			if receipt.Target == target && receipt.Revision == revision && deploymentComplete(receipt, providers.Upstreams[target]) {
				found = true
				break
			}
		}
		if !found {
			return "deploy", nil
		}
	}
	return "", nil
}
