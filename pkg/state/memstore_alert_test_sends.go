package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

var _ AlertTestSendStore = (*MemStore)(nil)

type memAlertTestSend struct {
	AlertTestSend
	claimedUntil time.Time
}

func (t memAlertTestSend) copyOut() AlertTestSend {
	out := t.AlertTestSend
	out.Event = append(json.RawMessage(nil), t.Event...)
	if t.Result != nil {
		r := *t.Result
		out.Result = &r
	}
	return out
}

// CreateAlertTestSend mirrors PgStore (ADR 20261022111812).
func (m *MemStore) CreateAlertTestSend(_ context.Context, in AlertTestSend) (AlertTestSend, error) {
	if len(in.Event) == 0 {
		in.Event = json.RawMessage(`{}`)
	}
	if !validAlertTestSend(in) {
		return AlertTestSend{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.alertTestSends == nil {
		m.alertTestSends = map[string]memAlertTestSend{}
	}
	if _, dup := m.alertTestSends[in.ID]; dup {
		return AlertTestSend{}, ErrConflict
	}
	t := memAlertTestSend{AlertTestSend: AlertTestSend{
		ID: in.ID, AccountID: in.AccountID, Kind: in.Kind, SubjectID: in.SubjectID,
		Event: append(json.RawMessage(nil), in.Event...), State: AlertTestSendPending, CreatedAt: time.Now().UTC(),
	}}
	m.alertTestSends[in.ID] = t
	return t.copyOut(), nil
}

// GetAlertTestSend mirrors PgStore.
func (m *MemStore) GetAlertTestSend(_ context.Context, accountID, id string) (AlertTestSend, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.alertTestSends[id]
	if !ok || t.AccountID != accountID {
		return AlertTestSend{}, ErrNotFound
	}
	return t.copyOut(), nil
}

// ClaimAlertTestSend mirrors PgStore.
func (m *MemStore) ClaimAlertTestSend(_ context.Context, lease time.Duration) (AlertTestSend, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var open []memAlertTestSend
	for _, t := range m.alertTestSends {
		if t.State == AlertTestSendPending || (t.State == AlertTestSendClaimed && t.claimedUntil.Before(now)) {
			open = append(open, t)
		}
	}
	if len(open) == 0 {
		return AlertTestSend{}, false, nil
	}
	sort.Slice(open, func(i, j int) bool { return open[i].CreatedAt.Before(open[j].CreatedAt) })
	t := open[0]
	t.State, t.ClaimToken, t.claimedUntil = AlertTestSendClaimed, uuid.NewString(), now.Add(lease)
	m.alertTestSends[t.ID] = t
	return t.copyOut(), true, nil
}

// CompleteAlertTestSend mirrors PgStore.
func (m *MemStore) CompleteAlertTestSend(_ context.Context, id, claimToken string, result AlertTestSendResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.alertTestSends[id]
	if !ok || t.State != AlertTestSendClaimed || t.ClaimToken != claimToken {
		return ErrConflict
	}
	r := result
	t.State, t.ClaimToken, t.claimedUntil, t.Result = AlertTestSendDone, "", time.Time{}, &r
	m.alertTestSends[id] = t
	return nil
}

// DeleteAlertTestSend mirrors PgStore.
func (m *MemStore) DeleteAlertTestSend(_ context.Context, accountID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.alertTestSends[id]; ok && t.AccountID == accountID {
		delete(m.alertTestSends, id)
	}
	return nil
}

// PruneAlertTestSends mirrors PgStore.
func (m *MemStore) PruneAlertTestSends(_ context.Context, cutoff time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, t := range m.alertTestSends {
		if t.CreatedAt.Before(cutoff) {
			delete(m.alertTestSends, id)
			n++
		}
	}
	return n, nil
}
