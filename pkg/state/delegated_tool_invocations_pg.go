package state

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
)

func (s *PgStore) BeginDelegatedToolInvocation(ctx context.Context, account, id string, in api.ToolInvocationIntent, target api.ToolInvocationTarget, d api.ToolInvocationDelegation) (api.ToolInvocationReceipt, error) {
	return s.beginToolInvocation(ctx, account, id, in, target, &d)
}

func (s *PgStore) ChangeDelegatedToolInvocation(ctx context.Context, account, id string, d api.ToolInvocationDelegation, action string, out *api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error) {
	if !toolinvocation.ValidID(id) || !toolinvocation.ValidID(d.GrantID) {
		return api.ToolInvocationReceipt{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	data, err := q.LockDelegatedToolGrant(ctx, tx, toolpublicationdb.LockDelegatedToolGrantParams{AccountID: mustPgUUID(account), GrantID: mustPgUUID(d.GrantID), KeyID: mustPgUUID(d.KeyID)})
	if err != nil {
		return api.ToolInvocationReceipt{}, mapErr(err)
	}
	var g api.ToolGrantRecord
	if err = json.Unmarshal(data, &g); err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	if err = lockToolGrantCredentialReady(ctx, q, tx, g, action == "dispatch"); err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	data, err = q.LockToolInvocation(ctx, tx, toolpublicationdb.LockToolInvocationParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return api.ToolInvocationReceipt{}, mapErr(err)
	}
	var record api.ToolInvocationRecord
	if err = json.Unmarshal(data, &record); err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	if !reflect.DeepEqual(record.Delegation, &d) || !validDelegatedBinding(g, d, record.Intent, record.Target) {
		return api.ToolInvocationReceipt{}, ErrNotFound
	}
	result, err := toolinvocation.Change(record, action, out, time.Now().UTC())
	if err != nil {
		return result, invocationError(err)
	}
	data, err = json.Marshal(result.Record)
	if err != nil {
		return result, err
	}
	err = q.ChangeToolInvocation(ctx, tx, toolpublicationdb.ChangeToolInvocationParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id), Status: result.Record.Status, Payload: data})
	if err != nil {
		return result, mapErr(err)
	}
	return result, tx.Commit(ctx)
}
