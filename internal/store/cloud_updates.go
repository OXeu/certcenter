package store

import (
	"context"
	"time"

	"gorm.io/gorm/clause"
)

// CloudUpdate records progress from one managed remote certificate to its replacement.
type CloudUpdate struct {
	ID            uint   `gorm:"primaryKey"`
	CertificateID uint   `gorm:"uniqueIndex:cloud_update;not null"`
	Target        string `gorm:"uniqueIndex:cloud_update;size:63;not null"`
	Revision      string `gorm:"uniqueIndex:cloud_update;size:64;not null"`
	OldRemoteID   string `gorm:"uniqueIndex:cloud_update;size:255;not null"`
	State         []byte `gorm:"type:longblob"`
	Deleted       bool   `gorm:"not null;default:false"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// DeployedCertificate identifies a previously uploaded certificate in a policy.
type DeployedCertificate struct {
	CertificateID uint
	RemoteID      string
	Names         []string `gorm:"serializer:json"`
}

// PreviousDeployments restricts replacements to earlier uploads in the same policy and destination.
func (s *Store) PreviousDeployments(ctx context.Context, cert Certificate, target, revision string) ([]DeployedCertificate, error) {
	out := []DeployedCertificate{}
	err := s.db.WithContext(ctx).Table("deployments AS d").
		Select("d.certificate_id, d.remote_id, c.names").
		Joins("JOIN certificates AS c ON c.id = d.certificate_id").
		Where("c.domain = ? AND c.policy_id = ? AND c.id < ? AND d.target = ? AND d.revision = ? AND d.remote_id <> ''", cert.Domain, cert.PolicyID, cert.ID, target, revision).
		Where("NOT EXISTS (SELECT 1 FROM cloud_updates AS u WHERE u.target = d.target AND u.revision = d.revision AND u.old_remote_id = d.remote_id AND u.deleted = ?)", true).
		Order("c.id DESC").Find(&out).Error
	return out, queryErr("list previous deployments", err)
}

// CloudUpdates returns persisted resource replacement checkpoints for a version.
func (s *Store) CloudUpdates(ctx context.Context, certificateID uint) ([]CloudUpdate, error) {
	out := []CloudUpdate{}
	err := s.db.WithContext(ctx).Where("certificate_id = ?", certificateID).Find(&out).Error
	return out, queryErr("load cloud update progress", err)
}

// SaveCloudUpdate saves a checkpoint before another cloud mutation can run.
func (s *Store) SaveCloudUpdate(ctx context.Context, update CloudUpdate) error {
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "certificate_id"}, {Name: "target"}, {Name: "revision"}, {Name: "old_remote_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"state", "deleted", "updated_at"}),
	}).Create(&update).Error
	return queryErr("save cloud update progress", err)
}

// DeferCertificateJob releases a worker while retaining the policy lock for asynchronous work.
func (s *Store) DeferCertificateJob(ctx context.Context, job CertificateJob, next time.Time) error {
	res := s.db.WithContext(ctx).Model(&CertificateJob{}).
		Where("id = ? AND worker = ? AND status = ?", job.ID, job.Worker, "running").
		Updates(map[string]any{"status": "queued", "worker": "", "lease_until": nil, "available_at": next})
	return s.mustAffect(ctx, res, "defer certificate operation")
}
