package durableentity

// adr: 712

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memoryObject struct {
	body    []byte
	version string
}
type memoryStore struct {
	mu      sync.Mutex
	objects map[string]memoryObject
}

func newMemoryStore() *memoryStore { return &memoryStore{objects: make(map[string]memoryObject)} }

func (s *memoryStore) Get(ctx context.Context, key string, maxBytes int64) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.objects[key]
	if !ok {
		return nil, "", ErrNotFound
	}
	if int64(len(object.body)) > maxBytes {
		return nil, "", ErrLimit
	}
	return append([]byte(nil), object.body...), object.version, nil
}

func (s *memoryStore) Put(ctx context.Context, key string, body []byte, expectedVersion string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	object, exists := s.objects[key]
	if expectedVersion == "" && exists || expectedVersion != "" && (!exists || object.version != expectedVersion) {
		return "", ErrConflict
	}
	version := `"` + digest(body) + `"`
	s.objects[key] = memoryObject{body: append([]byte(nil), body...), version: version}
	return version, nil
}

type wrappedStore struct {
	ObjectStore
	put func(context.Context, string, []byte, string) (string, error)
}

func (s wrappedStore) Put(ctx context.Context, key string, body []byte, version string) (string, error) {
	return s.put(ctx, key, body, version)
}

type fixture struct {
	store   *memoryStore
	manager *Manager
	clock   *atomic.Int64
	id      ID
	claim   Claim
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC).UnixNano())
	s := newMemoryStore()
	m := openManager(t, s, clock)
	id := ID{AccountID: "account-a", AppID: "app-a", Namespace: "customers", Key: "customer:456"}
	claim, err := m.Acquire(t.Context(), id, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return fixture{store: s, manager: m, clock: clock, id: id, claim: claim}
}

func openManager(t *testing.T, s ObjectStore, clock *atomic.Int64) *Manager {
	t.Helper()
	m, err := Open(t.Context(), s, Options{Now: func() time.Time { return time.Unix(0, clock.Load()) }})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func increment(_ context.Context, view View) (Transition, error) {
	var state struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(view.Data, &state); err != nil {
		return Transition{}, err
	}
	state.Count++
	body, err := json.Marshal(state)
	return Transition{Data: body, Result: body, AlarmAt: view.AlarmAt}, err
}

func request(id string) Request { return Request{ID: id, Payload: json.RawMessage(`{"delta":1}`)} }

func assertCount(t *testing.T, ctx context.Context, m *Manager, id ID, count int, version uint64) {
	t.Helper()
	view, err := m.Read(ctx, id)
	if err != nil || view.Version != version {
		t.Fatalf("read: %+v, %v", view, err)
	}
	var state struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(view.Data, &state); err != nil || state.Count != count {
		t.Fatalf("count: %s, %v", view.Data, err)
	}
}

func TestRestartRestoresStateAndReplaysOriginalResult(t *testing.T) {
	f := newFixture(t)
	alarm := time.Unix(0, f.clock.Load()).Add(time.Hour)
	first, err := f.manager.Execute(t.Context(), f.claim, request("first"), func(ctx context.Context, v View) (Transition, error) {
		transition, err := increment(ctx, v)
		transition.AlarmAt = &alarm
		transition.Result = json.RawMessage(`{ "count": 1 }`)
		return transition, err
	})
	if err != nil || first.Replayed {
		t.Fatal(first, err)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("second"), increment); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	restarted := openManager(t, f.store, f.clock)
	claim, err := restarted.Acquire(t.Context(), f.id, "new-process")
	if err != nil || claim.Epoch != f.claim.Epoch+1 || claim.Token == f.claim.Token {
		t.Fatal(claim, err)
	}
	replayed, err := restarted.Execute(t.Context(), claim, request("first"), func(context.Context, View) (Transition, error) {
		t.Fatal("replay ran handler")
		return Transition{}, nil
	})
	if err != nil || !replayed.Replayed || replayed.Version != first.Version || string(replayed.Value) != string(first.Value) {
		t.Fatal(first, replayed, err)
	}
	assertCount(t, t.Context(), restarted, f.id, 2, 2)
	view, err := restarted.Read(t.Context(), f.id)
	if err != nil || view.AlarmAt == nil || !view.AlarmAt.Equal(alarm) {
		t.Fatal(view, err)
	}
	changed := request("first")
	changed.Payload = json.RawMessage(`{"delta":2}`)
	if _, err := restarted.Execute(t.Context(), claim, changed, increment); !errors.Is(err, ErrRequestConflict) {
		t.Fatal(err)
	}
	if _, err := restarted.Renew(t.Context(), f.claim); !errors.Is(err, ErrStaleOwner) {
		t.Fatal(err)
	}
}

func TestIdentityIsolationAndBusyOwner(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Acquire(t.Context(), f.id, "another-process"); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	for _, field := range []string{"account", "app", "namespace", "key"} {
		t.Run(field, func(t *testing.T) {
			id := f.id
			switch field {
			case "account":
				id.AccountID = "another"
			case "app":
				id.AppID = "another"
			case "namespace":
				id.Namespace = "another"
			case "key":
				id.Key = "another"
			}
			if id.prefix() == f.id.prefix() {
				t.Fatal("scope collision")
			}
			claim, err := f.manager.Acquire(t.Context(), id, "process")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.Execute(t.Context(), claim, request("same-id"), increment); err != nil {
				t.Fatal(err)
			}
			assertCount(t, t.Context(), f.manager, id, 1, 1)
		})
	}
	assertCount(t, t.Context(), f.manager, f.id, 0, 0)
}

func TestConcurrentTransitionsSerializeAndReleaseIdleLocks(t *testing.T) {
	f := newFixture(t)
	const requests = 32
	var wg sync.WaitGroup
	var active atomic.Int64
	errorsCh := make(chan error, requests)
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), func(ctx context.Context, v View) (Transition, error) {
				if active.Add(1) != 1 {
					return Transition{}, errors.New("overlapping handlers")
				}
				defer active.Add(-1)
				runtime.Gosched()
				return increment(ctx, v)
			})
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, t.Context(), f.manager, f.id, requests, requests)
	f.manager.mu.Lock()
	defer f.manager.mu.Unlock()
	if len(f.manager.locks) != 0 {
		t.Fatal("idle entity retained a lock")
	}
}

