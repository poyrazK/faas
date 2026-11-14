package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
)

var ErrToolInvocationLimit = errors.New("tool invocation journal or active-call limit reached")

type ToolInvocationLimitError struct{ Limit, Observed int64 }

func (e *ToolInvocationLimitError) Error() string        { return ErrToolInvocationLimit.Error() }
func (e *ToolInvocationLimitError) Is(target error) bool { return target == ErrToolInvocationLimit }

type ToolInvocationStore interface {
	BeginToolInvocation(context.Context, string, string, api.ToolInvocationIntent, api.ToolInvocationTarget) (api.ToolInvocationReceipt, error)
	GetToolInvocation(context.Context, string, string) (api.ToolInvocationRecord, error)
	ChangeToolInvocation(context.Context, string, string, string, *api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error)
}

func invocationError(err error) error {
	if errors.Is(err, toolinvocation.ErrInvalid) {
		return ErrInvalidArgument
	}
	if errors.Is(err, toolinvocation.ErrConflict) {
		return ErrConflict
	}
	return err
}

func cloneToolInvocation(record api.ToolInvocationRecord) api.ToolInvocationRecord {
	data, _ := json.Marshal(record)
	var out api.ToolInvocationRecord
	_ = json.Unmarshal(data, &out)
	return out
}

func (m *MemStore) BeginToolInvocation(ctx context.Context, account, id string, in api.ToolInvocationIntent, target api.ToolInvocationTarget) (api.ToolInvocationReceipt, error) {
	return m.beginToolInvocation(ctx, account, id, in, target, nil)
}

func (m *MemStore) beginToolInvocation(_ context.Context, account, id string, in api.ToolInvocationIntent, target api.ToolInvocationTarget, delegation *api.ToolInvocationDelegation) (api.ToolInvocationReceipt, error) {
	now := time.Now().UTC()
	record, err := toolinvocation.New(account, id, in, target, now)
	if err != nil {
		return api.ToolInvocationReceipt{}, invocationError(err)
	}
	record.Delegation = delegation
	if toolinvocation.ValidateRecord(record, account, id) != nil {
		return api.ToolInvocationReceipt{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[account]; !ok {
		return api.ToolInvocationReceipt{}, ErrNotFound
	}
	if delegation != nil {
		g, grantErr := m.delegatedGrantLocked(account, *delegation)
		if grantErr != nil {
			return api.ToolInvocationReceipt{}, grantErr
		}
		if g.Credential != nil && g.Credential.ConnectionID != "" {
			if grantErr = m.toolOAuthGrantLocked(g, true); grantErr != nil {
				return api.ToolInvocationReceipt{}, grantErr
			}
		}
		if !validDelegatedBinding(g, *delegation, in, target) {
			return api.ToolInvocationReceipt{}, ErrNotFound
		}
	}
	if m.toolInvocations == nil {
		m.toolInvocations = map[string]api.ToolInvocationRecord{}
	}
	key := account + ":" + id
	if old, ok := m.toolInvocations[key]; ok {
		if !reflect.DeepEqual(old.Intent, in) || !reflect.DeepEqual(old.Delegation, delegation) {
			return api.ToolInvocationReceipt{}, ErrConflict
		}
		return api.ToolInvocationReceipt{Record: toolinvocation.View(cloneToolInvocation(old), now)}, nil
	}
	total, active := 0, 0
	for _, old := range m.toolInvocations {
		if old.AccountID == account {
			total++
			if !toolinvocation.Terminal(old.Status) && now.Before(old.Deadline) {
				active++
			}
		}
	}
	if total >= api.ToolInvocationsPerAccount {
		return api.ToolInvocationReceipt{}, &ToolInvocationLimitError{Limit: api.ToolInvocationsPerAccount, Observed: int64(total + 1)}
	}
	if active >= api.ToolInvocationsActiveMax {
		return api.ToolInvocationReceipt{}, &ToolInvocationLimitError{Limit: api.ToolInvocationsActiveMax, Observed: int64(active + 1)}
	}
	m.toolInvocations[key] = cloneToolInvocation(record)
	return api.ToolInvocationReceipt{Created: true, Record: record}, nil
}

func (m *MemStore) GetToolInvocation(_ context.Context, account, id string) (api.ToolInvocationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.toolInvocations[account+":"+id]
	if !ok {
		return record, ErrNotFound
	}
	return toolinvocation.View(cloneToolInvocation(record), time.Now()), nil
}

func (m *MemStore) ChangeToolInvocation(_ context.Context, account, id, action string, out *api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := account + ":" + id
	record, ok := m.toolInvocations[key]
	if !ok {
		return api.ToolInvocationReceipt{}, ErrNotFound
	}
	if record.Delegation != nil && action != "cancel" {
		return api.ToolInvocationReceipt{}, ErrConflict
	}
	result, err := toolinvocation.Change(record, action, out, time.Now().UTC())
	if err != nil {
		return result, invocationError(err)
	}
	m.toolInvocations[key] = cloneToolInvocation(result.Record)
	return result, nil
}
