package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/toolgrant"
)

func toolAuthorizationRow(r toolpublicationdb.ToolOauthAuthorization) ToolOAuthAuthorization {
	return ToolOAuthAuthorization{AccountID: r.AccountID.String(), ConnectionID: r.ConnectionID.String(), StateSHA256: r.StateSha256, Phase: r.Phase, ExpiresAt: r.ExpiresAt.Time, Ciphertext: append([]byte(nil), r.FlowCiphertext...)}
}

func (s *PgStore) GetToolOAuthAuthorization(ctx context.Context, account, id string) (ToolOAuthAuthorization, error) {
	if !toolgrant.ValidResourceID(account) || !toolgrant.ValidID(id) {
		return ToolOAuthAuthorization{}, ErrInvalidArgument
	}
	r, err := toolpublicationdb.New().GetToolOAuthAuthorization(ctx, s.pool, toolpublicationdb.GetToolOAuthAuthorizationParams{AccountID: mustPgUUID(account), ConnectionID: mustPgUUID(id)})
	if err != nil {
		return ToolOAuthAuthorization{}, mapErr(err)
	}
	return toolAuthorizationRow(r), nil
}

// Every authorization transition takes the connection lock first, including
// revocation. No SQL transaction spans a provider request.
func (s *PgStore) changeToolAuthorization(ctx context.Context, account, id string, change func(ToolOAuthConnection) (ToolOAuthConnection, error)) (ToolOAuthConnection, error) {
	if !toolgrant.ValidResourceID(account) || !toolgrant.ValidID(id) {
		return ToolOAuthConnection{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ToolOAuthConnection{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	r, err := q.LockToolOAuthConnection(ctx, tx, toolpublicationdb.LockToolOAuthConnectionParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return ToolOAuthConnection{}, mapErr(err)
	}
	c, err := toolOAuthRow(r)
	if err != nil {
		return c, err
	}
	if err = lockToolOAuthAuthority(ctx, q, tx, c.Record); err != nil {
		return c, err
	}
	a, err := q.GetToolOAuthAuthorization(ctx, tx, toolpublicationdb.GetToolOAuthAuthorizationParams{AccountID: mustPgUUID(account), ConnectionID: mustPgUUID(id)})
	if err == nil {
		x := toolAuthorizationRow(a)
		c.Authorization = &x
	} else if !errors.Is(mapErr(err), ErrNotFound) {
		return c, mapErr(err)
	}
	out, changeErr := change(c)
	if changeErr != nil && !errors.Is(changeErr, ErrToolOAuthReconnect) {
		return out, changeErr
	}
	if out.Authorization == nil {
		return out, ErrNotFound
	}
	f := out.Authorization
	err = q.StoreToolOAuthAuthorization(ctx, tx, toolpublicationdb.StoreToolOAuthAuthorizationParams{AccountID: mustPgUUID(account), ConnectionID: mustPgUUID(id), StateSha256: f.StateSHA256, Phase: f.Phase, ExpiresAt: pgtype.Timestamptz{Time: f.ExpiresAt, Valid: true}, FlowCiphertext: f.Ciphertext})
	if err != nil {
		return out, mapErr(err)
	}
	if err = updateToolOAuth(ctx, q, tx, out); err != nil {
		return out, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, changeErr
}

func (s *PgStore) BeginToolOAuthAuthorization(ctx context.Context, account, id string, a ToolOAuthAuthorization) (ToolOAuthAuthorization, error) {
	c, err := s.changeToolAuthorization(ctx, account, id, func(c ToolOAuthConnection) (ToolOAuthConnection, error) {
		if !validToolAuthorization(c, a, time.Now()) {
			return c, ErrConflict
		}
		if c.Authorization != nil {
			if c.Authorization.Phase != "pending" || !time.Now().Before(c.Authorization.ExpiresAt) {
				return c, ErrToolOAuthReconnect
			}
			return c, nil
		}
		x := cloneToolAuthorization(a)
		c.Authorization = &x
		return c, nil
	})
	if c.Authorization == nil {
		return ToolOAuthAuthorization{}, err
	}
	return cloneToolAuthorization(*c.Authorization), err
}

func (s *PgStore) ConsumeToolOAuthAuthorization(ctx context.Context, account, id, hash string) (ToolOAuthConnection, error) {
	return s.changeToolAuthorization(ctx, account, id, func(c ToolOAuthConnection) (ToolOAuthConnection, error) {
		return consumeToolAuthorization(c, hash, time.Now().UTC())
	})
}

func (s *PgStore) FinishToolOAuthAuthorization(ctx context.Context, account, id, lease string, ciphertext []byte, expiry *time.Time, failure string) (ToolOAuthConnection, error) {
	return s.changeToolAuthorization(ctx, account, id, func(c ToolOAuthConnection) (ToolOAuthConnection, error) {
		return finishToolAuthorization(c, lease, ciphertext, expiry, failure, time.Now().UTC())
	})
}
