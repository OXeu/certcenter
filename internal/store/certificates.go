package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Certificate is an immutable issued version with encrypted PEM material.
type Certificate struct {
	JobID       *uint     `gorm:"uniqueIndex" json:"-"`
	ID          uint      `gorm:"primaryKey" json:"id"`
	Domain      string    `gorm:"index:cert_policy;size:253;not null" json:"domain"`
	PolicyID    string    `gorm:"index:cert_policy;size:63;not null" json:"policy_id"`
	Names       []string  `gorm:"serializer:json;type:text" json:"names"`
	Fingerprint string    `gorm:"size:64;not null" json:"fingerprint"`
	Material    []byte    `gorm:"type:longblob;not null" json:"-"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `gorm:"index" json:"not_after"`
	CreatedAt   time.Time `json:"created_at"`
}

// ACMEAccount keeps the encrypted account key stable across renewals and restarts.
type ACMEAccount struct {
	ID       string `gorm:"primaryKey;size:64"`
	Material []byte `gorm:"type:longblob;not null"`
}

// CertificateJob is a durable queue entry; ActiveKey prevents overlapping policy jobs.
type CertificateJob struct {
	Automatic  bool      `json:"automatic"`
	ID         uint      `gorm:"primaryKey" json:"id"`
	Domain     string    `gorm:"index;size:253;not null" json:"domain"`
	PolicyID   string    `gorm:"size:63;not null" json:"policy_id"`
	Action     string    `gorm:"size:16;not null" json:"action"`
	Status     string    `gorm:"index;size:16;not null" json:"status"`
	ActiveKey  *string   `gorm:"uniqueIndex;size:64" json:"-"`
	Worker     string    `gorm:"size:64" json:"-"`
	LeaseUntil time.Time `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CertificateEvent is a credential-free issuance or deployment audit entry.
type CertificateEvent struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	JobID     uint      `gorm:"index;not null" json:"job_id"`
	Domain    string    `gorm:"index;size:253;not null" json:"domain"`
	PolicyID  string    `gorm:"size:63;not null" json:"policy_id"`
	Stage     string    `gorm:"size:32" json:"stage"`
	Target    string    `gorm:"size:63" json:"target"`
	Message   string    `gorm:"type:text" json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// Deployment records successful upstream uploads for retry deduplication.
type Deployment struct {
	Revision      string    `gorm:"uniqueIndex:deployment;size:64;not null" json:"-"`
	ID            uint      `gorm:"primaryKey" json:"id"`
	CertificateID uint      `gorm:"uniqueIndex:deployment;not null" json:"certificate_id"`
	Target        string    `gorm:"uniqueIndex:deployment;size:63;not null" json:"target"`
	RemoteID      string    `gorm:"size:255" json:"remote_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// CertificateForJob recovers a version saved before a worker stopped.
func (s *Store) CertificateForJob(ctx context.Context, jobID uint) (Certificate, error) {
	var cert Certificate
	err := s.db.WithContext(ctx).Where("job_id = ?", jobID).First(&cert).Error
	return cert, queryErr("recover issued certificate", err)
}

// ErrBusy means another operation already owns this certificate policy.
var ErrBusy = errors.New("certificate operation already queued or running")

// SaveCertificate stores a new immutable certificate version.
func (s *Store) SaveCertificate(ctx context.Context, cert Certificate) (Certificate, error) {
	err := s.db.WithContext(ctx).Create(&cert).Error
	return cert, queryErr("save certificate", err)
}

// Certificates lists all issued versions for a domain without encrypted material.
func (s *Store) Certificates(ctx context.Context, domain string) ([]Certificate, error) {
	out := []Certificate{}
	err := s.db.WithContext(ctx).Omit("material").Where("domain = ?", domain).Order("id DESC").Find(&out).Error
	return out, queryErr("list certificates", err)
}

// LatestCertificate loads the most recent version for a domain and policy.
func (s *Store) LatestCertificate(ctx context.Context, domain, policy string) (Certificate, error) {
	var cert Certificate
	err := s.db.WithContext(ctx).Where("domain = ? AND policy_id = ?", domain, policy).Order("id DESC").First(&cert).Error
	return cert, queryErr("latest certificate", err)
}

// Account loads an encrypted ACME account by authority and email hash.
func (s *Store) Account(ctx context.Context, id string) (ACMEAccount, error) {
	var account ACMEAccount
	err := s.db.WithContext(ctx).First(&account, "id = ?", id).Error
	return account, queryErr("load ACME account", err)
}

// SaveAccount inserts an account key once; concurrent callers read the winning key.
func (s *Store) SaveAccount(ctx context.Context, account ACMEAccount) error {
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&account).Error
	return queryErr("save ACME account", err)
}

