package state

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolconnection"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var ErrToolOAuthBusy = errors.New("execution connection renewal is in progress")
var ErrToolOAuthReconnect = errors.New("execution connection requires reconnection")

// Only the API-owned worker receives this internal encrypted state. Public APIs
// project Record and never serialize ciphertext or lease capabilities.
type ToolOAuthConnection struct {
	Record        api.ToolOAuthConnectionRecord
	Ciphertext    []byte                  `json:"-"`
	LeaseID       string                  `json:"-"`
	LeaseUntil    *time.Time              `json:"-"`
	Authorization *ToolOAuthAuthorization `json:"-"`
}

type ToolOAuthConnectionClaim struct {
	Connection ToolOAuthConnection
	Fresh      bool
}

type ToolOAuthConnectionStore interface {
	PutToolOAuthConnection(context.Context, api.ToolOAuthConnectionRecord) (ToolOAuthConnection, error)
	GetToolOAuthConnection(context.Context, string, string) (ToolOAuthConnection, error)
	ListToolOAuthConnections(context.Context, string, string, int) ([]ToolOAuthConnection, error)
	RevokeToolOAuthConnection(context.Context, string, string) (ToolOAuthConnection, error)
	BeginToolOAuthRenewal(context.Context, string, string, api.ToolInvocationDelegation) (ToolOAuthConnectionClaim, error)
	FinishToolOAuthRenewal(context.Context, string, string, string, []byte, *time.Time, string) (ToolOAuthConnection, error)
	GetToolOAuthAuthorization(context.Context, string, string) (ToolOAuthAuthorization, error)
	BeginToolOAuthAuthorization(context.Context, string, string, ToolOAuthAuthorization) (ToolOAuthAuthorization, error)
	ConsumeToolOAuthAuthorization(context.Context, string, string, string) (ToolOAuthConnection, error)
	FinishToolOAuthAuthorization(context.Context, string, string, string, []byte, *time.Time, string) (ToolOAuthConnection, error)
}

type ToolOAuthConnectionLimitError struct{ Limit, Observed int64 }

func (e *ToolOAuthConnectionLimitError) Error() string {
	return "execution connection capacity reached"
}

func cloneToolOAuth(c ToolOAuthConnection) ToolOAuthConnection {
	if c.Authorization != nil {
		a := cloneToolAuthorization(*c.Authorization)
		c.Authorization = &a
	}
	c.Record.Intent.Scopes = append([]string{}, c.Record.Intent.Scopes...)
	c.Ciphertext = append([]byte(nil), c.Ciphertext...)
	if c.Record.ExpiresAt != nil {
		x := *c.Record.ExpiresAt
		c.Record.ExpiresAt = &x
	}
	if c.Record.RevokedAt != nil {
		x := *c.Record.RevokedAt
		c.Record.RevokedAt = &x
	}
	if c.LeaseUntil != nil {
		x := *c.LeaseUntil
		c.LeaseUntil = &x
	}
	return c
}

