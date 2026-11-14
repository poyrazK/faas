package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/toolconnection"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func toolOAuthRow(row toolpublicationdb.ToolOauthConnection) (ToolOAuthConnection, error) {
	var c ToolOAuthConnection
	if err := json.Unmarshal(row.Payload, &c.Record); err != nil {
		return c, err
	}
	if toolconnection.ValidateRecord(c.Record) != nil {
		return c, ErrInvalidArgument
	}
	c.Record.State, c.Record.Generation = row.State, row.Generation
	c.Ciphertext = row.CredentialsCiphertext
	if row.ExpiresAt.Valid {
		c.Record.ExpiresAt = &row.ExpiresAt.Time
	}
	if row.RevokedAt.Valid {
		c.Record.RevokedAt = &row.RevokedAt.Time
	}
	if row.LeaseID.Valid {
		c.LeaseID = row.LeaseID.String()
	}
	if row.LeaseUntil.Valid {
		c.LeaseUntil = &row.LeaseUntil.Time
	}
	return c, nil
}

func toolOAuthTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}
func updateToolOAuth(ctx context.Context, q *toolpublicationdb.Queries, db toolpublicationdb.DBTX, c ToolOAuthConnection) error {
	lease := pgtype.UUID{}
	if c.LeaseID != "" {
		lease = mustPgUUID(c.LeaseID)
	}
	return q.UpdateToolOAuthConnection(ctx, db, toolpublicationdb.UpdateToolOAuthConnectionParams{AccountID: mustPgUUID(c.Record.AccountID), ID: mustPgUUID(c.Record.ID), State: c.Record.State, Generation: c.Record.Generation, CredentialsCiphertext: c.Ciphertext, ExpiresAt: toolOAuthTime(c.Record.ExpiresAt), LeaseID: lease, LeaseUntil: toolOAuthTime(c.LeaseUntil), RevokedAt: toolOAuthTime(c.Record.RevokedAt)})
}

func lockToolOAuthAuthority(ctx context.Context, q *toolpublicationdb.Queries, db toolpublicationdb.DBTX, r api.ToolOAuthConnectionRecord) error {
	if toolconnection.ValidateRecord(r) != nil {
		return ErrNotFound
	}
	if _, err := q.LockToolOAuthApp(ctx, db, toolpublicationdb.LockToolOAuthAppParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(r.AppID), Slug: r.Intent.App, ProjectID: r.ProjectID}); err != nil {
		return mapErr(err)
	}
	for _, source := range []struct{ ref, digest string }{{r.Intent.RefreshTokenSecretRef, r.SourceSHA256}, {r.Intent.ClientSecretRef, r.ClientSourceSHA256}} {
		if source.ref == "" {
			continue
		}
		cipher, err := q.LockToolGrantCredential(ctx, db, toolpublicationdb.LockToolGrantCredentialParams{AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), Scope: r.Intent.Scope, Key: strings.TrimPrefix(source.ref, api.SecretRefPrefix)})
		if err != nil {
			return mapErr(err)
		}
		if toolpublication.Digest(cipher) != source.digest {
			return ErrNotFound
		}
	}
	return nil
}

func checkToolOAuthGrant(g api.ToolGrantRecord, c ToolOAuthConnection, ready bool) error {
	r := c.Record
	if g.Credential == nil || g.Credential.ConnectionID != r.ID || r.AccountID != g.AccountID || r.RevokedAt != nil || r.BindingSHA256 != g.Credential.SourceSHA256 || r.AppID != g.AppID || r.ProjectID != g.ProjectID || r.Intent.Scope != g.Credential.Scope || !toolconnection.Covers(r.Intent.Scopes, g.Credential.RequiredScopes) || r.Intent.Guided && r.Generation == 0 {
		return ErrNotFound
	}
	if ready && !toolOAuthUsable(c, time.Now()) {
		return ErrToolOAuthReconnect
	}
	return nil
}

