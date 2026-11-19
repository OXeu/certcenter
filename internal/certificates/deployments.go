package certificates

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/OXeu/certcenter/internal/upstream"
)

var errCloudPending = errors.New("cloud resource update is pending")

func deploymentComplete(receipt store.Deployment, cfg certconfig.Upstream) bool {
	return !cfg.UpdateResources || (receipt.ResourcesUpdated && (!cfg.DeleteOld || receipt.OldDeleted))
}

func (s *Service) deploy(ctx context.Context, job store.CertificateJob, policy certconfig.Policy, providers certconfig.Providers, cert store.Certificate) error {
	if !matchesPolicy(cert, policy) {
		return errors.New("stored certificate names differ from policy; issue a new certificate first")
	}
	if !cert.NotAfter.After(time.Now()) {
		return errors.New("expired certificates cannot be deployed")
	}
	plain, err := s.Vault.Open(cert.Domain+":"+cert.PolicyID+":"+cert.Fingerprint, cert.Material)
	if err != nil {
		return err
	}
	var material Material
	if err := json.Unmarshal(plain, &material); err != nil {
		return errors.New("cannot decode stored certificate")
	}
	deployed, err := s.Store.Deployments(ctx, cert.ID)
	if err != nil {
		return fmt.Errorf("load deployment receipts: %w", err)
	}
	var failures []error
	pending := false
	for _, target := range policy.Upstreams {
		cfg := providers.Upstreams[target]
		receipt := store.Deployment{CertificateID: cert.ID, Target: target, Revision: upstream.Revision(cfg)}
		for _, existing := range deployed {
			if existing.Target == target && existing.Revision == receipt.Revision {
				receipt = existing
				break
			}
		}
		if err := s.deployTarget(ctx, job, cfg, cert, material, receipt); err != nil {
			if errors.Is(err, errCloudPending) {
				pending = true
				continue
			}
			failures = append(failures, fmt.Errorf("upstream %s: %w", target, err))
			if eventErr := s.event(ctx, job, "deployment_failed", target, err.Error()); eventErr != nil {
				return eventErr
			}
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if pending {
		return errCloudPending
	}
	return nil
}

func (s *Service) deployTarget(ctx context.Context, job store.CertificateJob, cfg certconfig.Upstream, cert store.Certificate, material Material, receipt store.Deployment) error {
	if receipt.RemoteID == "" || (cfg.Kind == "ssh" && !job.Automatic) {
		if err := s.event(ctx, job, "deploying", receipt.Target, "Uploading certificate"); err != nil {
			return err
		}
		remoteID, err := s.Upstream.Upload(ctx, cfg, upstream.Bundle{Name: "certcenter-" + cert.Fingerprint[:32], Fingerprint: cert.Fingerprint, Certificate: material.Certificate, PrivateKey: material.PrivateKey})
		if err != nil {
			return fmt.Errorf("upload certificate: %w", err)
		}
		if remoteID == "" {
			return errors.New("upstream returned an empty certificate ID")
		}
		receipt.RemoteID = remoteID
		if err := s.Store.SaveDeployment(ctx, receipt); err != nil {
			return fmt.Errorf("save upload receipt: %w", err)
		}
		if err := s.event(ctx, job, "uploaded", receipt.Target, "Upload completed: "+remoteID); err != nil {
			return err
		}
	}
	if deploymentComplete(receipt, cfg) {
		return nil
	}
	if err := s.updateResources(ctx, job, cfg, cert, receipt); err != nil {
		return err
	}
	receipt.ResourcesUpdated, receipt.OldDeleted = true, cfg.DeleteOld
	if err := s.Store.SaveDeployment(ctx, receipt); err != nil {
		return fmt.Errorf("save completed deployment: %w", err)
	}
	return s.event(ctx, job, "deployed", receipt.Target, "Cloud resources updated: "+receipt.RemoteID)
}

func (s *Service) updateResources(ctx context.Context, job store.CertificateJob, cfg certconfig.Upstream, cert store.Certificate, receipt store.Deployment) error {
	updater, ok := s.Upstream.(upstream.ResourceUpdater)
	if !ok {
		return errors.New("upstream does not support cloud resource updates")
	}
	previous, err := s.Store.PreviousDeployments(ctx, cert, receipt.Target, receipt.Revision)
	if err != nil {
		return fmt.Errorf("load previous cloud certificates: %w", err)
	}
	if err := checkReplacementNames(cert, receipt.RemoteID, previous); err != nil {
		return err
	}
	updates, err := s.Store.CloudUpdates(ctx, cert.ID)
	if err != nil {
		return fmt.Errorf("load cloud update checkpoints: %w", err)
	}
	seen := map[string]bool{receipt.RemoteID: true}
	for _, old := range previous {
		if seen[old.RemoteID] {
			continue
		}
		seen[old.RemoteID] = true
		checkpoint := store.CloudUpdate{CertificateID: cert.ID, Target: receipt.Target, Revision: receipt.Revision, OldRemoteID: old.RemoteID}
		for _, saved := range updates {
			if saved.Target == receipt.Target && saved.Revision == receipt.Revision && saved.OldRemoteID == old.RemoteID {
				checkpoint = saved
				break
			}
		}
		progress := upstream.UpdateProgress{}
		if len(checkpoint.State) != 0 {
			if err := json.Unmarshal(checkpoint.State, &progress); err != nil {
				return errors.New("invalid cloud update checkpoint")
			}
		}
		if progress.Complete(cfg.DeleteOld) {
			continue
		}
		before := progress.Stage
		progress, updateErr := updater.Advance(ctx, cfg, old.RemoteID, receipt.RemoteID, progress)
		checkpoint.State, err = json.Marshal(progress)
		if err != nil {
			return fmt.Errorf("encode cloud update checkpoint: %w", err)
		}
		checkpoint.Deleted = progress.Stage == "deleted"
		if err := s.Store.SaveCloudUpdate(ctx, checkpoint); err != nil {
			return fmt.Errorf("save cloud update checkpoint: %w", err)
		}
		if before != progress.Stage {
			if err := s.event(ctx, job, "cloud_update", receipt.Target, "Cloud replacement "+old.RemoteID+" -> "+receipt.RemoteID+": "+progress.Stage); err != nil {
				return err
			}
		}
		if updateErr != nil {
			return fmt.Errorf("update cloud resources: %w", updateErr)
		}
		return errCloudPending
	}
	return nil
}

func checkReplacementNames(cert store.Certificate, remoteID string, previous []store.DeployedCertificate) error {
	leaf := x509.Certificate{DNSNames: cert.Names}
	for _, old := range previous {
		if old.RemoteID == remoteID {
			continue
		}
		var missing []string
		for _, name := range old.Names {
			if err := leaf.VerifyHostname(name); err != nil {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("cannot replace cloud certificate %s with %s: new certificate does not cover %s", old.RemoteID, remoteID, strings.Join(missing, ", "))
		}
	}
	return nil
}
