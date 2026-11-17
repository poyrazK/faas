package state

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
)

func (m *MemStore) hostedTaskSourceRequesterFreshLocked(account, key string) bool {
	if !m.taskToolReleaseKeyFreshLocked(account, key) {
		return false
	}
	k := m.keys[key]
	return slices.Contains(k.Scopes, api.ScopeAdmin) || slices.Contains(k.Scopes, api.ScopeAppsRead) && slices.Contains(k.Scopes, api.ScopeDeployWrite)
}

func (m *MemStore) hostedTaskSourceOwnerFreshLocked(account string, owner HostedTaskSourceOwner) bool {
	if owner.Session {
		return owner.KeyID == ""
	}
	k, ok := m.keys[owner.KeyID]
	return ok && m.taskToolReleaseKeyFreshLocked(account, owner.KeyID) && slices.Contains(k.Scopes, api.ScopeAdmin) && !slices.Contains(k.Scopes, api.ScopeAgent) && k.Label != api.MCPLoginKeyLabel
}

func changeHostedTaskSource(record api.HostedTaskSourceIntent, action string, in api.HostedTaskSourceDecision, fresh, hasSource bool) (api.HostedTaskSourceIntent, error) {
	out, err := tasktoolsource.Change(record, action, in, fresh, hasSource, time.Now())
	if errors.Is(err, tasktoolsource.ErrInvalid) {
		return record, ErrInvalidArgument
	}
	if err != nil {
		return record, ErrConflict
	}
	return out, nil
}

func (m *MemStore) ChangeHostedTaskSourceIntent(ctx context.Context, account, id, action string, in api.HostedTaskSourceDecision, owner HostedTaskSourceOwner) (HostedTaskSourceIntent, error) {
	if err := ctx.Err(); err != nil {
		return HostedTaskSourceIntent{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.hostedTaskSourceIntents[account+":"+id]
	if !ok || action == "decide" && !m.hostedTaskSourceOwnerFreshLocked(account, owner) {
		return HostedTaskSourceIntent{}, ErrNotFound
	}
	var fresh bool
	if action == "check" || in.Decision == "approve" {
		review, err := tasktoolsource.Decode(row.Record.Review)
		if err != nil {
			return HostedTaskSourceIntent{}, ErrInvalidArgument
		}
		fresh = m.hostedTaskSourceRequesterFreshLocked(account, row.Record.RequesterKeyID) && m.taskToolReleaseFreshLocked(account, row.Record.RequesterKeyID, tasktoolsource.Intent(review), false)
	}
	m.retireHostedSourceLocked(account)
	row = m.hostedTaskSourceIntents[account+":"+id]
	out, err := changeHostedTaskSource(row.Record, action, in, fresh, len(row.Ciphertext) > 0)
	if err != nil {
		return HostedTaskSourceIntent{}, err
	}
	row.Record = out
	// source_state is a read projection; the persisted envelope keeps the
	// original retained metadata required by the issued table constraint.
	row.Record.SourceState = "retained"
	m.hostedTaskSourceIntents[account+":"+id] = cloneHostedSource(row)
	row.Record = out
	return cloneHostedSource(row), nil
}