func (s *PgStore) PutToolOAuthConnection(ctx context.Context, r api.ToolOAuthConnectionRecord) (ToolOAuthConnection, error) {
	if toolconnection.ValidateRecord(r) != nil || r.RevokedAt != nil {
		return ToolOAuthConnection{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ToolOAuthConnection{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	if _, err = q.LockToolPublicationAccount(ctx, tx, mustPgUUID(r.AccountID)); err != nil {
		return ToolOAuthConnection{}, mapErr(err)
	}
	row, err := q.GetToolOAuthConnection(ctx, tx, toolpublicationdb.GetToolOAuthConnectionParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(r.ID)})
	if err == nil {
		old, e := toolOAuthRow(row)
		if e != nil {
			return old, e
		}
		if old.Record.BindingSHA256 != r.BindingSHA256 {
			return old, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(mapErr(err), ErrNotFound) {
		return ToolOAuthConnection{}, mapErr(err)
	}
	if err = lockToolOAuthAuthority(ctx, q, tx, r); err != nil {
		return ToolOAuthConnection{}, err
	}
	counts, err := q.CountToolOAuthConnections(ctx, tx, toolpublicationdb.CountToolOAuthConnectionsParams{AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID)})
	if err != nil {
		return ToolOAuthConnection{}, mapErr(err)
	}
	if counts.Total >= api.ToolOAuthConnectionsPerAccount {
		return ToolOAuthConnection{}, &ToolOAuthConnectionLimitError{api.ToolOAuthConnectionsPerAccount, counts.Total + 1}
	}
	if counts.PerApp >= api.ToolOAuthConnectionsPerApp {
		return ToolOAuthConnection{}, &ToolOAuthConnectionLimitError{api.ToolOAuthConnectionsPerApp, counts.PerApp + 1}
	}
	r.State = "refresh_required"
	if r.Intent.Guided {
		r.State = "authorization_pending"
	}
	r.Generation = 0
	r.ExpiresAt = nil
	r.RecoveryAction = ""
	data, err := json.Marshal(r)
	if err != nil {
		return ToolOAuthConnection{}, err
	}
	if err = q.InsertToolOAuthConnection(ctx, tx, toolpublicationdb.InsertToolOAuthConnectionParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(r.ID), AppID: mustPgUUID(r.AppID), Payload: data, State: r.State}); err != nil {
		return ToolOAuthConnection{}, mapErr(err)
	}
	return ToolOAuthConnection{Record: r}, tx.Commit(ctx)
}

func (s *PgStore) GetToolOAuthConnection(ctx context.Context, account, id string) (ToolOAuthConnection, error) {
	if !toolgrant.ValidID(id) {
		return ToolOAuthConnection{}, ErrInvalidArgument
	}
	row, err := toolpublicationdb.New().GetToolOAuthConnection(ctx, s.pool, toolpublicationdb.GetToolOAuthConnectionParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return ToolOAuthConnection{}, mapErr(err)
	}
	return toolOAuthRow(row)
}
func (s *PgStore) ListToolOAuthConnections(ctx context.Context, account, cursor string, limit int) ([]ToolOAuthConnection, error) {
	if limit < 1 || limit > api.ToolOAuthConnectionPageMax+1 || cursor != "" && !toolgrant.ValidID(cursor) {
		return nil, ErrInvalidArgument
	}
	if cursor == "" {
		cursor = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := toolpublicationdb.New().ListToolOAuthConnections(ctx, s.pool, toolpublicationdb.ListToolOAuthConnectionsParams{AccountID: mustPgUUID(account), Cursor: mustPgUUID(cursor), Limit: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := []ToolOAuthConnection{}
	for _, row := range rows {
		c, e := toolOAuthRow(row)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, nil
}
func (s *PgStore) RevokeToolOAuthConnection(ctx context.Context, account, id string) (ToolOAuthConnection, error) {
	if !toolgrant.ValidID(id) {
		return ToolOAuthConnection{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ToolOAuthConnection{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	row, err := q.LockToolOAuthConnection(ctx, tx, toolpublicationdb.LockToolOAuthConnectionParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return ToolOAuthConnection{}, mapErr(err)
	}
	c, err := toolOAuthRow(row)
	if err != nil {
		return c, err
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
	if err = q.CancelToolOAuthAuthorization(ctx, tx, toolpublicationdb.CancelToolOAuthAuthorizationParams{AccountID: mustPgUUID(account), ConnectionID: mustPgUUID(id)}); err != nil {
		return c, mapErr(err)
	}
	if err = updateToolOAuth(ctx, q, tx, c); err != nil {
		return c, mapErr(err)
	}
	return c, tx.Commit(ctx)
}

func (s *PgStore) BeginToolOAuthRenewal(ctx context.Context, account, id string, d api.ToolInvocationDelegation) (ToolOAuthConnectionClaim, error) {
	if !toolgrant.ValidID(id) || !toolgrant.ValidID(d.GrantID) || !toolgrant.ValidResourceID(d.KeyID) {
		return ToolOAuthConnectionClaim{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ToolOAuthConnectionClaim{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	data, err := q.LockDelegatedToolGrant(ctx, tx, toolpublicationdb.LockDelegatedToolGrantParams{AccountID: mustPgUUID(account), GrantID: mustPgUUID(d.GrantID), KeyID: mustPgUUID(d.KeyID)})
	if err != nil {
		return ToolOAuthConnectionClaim{}, mapErr(err)
	}
	var g api.ToolGrantRecord
	if err = json.Unmarshal(data, &g); err != nil {
		return ToolOAuthConnectionClaim{}, err
	}
	if g.Intent.CredentialConnectionID != id || g.ProjectID != d.ProjectID || !toolgrant.Active(g, time.Now()) {
		return ToolOAuthConnectionClaim{}, ErrNotFound
	}
	row, err := q.LockToolOAuthConnection(ctx, tx, toolpublicationdb.LockToolOAuthConnectionParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return ToolOAuthConnectionClaim{}, mapErr(err)
	}
	c, err := toolOAuthRow(row)
	if err != nil {
		return ToolOAuthConnectionClaim{}, err
	}
	if err = checkToolOAuthGrant(g, c, false); err != nil {
		return ToolOAuthConnectionClaim{}, err
	}
	if err = lockToolOAuthAuthority(ctx, q, tx, c.Record); err != nil {
		return ToolOAuthConnectionClaim{}, err
	}
	out, claimErr := claimToolOAuth(c, time.Now().UTC())
	if out.Connection.Record.ID != "" {
		if err = updateToolOAuth(ctx, q, tx, out.Connection); err != nil {
			return out, mapErr(err)
		}
		if err = tx.Commit(ctx); err != nil {
			return out, err
		}
	}
	return out, claimErr
}

func (s *PgStore) FinishToolOAuthRenewal(ctx context.Context, account, id, lease string, ciphertext []byte, expiry *time.Time, failure string) (ToolOAuthConnection, error) {
	if !toolgrant.ValidID(id) || !toolgrant.ValidID(lease) {
		return ToolOAuthConnection{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ToolOAuthConnection{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	row, err := q.LockToolOAuthConnection(ctx, tx, toolpublicationdb.LockToolOAuthConnectionParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return ToolOAuthConnection{}, mapErr(err)
	}
	c, err := toolOAuthRow(row)
	if err != nil {
		return c, err
	}
	if err = lockToolOAuthAuthority(ctx, q, tx, c.Record); err != nil {
		return c, err
	}
	out, err := finishToolOAuth(c, lease, ciphertext, expiry, failure, time.Now().UTC())
	if err != nil {
		return out, err
	}
	if err = updateToolOAuth(ctx, q, tx, out); err != nil {
		return out, mapErr(err)
	}
	return out, tx.Commit(ctx)
}
