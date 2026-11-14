package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/toolgrant"
)

func (s *PgStore) PutToolGrant(ctx context.Context, in api.ToolGrantRecord) (api.ToolGrantRecord, error) {
	in.Intent.ExpiresAt = in.Intent.ExpiresAt.UTC().Truncate(time.Microsecond)
	if toolgrant.ValidateRecord(in) != nil || in.RevokedAt != nil {
		return api.ToolGrantRecord{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ToolGrantRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	if _, err = q.LockToolPublicationAccount(ctx, tx, mustPgUUID(in.AccountID)); err != nil {
		return api.ToolGrantRecord{}, mapErr(err)
	}
	raw, err := q.GetToolGrant(ctx, tx, toolpublicationdb.GetToolGrantParams{AccountID: mustPgUUID(in.AccountID), ID: mustPgUUID(in.ID)})
	if err == nil {
		var old api.ToolGrantRecord
		if err = json.Unmarshal(raw, &old); err != nil {
			return old, err
		}
		if !toolgrant.SameIntent(old.Intent, in.Intent) || old.AppID != in.AppID || old.ProjectID != in.ProjectID || old.App != in.App || !toolgrant.SameCredential(old.Credential, in.Credential) {
			return old, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(mapErr(err), ErrNotFound) {
		return api.ToolGrantRecord{}, mapErr(err)
	}
	counts, err := q.CountToolGrants(ctx, tx, toolpublicationdb.CountToolGrantsParams{AccountID: mustPgUUID(in.AccountID), KeyID: mustPgUUID(in.Intent.KeyID)})
	if err != nil {
		return api.ToolGrantRecord{}, mapErr(err)
	}
	if err = grantCapacity(counts.Total, counts.PerKey); err != nil {
		return api.ToolGrantRecord{}, err
	}
	raw, err = json.Marshal(in)
	if err != nil {
		return api.ToolGrantRecord{}, err
	}
	rows, err := q.InsertToolGrant(ctx, tx, toolpublicationdb.InsertToolGrantParams{AccountID: mustPgUUID(in.AccountID), ID: mustPgUUID(in.ID), KeyID: mustPgUUID(in.Intent.KeyID), Version: in.Intent.Version, ExpiresAt: pgtype.Timestamptz{Time: in.Intent.ExpiresAt, Valid: true}, Payload: raw})
	if err != nil {
		return api.ToolGrantRecord{}, mapErr(err)
	}
	if rows != 1 {
		return api.ToolGrantRecord{}, ErrNotFound
	}
	return in, tx.Commit(ctx)
}

func (s *PgStore) GetToolGrant(ctx context.Context, account, id string) (api.ToolGrantRecord, error) {
	data, err := toolpublicationdb.New().GetToolGrant(ctx, s.pool, toolpublicationdb.GetToolGrantParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	var out api.ToolGrantRecord
	if err != nil {
		return out, mapErr(err)
	}
	err = json.Unmarshal(data, &out)
	return out, err
}

func (s *PgStore) ListToolGrants(ctx context.Context, account, cursor string, limit int) (api.ToolGrantPage, error) {
	out := api.ToolGrantPage{Grants: []api.ToolGrantRecord{}}
	if !validGrantPage(cursor, limit) {
		return out, ErrInvalidArgument
	}
	if cursor == "" {
		cursor = uuid.Nil.String()
	}
	rows, err := toolpublicationdb.New().ListToolGrants(ctx, s.pool, toolpublicationdb.ListToolGrantsParams{AccountID: mustPgUUID(account), Cursor: mustPgUUID(cursor), Limit: int32(limit + 1)})
	if err != nil {
		return out, mapErr(err)
	}
	for _, raw := range rows {
		var g api.ToolGrantRecord
		if err = json.Unmarshal(raw, &g); err != nil {
			return out, err
		}
		out.Grants = append(out.Grants, g)
	}
	if len(out.Grants) > limit {
		out.Grants = out.Grants[:limit]
		out.NextCursor = out.Grants[limit-1].ID
	}
	return out, nil
}

func (s *PgStore) RevokeToolGrant(ctx context.Context, account, id string) (api.ToolGrantRecord, error) {
	data, err := toolpublicationdb.New().RevokeToolGrant(ctx, s.pool, toolpublicationdb.RevokeToolGrantParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	var out api.ToolGrantRecord
	if err != nil {
		return out, mapErr(err)
	}
	err = json.Unmarshal(data, &out)
	return out, err
}

func (s *PgStore) ToolGrantEntries(ctx context.Context, account, key string) ([]ToolGrantEntry, error) {
	out := []ToolGrantEntry{}
	rows, err := toolpublicationdb.New().ToolGrantEntries(ctx, s.pool, toolpublicationdb.ToolGrantEntriesParams{AccountID: mustPgUUID(account), KeyID: mustPgUUID(key)})
	if err != nil {
		return out, mapErr(err)
	}
	for _, row := range rows {
		var entry ToolGrantEntry
		if err = json.Unmarshal(row.GrantPayload, &entry.Grant); err != nil {
			return out, err
		}
		if err = json.Unmarshal(row.PublicationPayload, &entry.Publication); err != nil {
			return out, err
		}
		out = append(out, entry)
	}
	return out, nil
}
