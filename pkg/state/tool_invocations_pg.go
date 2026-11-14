package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
)

func (s *PgStore) BeginToolInvocation(ctx context.Context, account, id string, in api.ToolInvocationIntent, target api.ToolInvocationTarget) (api.ToolInvocationReceipt, error) {
	return s.beginToolInvocation(ctx, account, id, in, target, nil)
}

func (s *PgStore) beginToolInvocation(ctx context.Context, account, id string, in api.ToolInvocationIntent, target api.ToolInvocationTarget, delegation *api.ToolInvocationDelegation) (api.ToolInvocationReceipt, error) {
	now := time.Now().UTC()
	record, err := toolinvocation.New(account, id, in, target, now)
	if err != nil {
		return api.ToolInvocationReceipt{}, invocationError(err)
	}
	record.Delegation = delegation
	if toolinvocation.ValidateRecord(record, account, id) != nil {
		return api.ToolInvocationReceipt{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	if _, err = q.LockToolPublicationAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return api.ToolInvocationReceipt{}, mapErr(err)
	}
	if delegation != nil {
		data, grantErr := q.LockDelegatedToolGrant(ctx, tx, toolpublicationdb.LockDelegatedToolGrantParams{AccountID: mustPgUUID(account), GrantID: mustPgUUID(delegation.GrantID), KeyID: mustPgUUID(delegation.KeyID)})
		if grantErr != nil {
			return api.ToolInvocationReceipt{}, mapErr(grantErr)
		}
		var g api.ToolGrantRecord
		if grantErr = json.Unmarshal(data, &g); grantErr != nil {
			return api.ToolInvocationReceipt{}, grantErr
		}
		if grantErr = lockToolGrantCredentialReady(ctx, q, tx, g, true); grantErr != nil {
			return api.ToolInvocationReceipt{}, grantErr
		}
		if !validDelegatedBinding(g, *delegation, in, target) {
			return api.ToolInvocationReceipt{}, ErrNotFound
		}
	}
	key := toolpublicationdb.GetToolInvocationParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)}
	data, err := q.GetToolInvocation(ctx, tx, key)
	if err == nil {
		if err = json.Unmarshal(data, &record); err != nil {
			return api.ToolInvocationReceipt{}, err
		}
		if !reflect.DeepEqual(record.Intent, in) || !reflect.DeepEqual(record.Delegation, delegation) {
			return api.ToolInvocationReceipt{}, ErrConflict
		}
		return api.ToolInvocationReceipt{Record: toolinvocation.View(record, now)}, nil
	}
	if !errors.Is(mapErr(err), ErrNotFound) {
		return api.ToolInvocationReceipt{}, mapErr(err)
	}
	counts, err := q.CountToolInvocations(ctx, tx, mustPgUUID(account))
	if err != nil {
		return api.ToolInvocationReceipt{}, mapErr(err)
	}
	if counts.Total >= api.ToolInvocationsPerAccount {
		return api.ToolInvocationReceipt{}, &ToolInvocationLimitError{Limit: api.ToolInvocationsPerAccount, Observed: counts.Total + 1}
	}
	if counts.Active >= api.ToolInvocationsActiveMax {
		return api.ToolInvocationReceipt{}, &ToolInvocationLimitError{Limit: api.ToolInvocationsActiveMax, Observed: counts.Active + 1}
	}
	data, err = json.Marshal(record)
	if err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	err = q.InsertToolInvocation(ctx, tx, toolpublicationdb.InsertToolInvocationParams{AccountID: key.AccountID, ID: key.ID, Payload: data, Deadline: pgtype.Timestamptz{Time: record.Deadline, Valid: true}})
	if err != nil {
		return api.ToolInvocationReceipt{}, mapErr(err)
	}
	return api.ToolInvocationReceipt{Created: true, Record: record}, tx.Commit(ctx)
}

func (s *PgStore) GetToolInvocation(ctx context.Context, account, id string) (api.ToolInvocationRecord, error) {
	var out api.ToolInvocationRecord
	if !toolinvocation.ValidID(id) {
		return out, ErrInvalidArgument
	}
	data, err := toolpublicationdb.New().GetToolInvocation(ctx, s.pool, toolpublicationdb.GetToolInvocationParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return out, mapErr(err)
	}
	err = json.Unmarshal(data, &out)
	return toolinvocation.View(out, time.Now()), err
}

func (s *PgStore) ChangeToolInvocation(ctx context.Context, account, id, action string, outcome *api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error) {
	if !toolinvocation.ValidID(id) {
		return api.ToolInvocationReceipt{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	key := toolpublicationdb.LockToolInvocationParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)}
	data, err := q.LockToolInvocation(ctx, tx, key)
	if err != nil {
		return api.ToolInvocationReceipt{}, mapErr(err)
	}
	var record api.ToolInvocationRecord
	if err = json.Unmarshal(data, &record); err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	if record.Delegation != nil && action != "cancel" {
		return api.ToolInvocationReceipt{}, ErrConflict
	}
	result, err := toolinvocation.Change(record, action, outcome, time.Now().UTC())
	if err != nil {
		return result, invocationError(err)
	}
	data, err = json.Marshal(result.Record)
	if err != nil {
		return result, err
	}
	err = q.ChangeToolInvocation(ctx, tx, toolpublicationdb.ChangeToolInvocationParams{AccountID: key.AccountID, ID: key.ID, Status: result.Record.Status, Payload: data})
	if err != nil {
		return result, mapErr(err)
	}
	return result, tx.Commit(ctx)
}
