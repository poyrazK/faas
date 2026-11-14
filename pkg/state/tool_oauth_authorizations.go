package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// The verifier, callback URI and nonce are sealed together. Only pending flows
// retain this envelope. Consuming it never authorizes a second code exchange.
type ToolOAuthAuthorization struct {
	AccountID    string
	ConnectionID string
	StateSHA256  string
	Phase        string
	ExpiresAt    time.Time
	Ciphertext   []byte `json:"-"`
}

func cloneToolAuthorization(a ToolOAuthAuthorization) ToolOAuthAuthorization {
	a.Ciphertext = append([]byte(nil), a.Ciphertext...)
	return a
}

func validToolAuthorization(c ToolOAuthConnection, a ToolOAuthAuthorization, now time.Time) bool {
	return c.Record.Intent.Guided && c.Record.RevokedAt == nil && c.Record.Generation == 0 && c.Record.State == "authorization_pending" && len(c.Ciphertext) == 0 && a.AccountID == c.Record.AccountID && a.ConnectionID == c.Record.ID && a.Phase == "pending" && toolpublication.ValidID(a.StateSHA256) && a.ExpiresAt.After(now) && a.ExpiresAt.Sub(now) <= api.ToolOAuthAuthorizationLifetime && len(a.Ciphertext) > 0 && len(a.Ciphertext) <= api.ToolOAuthAuthorizationCiphertextMaxBytes
}

func consumeToolAuthorization(c ToolOAuthConnection, hash string, now time.Time) (ToolOAuthConnection, error) {
	a := c.Authorization
	if !c.Record.Intent.Guided || c.Record.RevokedAt != nil || c.Record.Generation != 0 || a == nil {
		return c, ErrNotFound
	}
	if c.Record.State != "authorization_pending" || a.Phase != "pending" || !toolpublication.ValidID(hash) || a.StateSHA256 != hash {
		return c, ErrConflict
	}
	a.Ciphertext = nil
	if !now.Before(a.ExpiresAt) {
		a.Phase = "failed"
		c.Record.State = "reconnect_required"
		return c, ErrToolOAuthReconnect
	}
	a.Phase = "exchanging"
	c.Record.State = "refreshing"
	c.LeaseID = uuid.NewString()
	until := now.Add(api.ToolOAuthRenewalLease)
	c.LeaseUntil = &until
	return c, nil
}

func finishToolAuthorization(c ToolOAuthConnection, lease string, ciphertext []byte, expiry *time.Time, failure string, now time.Time) (ToolOAuthConnection, error) {
	if !c.Record.Intent.Guided || c.Record.Generation != 0 || c.Authorization == nil || c.Authorization.Phase != "exchanging" {
		return c, ErrConflict
	}
	out, err := finishToolOAuth(c, lease, ciphertext, expiry, failure, now)
	if err != nil {
		return out, err
	}
	out.Authorization.Phase = "completed"
	if failure != "" {
		out.Authorization.Phase = "failed"
	}
	out.Authorization.Ciphertext = nil
	return out, nil
}

func (m *MemStore) GetToolOAuthAuthorization(_ context.Context, account, id string) (ToolOAuthAuthorization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.toolOAuthConnections[account+":"+id]
	if !ok || c.Authorization == nil {
		return ToolOAuthAuthorization{}, ErrNotFound
	}
	return cloneToolAuthorization(*c.Authorization), nil
}

func (m *MemStore) BeginToolOAuthAuthorization(_ context.Context, account, id string, a ToolOAuthAuthorization) (ToolOAuthAuthorization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.toolOAuthConnections[account+":"+id]
	if !ok {
		return ToolOAuthAuthorization{}, ErrNotFound
	}
	if err := m.toolOAuthAuthorityLocked(c.Record); err != nil {
		return ToolOAuthAuthorization{}, err
	}
	if !validToolAuthorization(c, a, time.Now()) {
		return ToolOAuthAuthorization{}, ErrConflict
	}
	if c.Authorization != nil {
		if c.Authorization.Phase != "pending" || !time.Now().Before(c.Authorization.ExpiresAt) {
			return ToolOAuthAuthorization{}, ErrToolOAuthReconnect
		}
		return cloneToolAuthorization(*c.Authorization), nil
	}
	x := cloneToolAuthorization(a)
	c.Authorization = &x
	m.toolOAuthConnections[account+":"+id] = c
	return cloneToolAuthorization(x), nil
}

func (m *MemStore) ConsumeToolOAuthAuthorization(_ context.Context, account, id, hash string) (ToolOAuthConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.toolOAuthConnections[account+":"+id]
	if !ok {
		return c, ErrNotFound
	}
	if err := m.toolOAuthAuthorityLocked(c.Record); err != nil {
		return c, err
	}
	out, err := consumeToolAuthorization(cloneToolOAuth(c), hash, time.Now().UTC())
	if err == nil || errors.Is(err, ErrToolOAuthReconnect) {
		m.toolOAuthConnections[account+":"+id] = out
	}
	return cloneToolOAuth(out), err
}

func (m *MemStore) FinishToolOAuthAuthorization(_ context.Context, account, id, lease string, ciphertext []byte, expiry *time.Time, failure string) (ToolOAuthConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.toolOAuthConnections[account+":"+id]
	if !ok {
		return c, ErrNotFound
	}
	if err := m.toolOAuthAuthorityLocked(c.Record); err != nil {
		return c, err
	}
	out, err := finishToolAuthorization(cloneToolOAuth(c), lease, ciphertext, expiry, failure, time.Now().UTC())
	if err == nil {
		m.toolOAuthConnections[account+":"+id] = out
	}
	return cloneToolOAuth(out), err
}