// EnqueueCertificate creates a job protected by a unique active policy key.
func (s *Store) EnqueueCertificate(ctx context.Context, job CertificateJob) (CertificateJob, error) {
	job.Status = "queued"
	err := s.db.WithContext(ctx).Create(&job).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return job, ErrBusy
	}
	return job, queryErr("queue certificate operation", err)
}

// ClaimCertificateJob atomically leases one queued job or one whose worker stopped.
func (s *Store) ClaimCertificateJob(ctx context.Context, worker string, now time.Time) (CertificateJob, error) {
	var job CertificateJob
	err := s.db.WithContext(ctx).Where("status = ? OR (status = ? AND lease_until < ?)", "queued", "running", now).Order("id").First(&job).Error
	if err != nil {
		return job, queryErr("find queued operation", err)
	}
	result := s.db.WithContext(ctx).Model(&CertificateJob{}).Where("id = ? AND (status = ? OR (status = ? AND lease_until < ?))", job.ID, "queued", "running", now).
		Updates(map[string]any{"status": "running", "worker": worker, "lease_until": now.Add(20 * time.Minute)})
	if result.Error != nil {
		return job, queryErr("claim operation", result.Error)
	}
	if result.RowsAffected == 0 {
		return job, ErrBusy
	}
	job.Status, job.Worker = "running", worker
	return job, nil
}

// FinishCertificateJob releases an operation lease and records its terminal state.
func (s *Store) FinishCertificateJob(ctx context.Context, job CertificateJob, status string) error {
	res := s.db.WithContext(ctx).Model(&CertificateJob{}).Where("id = ? AND worker = ? AND status = ?", job.ID, job.Worker, "running").Updates(map[string]any{"status": status, "active_key": nil})
	return s.mustAffect(ctx, res, "finish operation")
}

// CertificateJobs returns the most recent operations for a domain.
func (s *Store) CertificateJobs(ctx context.Context, domain string) ([]CertificateJob, error) {
	out := []CertificateJob{}
	err := s.db.WithContext(ctx).Where("domain = ?", domain).Order("id DESC").Limit(100).Find(&out).Error
	return out, queryErr("list operations", err)
}

// LastCertificateJob returns a policy's latest operation for retry backoff.
func (s *Store) LastCertificateJob(ctx context.Context, domain, policy string) (CertificateJob, error) {
	var job CertificateJob
	err := s.db.WithContext(ctx).Where("domain = ? AND policy_id = ?", domain, policy).Order("id DESC").First(&job).Error
	return job, queryErr("last operation", err)
}

// AddCertificateEvent appends an audit entry.
func (s *Store) AddCertificateEvent(ctx context.Context, event CertificateEvent) error {
	return queryErr("write certificate log", s.db.WithContext(ctx).Create(&event).Error)
}

// CertificateEvents returns recent audit entries for a domain.
func (s *Store) CertificateEvents(ctx context.Context, domain string) ([]CertificateEvent, error) {
	out := []CertificateEvent{}
	err := s.db.WithContext(ctx).Where("domain = ?", domain).Order("id DESC").Limit(500).Find(&out).Error
	return out, queryErr("read certificate logs", err)
}

// Deployments lists successful uploads of one certificate version.
func (s *Store) Deployments(ctx context.Context, certificateID uint) ([]Deployment, error) {
	out := []Deployment{}
	err := s.db.WithContext(ctx).Where("certificate_id = ?", certificateID).Find(&out).Error
	return out, queryErr("list deployments", err)
}

// SaveDeployment persists a successful upload without overwriting existing receipts.
func (s *Store) SaveDeployment(ctx context.Context, deployment Deployment) error {
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&deployment).Error
	if err != nil {
		return fmt.Errorf("save deployment: %w", err)
	}
	return nil
}
