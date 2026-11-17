package store

import (
	"context"
	"crypto/subtle"
	"time"
)

// OAuthClient is a registered public MCP client.
type OAuthClient struct {
	ID           string   `gorm:"primaryKey;size:64"`
	Name         string   `gorm:"size:128"`
	RedirectURIs []string `gorm:"serializer:json;type:text"`
	CreatedAt    time.Time
}

// OAuthAuthorization binds browser consent and a single-use code to a PKCE request.
type OAuthAuthorization struct {
	ID          string    `gorm:"primaryKey;size:64"`
	ClientID    string    `gorm:"size:64;index"`
	UserID      uint      `gorm:"index"`
	SessionHash string    `gorm:"size:64"`
	RedirectURI string    `gorm:"type:text"`
	Resource    string    `gorm:"type:text"`
	Scope       string    `gorm:"size:64"`
	State       string    `gorm:"type:text"`
	Challenge   string    `gorm:"size:128"`
	CodeHash    string    `gorm:"size:64;index"`
	Status      string    `gorm:"size:16"`
	ExpiresAt   time.Time `gorm:"index"`
}

// OAuthToken stores only credential digests and their grant bindings.
type OAuthToken struct {
	AccessHash       string `gorm:"primaryKey;size:64"`
	RefreshHash      string `gorm:"uniqueIndex;size:64"`
	Family           string `gorm:"size:64;index"`
	ClientID         string `gorm:"size:64"`
	UserID           uint   `gorm:"index"`
	Resource         string `gorm:"type:text"`
	Scope            string `gorm:"size:64"`
	Used             bool
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time `gorm:"index"`
}

// CreateOAuthClient persists public client metadata.
func (s *Store) CreateOAuthClient(ctx context.Context, client OAuthClient) error {
	return queryErr("create oauth client", s.db.WithContext(ctx).Create(&client).Error)
}

// OAuthClientByID resolves registered metadata without trusting request redirects.
func (s *Store) OAuthClientByID(ctx context.Context, id string) (OAuthClient, error) {
	var client OAuthClient
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&client).Error
	if err == nil && client.ID != id {
		return OAuthClient{}, ErrNotFound
	}
	return client, queryErr("load oauth client", err)
}

// CreateOAuthAuthorization persists a browser-bound consent request.
func (s *Store) CreateOAuthAuthorization(ctx context.Context, request OAuthAuthorization) error {
	if err := s.PruneOAuth(ctx); err != nil {
		return err
	}
	return queryErr("create oauth authorization", s.db.WithContext(ctx).Create(&request).Error)
}

// OAuthAuthorizationByID loads an unexpired consent request for its browser session.
func (s *Store) OAuthAuthorizationByID(ctx context.Context, id string, userID uint, sessionHash string) (OAuthAuthorization, error) {
	var request OAuthAuthorization
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ? AND session_hash = ? AND status = ? AND expires_at > ?", id, userID, sessionHash, "pending", time.Now().UTC()).First(&request).Error
	return request, queryErr("load oauth authorization", err)
}

// DecideOAuthAuthorization applies one consent decision.
func (s *Store) DecideOAuthAuthorization(ctx context.Context, id, sessionHash, codeHash, status string) error {
	result := s.db.WithContext(ctx).Model(&OAuthAuthorization{}).Where("id = ? AND session_hash = ? AND status = ? AND expires_at > ?", id, sessionHash, "pending", time.Now().UTC()).Updates(map[string]any{"code_hash": codeHash, "status": status})
	return s.mustAffect(ctx, result, "decide oauth authorization")
}

// ExchangeOAuthCode consumes a bound code and creates a token atomically.
func (s *Store) ExchangeOAuthCode(ctx context.Context, codeHash, clientID, redirectURI, resource, challenge string, token *OAuthToken) error {
	replay := false
	err := s.Transaction(ctx, func(tx *Store) error {
		var request OAuthAuthorization
		result := tx.db.Where("code_hash = ? AND client_id = ? AND redirect_uri = ? AND resource = ? AND challenge = ? AND expires_at > ?", codeHash, clientID, redirectURI, resource, challenge, time.Now().UTC()).First(&request)
		if err := queryErr("load authorization code", result.Error); err != nil {
			return err
		}
		if request.ClientID != clientID || request.RedirectURI != redirectURI || request.Resource != resource || subtle.ConstantTimeCompare([]byte(request.Challenge), []byte(challenge)) != 1 {
			return ErrNotFound
		}
		if request.Status == "used" {
			replay = true
			return tx.RevokeOAuthFamily(ctx, request.ID)
		}
		result = tx.db.Model(&OAuthAuthorization{}).Where("id = ? AND status = ?", request.ID, "approved").Update("status", "used")
		if err := tx.mustAffect(ctx, result, "consume authorization code"); err != nil {
			return err
		}
		if _, err := tx.UserByID(ctx, request.UserID); err != nil {
			return err
		}
		token.Family, token.UserID, token.ClientID, token.Resource, token.Scope = request.ID, request.UserID, request.ClientID, request.Resource, request.Scope
		return tx.CreateOAuthToken(ctx, *token)
	})
	if err != nil {
		return err
	}
	if replay {
		return ErrNotFound
	}
	return nil
}

