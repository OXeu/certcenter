package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm/clause"
)

// DNSDelegation binds a managed domain and certificate name to a stable random DNS label.
type DNSDelegation struct {
	ID    string `gorm:"primaryKey;size:64" json:"-"`
	Scope string `gorm:"size:253;not null" json:"-"`
	Name  string `gorm:"size:253;not null" json:"-"`
	Label string `gorm:"size:32;uniqueIndex;not null" json:"-"`
}

// DNSChallenge keeps concurrent ACME TXT values separate until cleanup or expiry.
type DNSChallenge struct {
	DelegationID string    `gorm:"primaryKey;size:64"`
	Value        string    `gorm:"primaryKey;size:64"`
	ExpiresAt    time.Time `gorm:"index;not null"`
}

// EnsureDNSDelegation creates a delegation once and returns the winning stable label.
func (s *Store) EnsureDNSDelegation(ctx context.Context, d DNSDelegation) (DNSDelegation, error) {
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&d).Error; err != nil {
		return DNSDelegation{}, fmt.Errorf("create DNS delegation: %w", err)
	}
	return s.DNSDelegation(ctx, d.ID)
}

// DNSDelegation loads a delegation by its scoped identifier.
func (s *Store) DNSDelegation(ctx context.Context, id string) (DNSDelegation, error) {
	var d DNSDelegation
	err := s.db.WithContext(ctx).First(&d, "id = ?", id).Error
	return d, queryErr("load DNS delegation", err)
}

// DNSDelegationByLabel resolves a public random label without exposing domain ownership.
func (s *Store) DNSDelegationByLabel(ctx context.Context, label string) (DNSDelegation, error) {
	var d DNSDelegation
	err := s.db.WithContext(ctx).First(&d, "label = ?", label).Error
	return d, queryErr("resolve DNS delegation", err)
}

// PutDNSChallenge publishes one expiring TXT value and removes expired values for its delegation.
func (s *Store) PutDNSChallenge(ctx context.Context, value DNSChallenge) error {
	return s.Transaction(ctx, func(tx *Store) error {
		if err := tx.db.WithContext(ctx).Where("delegation_id = ? AND expires_at <= ?", value.DelegationID, time.Now().UTC()).Delete(&DNSChallenge{}).Error; err != nil {
			return fmt.Errorf("expire DNS challenges: %w", err)
		}
		if err := tx.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "delegation_id"}, {Name: "value"}}, DoUpdates: clause.AssignmentColumns([]string{"expires_at"})}).Create(&value).Error; err != nil {
			return fmt.Errorf("publish DNS challenge: %w", err)
		}
		return nil
	})
}

// DNSChallengeValues returns only unexpired values for the requested delegation.
func (s *Store) DNSChallengeValues(ctx context.Context, id string) ([]string, error) {
	values := []string{}
	if err := s.db.WithContext(ctx).Model(&DNSChallenge{}).Where("delegation_id = ? AND expires_at > ?", id, time.Now().UTC()).Order("value").Pluck("value", &values).Error; err != nil {
		return nil, fmt.Errorf("read DNS challenges: %w", err)
	}
	return values, nil
}

// DeleteDNSChallenge removes only the value owned by this challenge.
func (s *Store) DeleteDNSChallenge(ctx context.Context, id, value string) error {
	if err := s.db.WithContext(ctx).Where("delegation_id = ? AND value = ?", id, value).Delete(&DNSChallenge{}).Error; err != nil {
		return fmt.Errorf("remove DNS challenge: %w", err)
	}
	return nil
}
