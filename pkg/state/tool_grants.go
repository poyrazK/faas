package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolgrant"
)

var ErrToolGrantLimit = errors.New("tool grant capacity reached")

type ToolGrantLimitError struct{ Limit, Observed int64 }

func (e *ToolGrantLimitError) Error() string {
	return fmt.Sprintf("tool grant limit %d exceeded by %d", e.Limit, e.Observed)
}
func (e *ToolGrantLimitError) Is(target error) bool { return target == ErrToolGrantLimit }

type ToolGrantEntry struct {
	Grant       api.ToolGrantRecord
	Publication api.ToolPublicationRecord
}

// ToolGrantStore is apid-owned delegation intent, separate from execution and
// connection consent. Stores never infer or widen the grantee's authority.
type ToolGrantStore interface {
	PutToolGrant(context.Context, api.ToolGrantRecord) (api.ToolGrantRecord, error)
	GetToolGrant(context.Context, string, string) (api.ToolGrantRecord, error)
	ListToolGrants(context.Context, string, string, int) (api.ToolGrantPage, error)
	RevokeToolGrant(context.Context, string, string) (api.ToolGrantRecord, error)
	ToolGrantEntries(context.Context, string, string) ([]ToolGrantEntry, error)
}

func cloneToolGrant(in api.ToolGrantRecord) api.ToolGrantRecord {
	raw, _ := json.Marshal(in)
	var out api.ToolGrantRecord
	_ = json.Unmarshal(raw, &out)
	return out
}

func grantCapacity(total, perKey int64) error {
	if total >= api.ToolGrantsPerAccount {
		return &ToolGrantLimitError{api.ToolGrantsPerAccount, total + 1}
	}
	if perKey >= api.ToolGrantsPerKey {
		return &ToolGrantLimitError{api.ToolGrantsPerKey, perKey + 1}
	}
	return nil
}

func validGrantPage(cursor string, limit int) bool {
	return (cursor == "" || toolgrant.ValidID(cursor)) && limit > 0 && limit <= api.ToolGrantPageMax
}

func (m *MemStore) PutToolGrant(_ context.Context, in api.ToolGrantRecord) (api.ToolGrantRecord, error) {
	in.Intent.ExpiresAt = in.Intent.ExpiresAt.UTC().Truncate(time.Microsecond)
	if toolgrant.ValidateRecord(in) != nil || in.RevokedAt != nil {
		return api.ToolGrantRecord{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.toolGrants == nil {
		m.toolGrants = map[string]api.ToolGrantRecord{}
	}
	key := in.AccountID + ":" + in.ID
	if old, ok := m.toolGrants[key]; ok {
		if !toolgrant.SameIntent(old.Intent, in.Intent) || old.AppID != in.AppID || old.ProjectID != in.ProjectID || old.App != in.App || !toolgrant.SameCredential(old.Credential, in.Credential) {
			return api.ToolGrantRecord{}, ErrConflict
		}
		return cloneToolGrant(old), nil
	}
	k, ok := m.keys[in.Intent.KeyID]
	if !ok || k.AccountID != in.AccountID || (k.Status != "active" && k.Status != "grace") || (k.ExpiresAt != nil && !time.Now().Before(*k.ExpiresAt)) {
		return api.ToolGrantRecord{}, ErrNotFound
	}
	p, ok := m.toolPublications[in.AccountID+":"+in.Intent.Version]
	if !ok || p.Status != "completed" {
		return api.ToolGrantRecord{}, ErrNotFound
	}
	var total, perKey int64
	for _, g := range m.toolGrants {
		if g.AccountID == in.AccountID {
			total++
			if g.Intent.KeyID == in.Intent.KeyID {
				perKey++
			}
		}
	}
	if err := grantCapacity(total, perKey); err != nil {
		return api.ToolGrantRecord{}, err
	}
	m.toolGrants[key] = cloneToolGrant(in)
	return cloneToolGrant(in), nil
}

func (m *MemStore) GetToolGrant(_ context.Context, account, id string) (api.ToolGrantRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.toolGrants[account+":"+id]
	if !ok {
		return g, ErrNotFound
	}
	return cloneToolGrant(g), nil
}

func (m *MemStore) ListToolGrants(_ context.Context, account, cursor string, limit int) (api.ToolGrantPage, error) {
	if !validGrantPage(cursor, limit) {
		return api.ToolGrantPage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := api.ToolGrantPage{Grants: []api.ToolGrantRecord{}}
	for _, g := range m.toolGrants {
		if g.AccountID == account && g.ID > cursor {
			out.Grants = append(out.Grants, cloneToolGrant(g))
		}
	}
	sort.Slice(out.Grants, func(i, j int) bool { return out.Grants[i].ID < out.Grants[j].ID })
	if len(out.Grants) > limit {
		out.Grants = out.Grants[:limit]
		out.NextCursor = out.Grants[limit-1].ID
	}
	return out, nil
}

func (m *MemStore) RevokeToolGrant(_ context.Context, account, id string) (api.ToolGrantRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := account + ":" + id
	g, ok := m.toolGrants[key]
	if !ok {
		return g, ErrNotFound
	}
	if g.RevokedAt == nil {
		now := time.Now().UTC()
		g.RevokedAt = &now
		m.toolGrants[key] = g
	}
	return cloneToolGrant(g), nil
}

func (m *MemStore) ToolGrantEntries(_ context.Context, account, key string) ([]ToolGrantEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ToolGrantEntry{}
	for _, g := range m.toolGrants {
		if g.AccountID != account || g.Intent.KeyID != key || !toolgrant.Active(g, time.Now()) {
			continue
		}
		if p, ok := m.toolPublications[account+":"+g.Intent.Version]; ok && p.Status == "completed" {
			out = append(out, ToolGrantEntry{cloneToolGrant(g), cloneToolPublication(p)})
		}
	}
	return out, nil
}
