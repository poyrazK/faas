// adr: 638
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *memoryStore) ListEntityPrefixes(ctx context.Context, prefix, cursor string, limit int32) (EntityPrefixPage, error) {
	if err := ctx.Err(); err != nil {
		return EntityPrefixPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for key := range s.objects {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		name, _, found := strings.Cut(strings.TrimPrefix(key, prefix), "/")
		if found && prefix+name+"/" > cursor {
			seen[prefix+name+"/"] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	page := EntityPrefixPage{Prefixes: keys}
	if len(keys) > int(limit) {
		page.Prefixes = keys[:limit]
		page.NextCursor = page.Prefixes[len(page.Prefixes)-1]
	}
	return page, nil
}

func scheduleTestAlarm(t *testing.T, f fixture) Alarm {
	t.Helper()
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(0, f.clock.Load())
	_, err := f.manager.Invoke(t.Context(), f.id, "caller", request("schedule"), func(ctx context.Context, view View) (Transition, error) {
		transition, err := increment(ctx, view)
		transition.AlarmAt = &at
		return transition, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return Alarm{Entity: f.id, Version: 1, At: at}
}

func consumeTestAlarm(ctx context.Context, view View) (Transition, error) {
	transition, err := increment(ctx, view)
	transition.AlarmAt = nil
	return transition, err
}

func TestAlarmDiscoveryPaginatesEntitiesWithoutScanningSnapshots(t *testing.T) {
	f := newFixture(t)
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(0, f.clock.Load())
	for i := range 11 {
		id := f.id
		id.Key = fmt.Sprintf("due-%d", i)
		if _, err := f.manager.Invoke(t.Context(), id, "caller", request("schedule"), func(context.Context, View) (Transition, error) {
			return Transition{Data: json.RawMessage(`{}`), Result: json.RawMessage(`null`), AlarmAt: &now}, nil
		}); err != nil {
			t.Fatal(err)
		}
		// Retained snapshots remain under the same directory, regardless of count.
		for j := range 20 {
			if _, err := f.store.Put(t.Context(), id.prefix()+fmt.Sprintf("snapshots/unused-%d", j), []byte(`{}`), ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	future := now.Add(time.Hour)
	id := f.id
	id.Key = "future"
	if _, err := f.manager.Invoke(t.Context(), id, "caller", request("schedule"), func(context.Context, View) (Transition, error) {
		return Transition{Data: json.RawMessage(`{}`), Result: json.RawMessage(`null`), AlarmAt: &future}, nil
	}); err != nil {
		t.Fatal(err)
	}
	restarted := openManager(t, f.store, f.clock)
	cursor, pages, due := "", 0, 0
	for {
		page, err := restarted.ScanDueAlarms(t.Context(), cursor)
		if err != nil || page.Failed != 0 || len(page.Alarms) > api.DurableEntityAlarmScanPageSize {
			t.Fatalf("discovery = %+v %v", page, err)
		}
		for _, alarm := range page.Alarms {
			if alarm.Version != 1 || !alarm.At.Equal(now) {
				t.Fatalf("unexpected alarm %+v", alarm)
			}
		}
		due += len(page.Alarms)
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if due != 11 || pages != 2 {
		t.Fatalf("due=%d pages=%d; snapshots amplified discovery", due, pages)
	}
}

func TestAlarmCommitReplayAndReservedRequestIdentity(t *testing.T) {
	f := newFixture(t)
	alarm := scheduleTestAlarm(t, f)
	result, err := f.manager.InvokeAlarm(t.Context(), alarm, "worker", consumeTestAlarm)
	if err != nil || result.Version != 2 || result.Replayed {
		t.Fatalf("alarm commit = %+v %v", result, err)
	}
	restarted := openManager(t, f.store, f.clock)
	result, err = restarted.InvokeAlarm(t.Context(), alarm, "replacement", func(context.Context, View) (Transition, error) {
		t.Fatal("committed alarm re-executed")
		return Transition{}, nil
	})
	if err != nil || !result.Replayed || result.Version != 2 {
		t.Fatalf("alarm replay = %+v %v", result, err)
	}
	view, err := restarted.Read(t.Context(), f.id)
	if err != nil || view.AlarmAt != nil {
		t.Fatalf("alarm was not cleared: %+v %v", view, err)
	}
	if _, err := restarted.Invoke(t.Context(), f.id, "caller", AlarmRequest(alarm), increment); !errors.Is(err, ErrInvalid) {
		t.Fatal("ordinary caller could forge alarm identity", err)
	}
	assertCount(t, t.Context(), restarted, f.id, 2, 2)
}

func TestAlarmReplacementCancellationAndInterveningTransition(t *testing.T) {
	for _, kind := range []string{"clear", "replace", "preserve"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			alarm := scheduleTestAlarm(t, f)
			if _, err := f.manager.Invoke(t.Context(), f.id, "caller", request("change"), func(ctx context.Context, view View) (Transition, error) {
				transition, err := increment(ctx, view)
				switch kind {
				case "clear":
					transition.AlarmAt = nil
				case "replace":
					at := alarm.At.Add(time.Hour)
					transition.AlarmAt = &at
				}
				return transition, err
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.InvokeAlarm(t.Context(), alarm, "worker", func(context.Context, View) (Transition, error) {
				t.Fatal("obsolete alarm reached handler")
				return Transition{}, nil
			}); !errors.Is(err, ErrAlarmObsolete) {
				t.Fatalf("obsolete alarm = %v", err)
			}
			page, err := f.manager.ScanDueAlarms(t.Context(), "")
			if err != nil {
				t.Fatal(err)
			}
			if kind == "preserve" {
				if len(page.Alarms) != 1 || page.Alarms[0].Version != 2 {
					t.Fatalf("preserved deadline not rediscovered: %+v", page)
				}
				if _, err := f.manager.InvokeAlarm(t.Context(), page.Alarms[0], "worker", consumeTestAlarm); err != nil {
					t.Fatal(err)
				}
			} else if len(page.Alarms) != 0 {
				t.Fatalf("cleared/future alarm is due: %+v", page)
			}
		})
	}
}

func TestAlarmHandlerFailureAndLostCommitAcknowledgement(t *testing.T) {
	f := newFixture(t)
	alarm := scheduleTestAlarm(t, f)
	if _, err := f.manager.InvokeAlarm(t.Context(), alarm, "worker", func(context.Context, View) (Transition, error) {
		return Transition{}, errors.New("handler unavailable")
	}); err == nil {
		t.Fatal("failed handler acknowledged")
	}
	view, err := f.manager.Read(t.Context(), f.id)
	if err != nil || view.Version != 1 || view.AlarmAt == nil {
		t.Fatal("failed handler consumed alarm", view, err)
	}
	f.manager.store = wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, version string) (string, error) {
		next, err := f.store.Put(ctx, key, body, version)
		var value manifest
		if err == nil && strings.HasSuffix(key, "/manifest.json") && json.Unmarshal(body, &value) == nil && value.Version == 2 {
			return "", errors.New("discarded accepted response")
		}
		return next, err
	}}
	if _, err := f.manager.InvokeAlarm(t.Context(), alarm, "worker", consumeTestAlarm); !errors.Is(err, ErrUncertain) {
		t.Fatalf("lost alarm commit response = %v", err)
	}
	restarted := openManager(t, f.store, f.clock)
	result, err := restarted.InvokeAlarm(t.Context(), alarm, "replacement", consumeTestAlarm)
	if err != nil || !result.Replayed || result.Version != 2 {
		t.Fatalf("uncertain alarm recovery = %+v %v", result, err)
	}
	assertCount(t, t.Context(), restarted, f.id, 2, 2)
}

func TestAlarmConcurrentWorkersAndCorruptEntityIsolation(t *testing.T) {
	f := newFixture(t)
	alarm := scheduleTestAlarm(t, f)
	other := openManager(t, f.store, f.clock)
	var calls atomic.Int64
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager := f.manager
			if i%2 == 0 {
				manager = other
			}
			_, err := manager.InvokeAlarm(t.Context(), alarm, fmt.Sprint(i), func(ctx context.Context, view View) (Transition, error) {
				calls.Add(1)
				return consumeTestAlarm(ctx, view)
			})
			if err != nil && !errors.Is(err, ErrBusy) && !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("alarm handler ran %d times", calls.Load())
	}
	bad := f.id
	bad.Key = "corrupt"
	if _, err := f.store.Put(t.Context(), bad.prefix()+"manifest.json", []byte(`{}`), ""); err != nil {
		t.Fatal(err)
	}
	page, err := other.ScanDueAlarms(t.Context(), "")
	if err != nil || page.Failed != 1 || len(page.Alarms) != 0 {
		t.Fatalf("corrupt entity stalled discovery: %+v %v", page, err)
	}
}

type alarmListingFixture struct {
	ObjectStore
	page EntityPrefixPage
}

func (s alarmListingFixture) ListEntityPrefixes(context.Context, string, string, int32) (EntityPrefixPage, error) {
	return s.page, nil
}

func TestAlarmDiscoveryRejectsUnsupportedAndInvalidPages(t *testing.T) {
	f := newFixture(t)
	f.manager.store = wrappedStore{ObjectStore: f.store}
	if err := f.manager.CheckAlarmDiscovery(t.Context()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported listing = %v", err)
	}
	for _, page := range []EntityPrefixPage{
		{Prefixes: make([]string, api.DurableEntityAlarmScanPageSize+1)},
		{NextCursor: "cursor-without-entries"},
		{NextCursor: strings.Repeat("x", api.MaxObjectS3ListCursorBytes+1)},
	} {
		f.manager.store = alarmListingFixture{ObjectStore: f.store, page: page}
		if err := f.manager.CheckAlarmDiscovery(t.Context()); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("invalid listing accepted: %+v %v", page, err)
		}
	}
	f.manager.store = alarmListingFixture{ObjectStore: f.store, page: EntityPrefixPage{Prefixes: []string{"outside/entity/data/"}}}
	page, err := f.manager.ScanDueAlarms(t.Context(), "")
	if err != nil || page.Failed != 1 || len(page.Alarms) != 0 {
		t.Fatalf("outside prefix accepted = %+v %v", page, err)
	}
}