func TestTakeoverWhileHandlerRunsFencesItsState(t *testing.T) {
	f := newFixture(t)
	replacement := openManager(t, f.store, f.clock)
	_, err := f.manager.Execute(t.Context(), f.claim, request("old"), func(ctx context.Context, v View) (Transition, error) {
		f.clock.Add(int64(time.Minute))
		claim, err := replacement.Acquire(ctx, f.id, "replacement")
		if err != nil {
			return Transition{}, err
		}
		if _, err := replacement.Execute(ctx, claim, request("new"), increment); err != nil {
			return Transition{}, err
		}
		return increment(ctx, v)
	})
	if !errors.Is(err, ErrStaleOwner) {
		t.Fatal(err)
	}
	assertCount(t, t.Context(), replacement, f.id, 1, 1)
	if err := f.manager.Release(t.Context(), f.claim); !errors.Is(err, ErrStaleOwner) {
		t.Fatal(err)
	}
}

func TestTakeoverBetweenOwnershipReadAndPublicationRejectsCAS(t *testing.T) {
	f := newFixture(t)
	replacement := openManager(t, f.store, f.clock)
	var armed atomic.Bool
	armed.Store(true)
	f.manager.store = wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, version string) (string, error) {
		if strings.HasSuffix(key, "manifest.json") && armed.CompareAndSwap(true, false) {
			f.clock.Add(int64(time.Minute))
			if _, err := replacement.Acquire(ctx, f.id, "replacement"); err != nil {
				return "", err
			}
		}
		return f.store.Put(ctx, key, body, version)
	}}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("old"), increment); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	assertCount(t, t.Context(), replacement, f.id, 0, 0)
}

func TestLostCommitResponseSurvivesTakeoverAndRetry(t *testing.T) {
	f := newFixture(t)
	var lost atomic.Bool
	f.manager.store = wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, version string) (string, error) {
		etag, err := f.store.Put(ctx, key, body, version)
		if err == nil && strings.HasSuffix(key, "manifest.json") && lost.CompareAndSwap(false, true) {
			return "", errors.New("response lost after commit")
		}
		return etag, err
	}}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("payment-state"), increment); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	f.clock.Add(int64(time.Minute))
	restarted := openManager(t, f.store, f.clock)
	claim, err := restarted.Acquire(t.Context(), f.id, "new-process")
	if err != nil {
		t.Fatal(err)
	}
	result, err := restarted.Execute(t.Context(), claim, request("payment-state"), func(context.Context, View) (Transition, error) {
		t.Fatal("committed request executed twice")
		return Transition{}, nil
	})
	if err != nil || !result.Replayed {
		t.Fatal(result, err)
	}
	assertCount(t, t.Context(), restarted, f.id, 1, 1)
}

func TestUncommittedUploadNeverChangesState(t *testing.T) {
	for _, afterUpload := range []bool{false, true} {
		t.Run(fmt.Sprint(afterUpload), func(t *testing.T) {
			f := newFixture(t)
			failed := errors.New("snapshot upload interrupted")
			f.manager.store = wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, version string) (string, error) {
				if strings.Contains(key, "/snapshots/") {
					if afterUpload {
						if _, err := f.store.Put(ctx, key, body, version); err != nil {
							return "", err
						}
					}
					return "", failed
				}
				return f.store.Put(ctx, key, body, version)
			}}
			if _, err := f.manager.Execute(t.Context(), f.claim, request("interrupted"), increment); !errors.Is(err, failed) {
				t.Fatal(err)
			}
			assertCount(t, t.Context(), f.manager, f.id, 0, 0)
		})
	}
}