func (m *MemStore) PutToolOAuthConnection(_ context.Context, r api.ToolOAuthConnectionRecord) (ToolOAuthConnection, error) {
	if toolconnection.ValidateRecord(r) != nil || r.RevokedAt != nil {
		return ToolOAuthConnection{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.toolOAuthConnections == nil {
		m.toolOAuthConnections = map[string]ToolOAuthConnection{}
	}
	key := r.AccountID + ":" + r.ID
	if old, ok := m.toolOAuthConnections[key]; ok {
		if old.Record.BindingSHA256 != r.BindingSHA256 {
			return old, ErrConflict
		}
		return cloneToolOAuth(old), nil
	}
	var total, perApp int64
	for _, old := range m.toolOAuthConnections {
		if old.Record.AccountID == r.AccountID {
			total++
			if old.Record.AppID == r.AppID {
				perApp++
			}
		}
	}
	if total >= api.ToolOAuthConnectionsPerAccount {
		return ToolOAuthConnection{}, &ToolOAuthConnectionLimitError{api.ToolOAuthConnectionsPerAccount, total + 1}
	}
	if perApp >= api.ToolOAuthConnectionsPerApp {
		return ToolOAuthConnection{}, &ToolOAuthConnectionLimitError{api.ToolOAuthConnectionsPerApp, perApp + 1}
	}
	if err := m.toolOAuthAuthorityLocked(r); err != nil {
		return ToolOAuthConnection{}, err
	}
	r.State = "refresh_required"
	if r.Intent.Guided {
		r.State = "authorization_pending"
	}
	r.Generation = 0
	r.ExpiresAt = nil
	r.RecoveryAction = ""
	c := cloneToolOAuth(ToolOAuthConnection{Record: r})
	m.toolOAuthConnections[key] = c
	return cloneToolOAuth(c), nil
}

func (m *MemStore) GetToolOAuthConnection(_ context.Context, account, id string) (ToolOAuthConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.toolOAuthConnections[account+":"+id]
	if !ok {
		return c, ErrNotFound
	}
	return cloneToolOAuth(c), nil
}
func (m *MemStore) ListToolOAuthConnections(_ context.Context, account, cursor string, limit int) ([]ToolOAuthConnection, error) {
	if limit < 1 || limit > api.ToolOAuthConnectionPageMax+1 || cursor != "" && !toolgrant.ValidID(cursor) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ToolOAuthConnection{}
	for _, c := range m.toolOAuthConnections {
		if c.Record.AccountID == account && c.Record.ID > cursor {
			out = append(out, cloneToolOAuth(c))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Record.ID < out[j].Record.ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemStore) RevokeToolOAuthConnection(_ context.Context, account, id string) (ToolOAuthConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.toolOAuthConnections[account+":"+id]
	if !ok {
		return c, ErrNotFound
	}
	if c.Record.RevokedAt == nil {
		now := time.Now().UTC()
		c.Record.RevokedAt = &now
	}
	c.Record.State = "revoked"
	c.Record.ExpiresAt = nil
	c.LeaseID = ""
	c.LeaseUntil = nil
	c.Ciphertext = nil
	if c.Authorization != nil {
		c.Authorization.Phase = "failed"
		c.Authorization.Ciphertext = nil
	}
	m.toolOAuthConnections[account+":"+id] = c
	return cloneToolOAuth(c), nil
}

func (m *MemStore) toolOAuthAuthorityLocked(r api.ToolOAuthConnectionRecord) error {
	a, ok := m.apps[r.AppID]
	if !ok || a.AccountID != r.AccountID || a.Slug != r.Intent.App || a.ProjectID != r.ProjectID || !m.delegatedPersonalOrgLocked(r.AccountID, a.OrgID) || toolconnection.ValidateRecord(r) != nil {
		return ErrNotFound
	}
	for _, source := range []struct{ ref, digest string }{{r.Intent.RefreshTokenSecretRef, r.SourceSHA256}, {r.Intent.ClientSecretRef, r.ClientSourceSHA256}} {
		if source.ref == "" {
			continue
		}
		s, ok := m.secrets[secretKey{AppID: r.AppID, Scope: r.Intent.Scope, Key: strings.TrimPrefix(source.ref, api.SecretRefPrefix)}]
		if !ok || s.AccountID != r.AccountID || s.ManagedPostgresBindingID != "" || s.ManagedObjectStorageCredentialID != "" || s.ManagedCredentialRef != "" || toolpublication.Digest(s.Ciphertext) != source.digest {
			return ErrNotFound
		}
	}
	return nil
}

func (m *MemStore) toolOAuthGrantLocked(g api.ToolGrantRecord, ready bool) error {
	c, ok := m.toolOAuthConnections[g.AccountID+":"+g.Intent.CredentialConnectionID]
	if !ok || c.Record.RevokedAt != nil || c.Record.BindingSHA256 != g.Credential.SourceSHA256 || c.Record.AppID != g.AppID || c.Record.ProjectID != g.ProjectID || c.Record.Intent.Scope != g.Credential.Scope || !toolconnection.Covers(c.Record.Intent.Scopes, g.Credential.RequiredScopes) || c.Record.Intent.Guided && c.Record.Generation == 0 {
		return ErrNotFound
	}
	if err := m.toolOAuthAuthorityLocked(c.Record); err != nil {
		return err
	}
	if ready && !toolOAuthUsable(c, time.Now()) {
		return ErrToolOAuthReconnect
	}
	return nil
}

func toolOAuthUsable(c ToolOAuthConnection, now time.Time) bool {
	return c.Record.RevokedAt == nil && (c.Record.State == "ready" || c.Record.State == "refreshing") && len(c.Ciphertext) > 0 && c.Record.ExpiresAt != nil && c.Record.ExpiresAt.After(now)
}

func claimToolOAuth(c ToolOAuthConnection, now time.Time) (ToolOAuthConnectionClaim, error) {
	if c.Record.RevokedAt != nil {
		return ToolOAuthConnectionClaim{}, ErrNotFound
	}
	if c.Record.Intent.Guided && c.Record.Generation == 0 {
		return ToolOAuthConnectionClaim{}, ErrToolOAuthReconnect
	}
	if c.Record.State == "reconnect_required" {
		return ToolOAuthConnectionClaim{}, ErrToolOAuthReconnect
	}
	if c.Record.State == "refreshing" {
		if c.LeaseUntil != nil && now.Before(*c.LeaseUntil) {
			return ToolOAuthConnectionClaim{}, ErrToolOAuthBusy
		}
		c.Record.State = "reconnect_required"
		c.LeaseID = ""
		c.LeaseUntil = nil
		return ToolOAuthConnectionClaim{Connection: c}, ErrToolOAuthReconnect
	}
	if toolOAuthUsable(c, now.Add(api.ToolOAuthRefreshSkew)) {
		return ToolOAuthConnectionClaim{Connection: c}, nil
	}
	c.Record.State = "refreshing"
	c.LeaseID = uuid.NewString()
	until := now.Add(api.ToolOAuthRenewalLease)
	c.LeaseUntil = &until
	return ToolOAuthConnectionClaim{Connection: c, Fresh: true}, nil
}

func (m *MemStore) BeginToolOAuthRenewal(_ context.Context, account, id string, d api.ToolInvocationDelegation) (ToolOAuthConnectionClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, err := m.delegatedGrantLocked(account, d)
	if err != nil || g.Intent.CredentialConnectionID != id {
		if err == nil {
			err = ErrNotFound
		}
		return ToolOAuthConnectionClaim{}, err
	}
	c := m.toolOAuthConnections[account+":"+id]
	out, err := claimToolOAuth(c, time.Now().UTC())
	if out.Connection.Record.ID != "" {
		m.toolOAuthConnections[account+":"+id] = cloneToolOAuth(out.Connection)
	}
	out.Connection = cloneToolOAuth(out.Connection)
	return out, err
}

func finishToolOAuth(c ToolOAuthConnection, lease string, ciphertext []byte, expiry *time.Time, failure string, now time.Time) (ToolOAuthConnection, error) {
	if c.Record.RevokedAt != nil || c.Record.State != "refreshing" || c.LeaseID != lease || c.LeaseUntil == nil || !now.Before(*c.LeaseUntil) {
		return c, ErrConflict
	}
	if failure == "" {
		if len(ciphertext) == 0 || len(ciphertext) > api.ToolOAuthCiphertextMaxBytes || expiry == nil || !expiry.After(now) {
			return c, ErrInvalidArgument
		}
		c.Ciphertext = append([]byte(nil), ciphertext...)
		c.Record.ExpiresAt = expiry
		c.Record.Generation++
		c.Record.State = "ready"
	} else {
		if !slices.Contains([]string{"credential_unavailable", "renewal_uncertain", "provider_rejected", "scope_changed"}, failure) {
			return c, ErrInvalidArgument
		}
		c.Record.State = "reconnect_required"
	}
	c.LeaseID = ""
	c.LeaseUntil = nil
	return c, nil
}

func (m *MemStore) FinishToolOAuthRenewal(_ context.Context, account, id, lease string, ciphertext []byte, expiry *time.Time, failure string) (ToolOAuthConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.toolOAuthConnections[account+":"+id]
	if !ok {
		return c, ErrNotFound
	}
	if err := m.toolOAuthAuthorityLocked(c.Record); err != nil {
		return c, err
	}
	out, err := finishToolOAuth(c, lease, ciphertext, expiry, failure, time.Now().UTC())
	if err == nil {
		m.toolOAuthConnections[account+":"+id] = out
	}
	return cloneToolOAuth(out), err
}
