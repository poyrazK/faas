package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
)

var ErrHostedTaskSourceLimit = errors.New("hosted Task source retention limit")

// HostedTaskSourceLimitError reports the actual exceeded admission bound.
type HostedTaskSourceLimitError struct {
	Quota           string
	Limit, Observed int64
}

func (e *HostedTaskSourceLimitError) Error() string {
	return "hosted Task source retention quota exceeded: " + e.Quota
}
func (e *HostedTaskSourceLimitError) Unwrap() error { return ErrHostedTaskSourceLimit }
func hostedSourceQuota(records, active, cipherBytes int64) error {
	for _, q := range []struct {
		name            string
		observed, limit int64
	}{{"identities", records, api.HostedTaskSourceRecordsPerAccount}, {"active_intents", active, api.HostedTaskSourceActivePerAccount}, {"ciphertext_bytes", cipherBytes, api.HostedTaskSourceAccountCiphertextMaxBytes}} {
		if q.observed > q.limit {
			return &HostedTaskSourceLimitError{Quota: q.name, Limit: q.limit, Observed: q.observed}
		}
	}
	return nil
}

// HostedTaskSourceIntent is private store data; handlers project only Record.
type HostedTaskSourceIntent struct {
	Record     api.HostedTaskSourceIntent
	Ciphertext []byte
}
type HostedTaskSourceIntentStore interface {
	PrepareHostedTaskSourceIntent(context.Context, string, string, string, tasktoolsource.Review, []byte) (HostedTaskSourceIntent, error)
	GetHostedTaskSourceIntent(context.Context, string, string) (HostedTaskSourceIntent, error)
	PurgeExpiredHostedTaskSources(context.Context) (int64, error)
	ChangeHostedTaskSourceIntent(context.Context, string, string, string, api.HostedTaskSourceDecision, HostedTaskSourceOwner) (HostedTaskSourceIntent, error)
}

// HostedTaskSourceOwner is populated only by the authenticated native handler,
// never decoded from a request. Session marks an authenticated personal owner.
type HostedTaskSourceOwner struct {
	KeyID   string
	Session bool
}

func cloneHostedSource(in HostedTaskSourceIntent) HostedTaskSourceIntent {
	b, _ := json.Marshal(in.Record)
	var record api.HostedTaskSourceIntent
	_ = json.Unmarshal(b, &record)
	return HostedTaskSourceIntent{Record: record, Ciphertext: append([]byte(nil), in.Ciphertext...)}
}
func hostedSourceReplay(existing HostedTaskSourceIntent, key string, r tasktoolsource.Review) (HostedTaskSourceIntent, error) {
	retained, err := tasktoolsource.Decode(existing.Record.Review)
	if err != nil {
		return HostedTaskSourceIntent{}, err
	}
	raw, _ := json.Marshal(r)
	old, _ := json.Marshal(retained)
	if existing.Record.RequesterKeyID != key || existing.Record.ReviewSHA256 != r.Fingerprint || string(old) != string(raw) {
		return HostedTaskSourceIntent{}, ErrConflict
	}
	out := cloneHostedSource(existing)
	out.Record = tasktoolsource.View(out.Record, len(out.Ciphertext) > 0, time.Now())
	return out, nil
}
func (m *MemStore) retireHostedSourceLocked(account string) int64 {
	var n int64
	now := time.Now()
	for k, row := range m.hostedTaskSourceIntents {
		if (account == "" || row.Record.AccountID == account) && len(row.Ciphertext) > 0 && !now.Before(row.Record.ExpiresAt) {
			row.Ciphertext = nil
			m.hostedTaskSourceIntents[k] = row
			n++
			if account == "" && n >= api.HostedTaskSourceSweepBatch {
				break
			}
		}
	}
	return n
}
func (m *MemStore) PrepareHostedTaskSourceIntent(_ context.Context, account, key, id string, r tasktoolsource.Review, ciphertext []byte) (HostedTaskSourceIntent, error) {
	record, err := tasktoolsource.NewRecord(account, key, id, r, time.Now())
	if err != nil || len(ciphertext) == 0 || len(ciphertext) > api.HostedTaskSourceCiphertextMaxBytes {
		return HostedTaskSourceIntent{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hostedTaskSourceRequesterFreshLocked(account, key) {
		return HostedTaskSourceIntent{}, ErrNotFound
	}
	m.retireHostedSourceLocked(account)
	if old, ok := m.hostedTaskSourceIntents[account+":"+id]; ok {
		return hostedSourceReplay(old, key, r)
	}
	if !m.taskToolReleaseFreshLocked(account, key, tasktoolsource.Intent(r), false) {
		return HostedTaskSourceIntent{}, ErrConflict
	}
	count, active, size := 0, 0, len(ciphertext)
	for _, row := range m.hostedTaskSourceIntents {
		if row.Record.AccountID == account {
			count++
			if len(row.Ciphertext) > 0 {
				active++
				size += len(row.Ciphertext)
			}
		}
	}
	if err := hostedSourceQuota(int64(count+1), int64(active+1), int64(size)); err != nil {
		return HostedTaskSourceIntent{}, err
	}

	if m.hostedTaskSourceIntents == nil {
		m.hostedTaskSourceIntents = map[string]HostedTaskSourceIntent{}
	}
	out := HostedTaskSourceIntent{Record: record, Ciphertext: append([]byte(nil), ciphertext...)}
	m.hostedTaskSourceIntents[account+":"+id] = out
	return cloneHostedSource(out), nil
}
func (m *MemStore) GetHostedTaskSourceIntent(_ context.Context, account, id string) (HostedTaskSourceIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retireHostedSourceLocked(account)
	row, ok := m.hostedTaskSourceIntents[account+":"+id]
	if !ok {
		return HostedTaskSourceIntent{}, ErrNotFound
	}
	out := cloneHostedSource(row)
	out.Record = tasktoolsource.View(out.Record, len(out.Ciphertext) > 0, time.Now())
	return out, nil
}
func (m *MemStore) PurgeExpiredHostedTaskSources(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.retireHostedSourceLocked(""), nil
}
