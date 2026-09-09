package prewarm

// adr: 160

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeStore struct {
	intents   []state.PrewarmIntent
	claimed   []string
	completed map[string]string
	failed    map[string]string
}

func (f *fakeStore) ListDuePrewarmIntents(_ context.Context, before, now time.Time, _ int) ([]state.PrewarmIntent, error) {
	var out []state.PrewarmIntent
	for _, intent := range f.intents {
		if intent.Status == state.PrewarmStatusPending && !intent.WakeAt.After(before) && intent.ExpiresAt.After(now) {
			out = append(out, intent)
		}
	}
	return out, nil
}
func (f *fakeStore) ClaimPrewarmIntent(_ context.Context, id string, at time.Time) (state.PrewarmIntent, bool, error) {
	for i := range f.intents {
		if f.intents[i].ID != id {
			continue
		}
		if f.intents[i].Status != state.PrewarmStatusPending {
			return f.intents[i], false, nil
		}
		f.intents[i].Status = state.PrewarmStatusRunning
		f.intents[i].ClaimedAt = &at
		f.claimed = append(f.claimed, id)
		return f.intents[i], true, nil
	}
	return state.PrewarmIntent{}, false, state.ErrNotFound
}
func (f *fakeStore) CompletePrewarmIntent(_ context.Context, id string, _ time.Time, _ int, outcome string) error {
	if f.completed == nil {
		f.completed = map[string]string{}
	}
	f.completed[id] = outcome
	return nil
}
func (f *fakeStore) FailPrewarmIntent(_ context.Context, id string, _ time.Time, cause string) error {
	if f.failed == nil {
		f.failed = map[string]string{}
	}
	f.failed[id] = cause
	return nil
}

type fakeEngine struct {
	calls int
	appID string
	count int
	err   error
}

type fakeAuditor struct {
	mu    sync.Mutex
	kinds []string
	data  []map[string]any
}

func (f *fakeAuditor) Emit(_ context.Context, kind string, _ *string, data map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds = append(f.kinds, kind)
	f.data = append(f.data, data)
}

func (f *fakeEngine) Prewarm(_ context.Context, appID string, count int) (int, error) {
	f.calls++
	f.appID = appID
	f.count = count
	if f.err != nil {
		return 0, f.err
	}
	return count, f.err
}

func TestTickClaimsAndCompletesWithinLeadTime(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{intents: []state.PrewarmIntent{
		{ID: "due", AppID: "app-1", Count: 3, WakeAt: now.Add(30 * time.Second), ExpiresAt: now.Add(5 * time.Minute), Status: state.PrewarmStatusPending},
		{ID: "later", AppID: "app-2", Count: 2, WakeAt: now.Add(2 * time.Minute), ExpiresAt: now.Add(5 * time.Minute), Status: state.PrewarmStatusPending},
	}}
	engine := &fakeEngine{}
	tr := New(store, engine, Options{Clock: func() time.Time { return now }, LeadTime: time.Minute})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if engine.calls != 1 || engine.appID != "app-1" || engine.count != 3 {
		t.Fatalf("engine call = %#v", engine)
	}
	if got := store.completed["due"]; got != "admitted:3" {
		t.Fatalf("completion = %q", got)
	}
	if len(store.claimed) != 1 || store.claimed[0] != "due" {
		t.Fatalf("claims = %#v", store.claimed)
	}
}

func TestTickPersistsAdmissionFailure(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{intents: []state.PrewarmIntent{{ID: "bad", AppID: "app", Count: 1, WakeAt: now, ExpiresAt: now.Add(2 * time.Minute), Status: state.PrewarmStatusPending}}}
	engine := &fakeEngine{err: errors.New("capacity")}
	auditor := &fakeAuditor{}
	tr := New(store, engine, Options{Clock: func() time.Time { return now }, Auditor: auditor})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.failed["bad"] != "capacity" {
		t.Fatalf("failure = %#v", store.failed)
	}
	if len(store.completed) != 0 {
		t.Fatalf("unexpected completion = %#v", store.completed)
	}
	if len(auditor.kinds) != 1 || auditor.kinds[0] != "prewarm.fired" || auditor.data[0]["status"] != "failed" {
		t.Fatalf("audit = %#v", auditor.kinds)
	}
}

func TestTickFiresLateIntentBeforeExpiry(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{intents: []state.PrewarmIntent{{
		ID: "late", AppID: "app", Count: 1,
		WakeAt: now.Add(-30 * time.Second), ExpiresAt: now.Add(30 * time.Second),
		Status: state.PrewarmStatusPending,
	}}}
	engine := &fakeEngine{}
	tr := New(store, engine, Options{Clock: func() time.Time { return now }, LeadTime: time.Minute})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if engine.calls != 1 || store.completed["late"] != "admitted:1" {
		t.Fatalf("late intent was not fired: calls=%d completed=%#v", engine.calls, store.completed)
	}
}