func TestRenewalDuringTransitionPreservesStateAndAuthority(t *testing.T) {
	f := newFixture(t)
	_, err := f.manager.Execute(t.Context(), f.claim, request("renewed"), func(ctx context.Context, v View) (Transition, error) {
		f.clock.Add(int64(10 * time.Second))
		if _, err := f.manager.Renew(ctx, f.claim); err != nil {
			return Transition{}, err
		}
		return increment(ctx, v)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCount(t, t.Context(), f.manager, f.id, 1, 1)
	if _, err := f.manager.Renew(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
}

func TestCommittedSnapshotCorruptionAndMissingDataFailClosed(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.manager.Execute(t.Context(), f.claim, request("one"), increment); err != nil {
				t.Fatal(err)
			}
			value, _, err := f.manager.readManifest(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			f.store.mu.Lock()
			if corrupt {
				f.store.objects[value.SnapshotKey] = memoryObject{body: []byte(`{}`), version: "changed"}
			} else {
				delete(f.store.objects, value.SnapshotKey)
			}
			f.store.mu.Unlock()
			if _, err := f.manager.Read(t.Context(), f.id); !errors.Is(err, ErrCorrupt) {
				t.Fatal(err)
			}
			if _, err := f.manager.Execute(t.Context(), f.claim, request("two"), increment); !errors.Is(err, ErrCorrupt) {
				t.Fatal(err)
			}
		})
	}
}

func TestLegacyReceiptBudgetMigratesAndPreservesOldReplay(t *testing.T) {
	f := newFixture(t)
	state := snapshot{Schema: 1, ID: f.id, Version: 1, Data: json.RawMessage(`{"count":1}`), Receipts: make(map[string]receipt)}
	for i := range api.MaxDurableEntityReceipts {
		state.Receipts[fmt.Sprint(i)] = receipt{Fingerprint: digest(request("unused").Payload), Result: json.RawMessage(`1`), Version: 1}
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	value, version, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	value.Version, value.SnapshotKey, value.SnapshotHash = 1, f.id.prefix()+"snapshots/"+uuid.NewString()+".json", digest(body)
	value.StorageUsage = nil // Original manifests did not carry root accounting.
	if _, err := f.store.Put(t.Context(), value.SnapshotKey, body, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.putManifest(t.Context(), value, version); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("new"), increment); err != nil {
		t.Fatal(err)
	}
	collectAll(t, f.manager, f.claim)
	result, err := f.manager.Execute(t.Context(), f.claim, request("0"), increment)
	if err != nil || !result.Replayed {
		t.Fatal(result, err)
	}
	assertCount(t, t.Context(), f.manager, f.id, 2, 2)
	value, _, err = f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	state, err = f.manager.readSnapshot(t.Context(), value)
	if err != nil || state.Schema != 2 || state.LegacyReceipts == nil || len(state.Receipts) != 0 {
		t.Fatal(state, err)
	}
}

func TestInvalidTransitionAndOversizeDoNotCommit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		transition Transition
		want       error
	}{
		{"invalid-json", Transition{Data: json.RawMessage(`{`), Result: json.RawMessage(`null`)}, ErrInvalid},
		{"oversize", Transition{Data: json.RawMessage(`"` + strings.Repeat("x", api.MaxDurableEntitySnapshotBytes) + `"`), Result: json.RawMessage(`null`)}, ErrLimit},
		{"invalid-alarm", Transition{Data: json.RawMessage(`{}`), Result: json.RawMessage(`null`), AlarmAt: &time.Time{}}, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.manager.Execute(t.Context(), f.claim, request("bad"), func(context.Context, View) (Transition, error) { return tc.transition, nil }); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			assertCount(t, t.Context(), f.manager, f.id, 0, 0)
		})
	}
}

func TestCancelledWaiterDoesNotRunOrRetainEntityLock(t *testing.T) {
	f := newFixture(t)
	unlock, err := f.manager.lock(t.Context(), f.id.prefix())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.manager.Execute(ctx, f.claim, request("cancelled"), increment); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unlock()
	if len(f.manager.locks) != 0 {
		t.Fatal("cancelled waiter leaked a lock")
	}
}

func TestOpenRejectsIgnoredConditionalWrites(t *testing.T) {
	s := newMemoryStore()
	bad := wrappedStore{ObjectStore: s, put: func(ctx context.Context, key string, body []byte, _ string) (string, error) {
		s.mu.Lock()
		old, exists := s.objects[key]
		s.mu.Unlock()
		version := ""
		if exists {
			version = old.version
		}
		return s.Put(ctx, key, body, version)
	}}
	if _, err := Open(t.Context(), bad, Options{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}