// CreateOAuthToken persists an issued token pair.
func (s *Store) CreateOAuthToken(ctx context.Context, token OAuthToken) error {
	return queryErr("create oauth token", s.db.WithContext(ctx).Create(&token).Error)
}

// OAuthAccessUser resolves an active, audience-bound token and the user's current role.
func (s *Store) OAuthAccessUser(ctx context.Context, hash, resource string) (User, error) {
	var token OAuthToken
	err := s.db.WithContext(ctx).Where("access_hash = ? AND resource = ? AND used = ? AND access_expires_at > ?", hash, resource, false, time.Now().UTC()).First(&token).Error
	if err := queryErr("load oauth access token", err); err != nil {
		return User{}, err
	}
	if token.Resource != resource {
		return User{}, ErrNotFound
	}
	return s.UserByID(ctx, token.UserID)
}

// RefreshOAuthToken rotates a refresh token and revokes its family on reuse.
func (s *Store) RefreshOAuthToken(ctx context.Context, hash, clientID, resource string, token *OAuthToken) error {
	replay := false
	err := s.Transaction(ctx, func(tx *Store) error {
		var previous OAuthToken
		err := tx.db.Where("refresh_hash = ? AND client_id = ? AND resource = ? AND refresh_expires_at > ?", hash, clientID, resource, time.Now().UTC()).First(&previous).Error
		if err := queryErr("load oauth refresh token", err); err != nil {
			return err
		}
		if previous.ClientID != clientID || previous.Resource != resource {
			return ErrNotFound
		}
		if previous.Used {
			replay = true
			return tx.RevokeOAuthFamily(ctx, previous.Family)
		}
		result := tx.db.Model(&OAuthToken{}).Where("access_hash = ? AND used = ?", previous.AccessHash, false).Update("used", true)
		if err := tx.mustAffect(ctx, result, "consume oauth refresh token"); err != nil {
			return err
		}
		if _, err := tx.UserByID(ctx, previous.UserID); err != nil {
			return err
		}
		token.Family, token.UserID, token.ClientID, token.Resource, token.Scope = previous.Family, previous.UserID, previous.ClientID, previous.Resource, previous.Scope
		token.RefreshExpiresAt = previous.RefreshExpiresAt
		return tx.CreateOAuthToken(ctx, *token)
	})
	if err != nil {
		return err
	}
	if replay {
		return ErrNotFound
	}
	return nil
}

// RevokeOAuthFamily removes all credentials issued by one consent grant.
func (s *Store) RevokeOAuthFamily(ctx context.Context, family string) error {
	return queryErr("revoke oauth grant", s.db.WithContext(ctx).Where("family = ?", family).Delete(&OAuthToken{}).Error)
}

// RevokeOAuthToken revokes a caller-bound access or refresh token without revealing its validity.
func (s *Store) RevokeOAuthToken(ctx context.Context, hash, clientID string) error {
	var token OAuthToken
	result := s.db.WithContext(ctx).Where("client_id = ? AND (access_hash = ? OR refresh_hash = ?)", clientID, hash, hash).Limit(1).Find(&token)
	if err := queryErr("find oauth token", result.Error); err != nil {
		return err
	}
	if result.RowsAffected == 0 || token.ClientID != clientID {
		return nil
	}
	return s.RevokeOAuthFamily(ctx, token.Family)
}

// ConsumeOAuthLogin retrieves a single-use login continuation from its opaque cookie.
func (s *Store) ConsumeOAuthLogin(ctx context.Context, id string) (OAuthAuthorization, error) {
	var request OAuthAuthorization
	err := s.Transaction(ctx, func(tx *Store) error {
		result := tx.db.Where("id = ? AND status = ? AND expires_at > ?", id, "login", time.Now().UTC()).First(&request)
		if err := queryErr("load oauth login", result.Error); err != nil {
			return err
		}
		if request.ID != id {
			return ErrNotFound
		}
		return tx.mustAffect(ctx, tx.db.Where("id = ? AND status = ?", id, "login").Delete(&OAuthAuthorization{}), "consume oauth login")
	})
	return request, err
}

// PruneOAuth removes expired authorizations and refresh token histories.
func (s *Store) PruneOAuth(ctx context.Context) error {
	if err := queryErr("prune oauth authorizations", s.db.WithContext(ctx).Where("expires_at <= ?", time.Now().UTC()).Delete(&OAuthAuthorization{}).Error); err != nil {
		return err
	}
	return queryErr("prune oauth tokens", s.db.WithContext(ctx).Where("refresh_expires_at <= ?", time.Now().UTC()).Delete(&OAuthToken{}).Error)
}
