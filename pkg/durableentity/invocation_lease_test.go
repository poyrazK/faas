// adr: 712
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type invocationLeaseFixture struct {
	m        *Manager
	store    ObjectStore
	clock    *atomic.Int64
	initial  time.Time
	id       ID
	renewed  chan time.Time
	fail     atomic.Bool
	mode     string
	attempts atomic.Int64
}

func managedLeaseFixture(t *testing.T, store ObjectStore, mode string) *invocationLeaseFixture {
	t.Helper()
	f := &invocationLeaseFixture{store: store, clock: &atomic.Int64{}, initial: time.Now().UTC(), id: ID{AccountID: "lease-account", AppID: "lease-app", Namespace: "counters", Key: "renewed"}, renewed: make(chan time.Time, 8), mode: mode}
	f.clock.Store(f.initial.UnixNano())
	f.fail.Store(true)
	m, err := Open(t.Context(), wrappedStore{ObjectStore: store, put: f.put}, Options{LeaseDuration: api.MaxDurableEntityLease, InvocationLeaseDuration: api.DurableEntityInvocationLease, InvocationRenewInterval: 10 * time.Millisecond, Now: func() time.Time { return time.Unix(0, f.clock.Load()) }})
	if err != nil {
		t.Fatal(err)
	}
	f.m = m
	return f
}

func (f *invocationLeaseFixture) put(ctx context.Context, key string, body []byte, etag string) (string, error) {
	var value manifest
	renewal := strings.HasSuffix(key, "/manifest.json") && json.Unmarshal(body, &value) == nil && value.OwnerID != "" && value.Version == 0 && value.ExpiresAt.After(f.initial.Add(api.DurableEntityInvocationLease))
	if renewal {
		f.attempts.Add(1)
		if f.mode == "conflict" && f.fail.Swap(false) {
			return "", ErrConflict
		}
		if f.mode == "partition" {
			<-ctx.Done()
			return "", ctx.Err()
		}
		if f.mode == "rejected-uncertain" {
			return "", errors.New("renewal response unavailable")
		}
	}
	version, err := f.store.Put(ctx, key, body, etag)
	if renewal && err == nil {
		if f.mode == "accepted-uncertain" {
			return "", errors.New("renewal acknowledgement discarded")
		}
		select {
		case f.renewed <- value.ExpiresAt:
		default:
		}
	}
	return version, err
}

func (f *invocationLeaseFixture) waitRenewal(ctx context.Context, expiry time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case observed := <-f.renewed:
			if !observed.Before(expiry) {
				return nil
			}
		}
	}
}

func exerciseManagedLease(t *testing.T, store ObjectStore, mode string) {
	t.Helper()
	f := managedLeaseFixture(t, store, mode)
	ctx, cancel := context.WithTimeout(t.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	result, err := f.m.Invoke(ctx, f.id, "caller", request("one"), func(ctx context.Context, view View) (Transition, error) {
		for range 2 {
			f.clock.Add(int64(20 * time.Second))
			if err := f.waitRenewal(ctx, time.Unix(0, f.clock.Load()).Add(api.DurableEntityInvocationLease)); err != nil {
				return Transition{}, err
			}
		}
		// The initial lease has expired, but confirmed renewals keep competitors
		// out without changing the business state or original ownership epoch.
		if _, err := f.m.Acquire(ctx, f.id, "competitor"); !errors.Is(err, ErrBusy) {
			return Transition{}, errors.New("renewed entity admitted another owner")
		}
		return increment(ctx, view)
	})
	if err != nil || result.Version != 1 || f.attempts.Load() < 2 {
		t.Fatal("managed lease did not preserve publication", result, err, f.attempts.Load())
	}
	assertCount(t, ctx, f.m, f.id, 1, 1)
	replay, err := f.m.Invoke(ctx, f.id, "replacement", request("one"), func(context.Context, View) (Transition, error) {
		t.Error("managed receipt replay ran the handler")
		return Transition{}, ErrInvalid
	})
	if err != nil || !replay.Replayed || string(replay.Value) != string(result.Value) {
		t.Fatal("managed lease lost its original receipt", replay, err)
	}
	value, _, err := f.m.readManifest(ctx, f.id)
	if err != nil || value.OwnerID != "" || !value.ExpiresAt.IsZero() {
		t.Fatal("renewal raced release cleanup", value.OwnerID, err)
	}
}

func TestManagedInvocationRenewsPastInitialExpiryAndRetriesDefiniteConflict(t *testing.T) {
	for _, mode := range []string{"success", "conflict"} {
		t.Run(mode, func(t *testing.T) { exerciseManagedLease(t, newMemoryStore(), mode) })
	}
}

func TestManagedInvocationUncertainRenewalAndPartitionCancelPublication(t *testing.T) {
	for _, mode := range []string{"accepted-uncertain", "rejected-uncertain", "partition"} {
		t.Run(mode, func(t *testing.T) {
			f := managedLeaseFixture(t, newMemoryStore(), mode)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := f.m.Invoke(ctx, f.id, "caller", request("one"), func(ctx context.Context, view View) (Transition, error) {
				f.clock.Add(int64(20 * time.Second))
				<-ctx.Done()
				// A late guest response must not publish, even if it ignores cancellation.
				return increment(ctx, view)
			})
			if !errors.Is(err, ErrUncertain) || result.Version != 0 || f.attempts.Load() != 1 {
				t.Fatal("unknown renewal was retried or published state", result, err, f.attempts.Load())
			}
			assertCount(t, t.Context(), f.m, f.id, 0, 0)
			replacement := openManager(t, f.store, f.clock)
			result, err = replacement.Invoke(t.Context(), f.id, "replacement", request("one"), increment)
			if err != nil || result.Replayed || result.Version != 1 {
				t.Fatal("renewal failure poisoned the original request", result, err)
			}
			assertCount(t, t.Context(), replacement, f.id, 1, 1)
		})
	}
}

func TestManagedInvocationTakeoverCancelsLateGuest(t *testing.T) {
	f := managedLeaseFixture(t, newMemoryStore(), "success")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := f.m.Invoke(ctx, f.id, "obsolete", request("late"), func(ctx context.Context, view View) (Transition, error) {
		f.clock.Add(int64(api.DurableEntityInvocationLease + time.Second))
		replacement := openManager(t, f.store, f.clock)
		if _, err := replacement.Invoke(t.Context(), f.id, "replacement", request("winner"), increment); err != nil {
			return Transition{}, err
		}
		<-ctx.Done()
		return increment(ctx, view)
	})
	if !errors.Is(err, ErrStaleOwner) {
		t.Fatal("takeover did not cancel the obsolete guest", err)
	}
	assertCount(t, t.Context(), f.m, f.id, 1, 1)
}

func TestManagedInvocationRenewsDuringImmutableUpload(t *testing.T) {
	store := newMemoryStore()
	f := managedLeaseFixture(t, store, "success")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	f.m.store = wrappedStore{ObjectStore: store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		if strings.Contains(key, "/snapshots/") {
			f.clock.Add(int64(20 * time.Second))
			if err := f.waitRenewal(ctx, f.initial.Add(50*time.Second)); err != nil {
				return "", err
			}
		}
		return f.put(ctx, key, body, etag)
	}}
	result, err := f.m.Invoke(ctx, f.id, "caller", request("one"), increment)
	if err != nil || result.Version != 1 || f.attempts.Load() == 0 {
		t.Fatal("immutable upload blocked renewal or publication", result, err)
	}
	assertCount(t, ctx, f.m, f.id, 1, 1)
}

func TestManagedInvocationRenewsEarlyAfterSlowAcquisitionAcknowledgement(t *testing.T) {
	store := newMemoryStore()
	f := managedLeaseFixture(t, store, "success")
	f.m.renewInterval = api.DurableEntityInvocationRenewInterval
	var acquired atomic.Bool
	f.m.store = wrappedStore{ObjectStore: store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		version, err := f.put(ctx, key, body, etag)
		if err == nil && strings.HasSuffix(key, "/manifest.json") && !acquired.Swap(true) {
			// The acquisition is stored, but its acknowledgement consumes most
			// of the lease. Waiting the normal ten seconds would lose ownership.
			f.clock.Add(int64(28 * time.Second))
		}
		return version, err
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := f.m.Invoke(ctx, f.id, "caller", request("one"), func(ctx context.Context, view View) (Transition, error) {
		if err := f.waitRenewal(ctx, f.initial.Add(58*time.Second)); err != nil {
			return Transition{}, err
		}
		return increment(ctx, view)
	})
	if err != nil || result.Version != 1 || f.attempts.Load() == 0 {
		t.Fatal("slow acquisition acknowledgement prevented early renewal", result, err)
	}
}

func TestManagedInvocationUncertainCommitReplaysOriginalReceipt(t *testing.T) {
	store := newMemoryStore()
	f := managedLeaseFixture(t, store, "success")
	var lost atomic.Bool
	f.m.store = wrappedStore{ObjectStore: store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		version, err := f.put(ctx, key, body, etag)
		var value manifest
		if err == nil && strings.HasSuffix(key, "/manifest.json") && json.Unmarshal(body, &value) == nil && value.Version == 1 && !lost.Swap(true) {
			return "", errors.New("commit acknowledgement discarded")
		}
		return version, err
	}}
	if _, err := f.m.Invoke(t.Context(), f.id, "caller", request("one"), increment); !errors.Is(err, ErrUncertain) {
		t.Fatal("lost publication acknowledgement was not uncertain", err)
	}
	result, err := f.m.Invoke(t.Context(), f.id, "retry", request("one"), func(context.Context, View) (Transition, error) {
		t.Error("uncertain managed commit repeated the handler")
		return Transition{}, ErrInvalid
	})
	if err != nil || !result.Replayed || result.Version != 1 {
		t.Fatal("uncertain managed commit lost the original receipt", result, err)
	}
	assertCount(t, t.Context(), f.m, f.id, 1, 1)
}

func TestManagedAlarmRenewsAndReplaysClearedDeadline(t *testing.T) {
	store := newMemoryStore()
	f := managedLeaseFixture(t, store, "success")
	at := f.initial.Add(-time.Second)
	if _, err := f.m.Invoke(t.Context(), f.id, "schedule", request("schedule"), func(ctx context.Context, view View) (Transition, error) {
		transition, err := increment(ctx, view)
		transition.AlarmAt = &at
		return transition, err
	}); err != nil {
		t.Fatal(err)
	}
	f.m.store = wrappedStore{ObjectStore: store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		version, err := store.Put(ctx, key, body, etag)
		var value manifest
		if err == nil && strings.HasSuffix(key, "/manifest.json") && json.Unmarshal(body, &value) == nil && value.Version == 1 && value.ExpiresAt.After(f.initial.Add(api.DurableEntityInvocationLease)) {
			select {
			case f.renewed <- value.ExpiresAt:
			default:
			}
		}
		return version, err
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	alarm := Alarm{Entity: f.id, Version: 1, At: at}
	result, err := f.m.InvokeAlarm(ctx, alarm, "alarm-worker", func(ctx context.Context, view View) (Transition, error) {
		f.clock.Add(int64(20 * time.Second))
		if err := f.waitRenewal(ctx, f.initial.Add(50*time.Second)); err != nil {
			return Transition{}, err
		}
		return consumeTestAlarm(ctx, view)
	})
	if err != nil || result.Version != 2 {
		t.Fatal("alarm renewal failed", result, err)
	}
	replay, err := f.m.InvokeAlarm(ctx, alarm, "replacement", consumeTestAlarm)
	if err != nil || !replay.Replayed || replay.Version != result.Version {
		t.Fatal("renewed alarm did not preserve its receipt", replay, err)
	}
	assertCount(t, ctx, f.m, f.id, 2, 2)
}

func TestManagedInvocationCancellationAfterCommitPreservesAcknowledgement(t *testing.T) {
	store := newMemoryStore()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := managedLeaseFixture(t, store, "success")
	f.m.store = wrappedStore{ObjectStore: store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		version, err := store.Put(ctx, key, body, etag)
		var value manifest
		if err == nil && strings.HasSuffix(key, "/manifest.json") && json.Unmarshal(body, &value) == nil && value.Version == 1 {
			cancel()
		}
		return version, err
	}}
	result, err := f.m.Invoke(ctx, f.id, "caller", request("one"), increment)
	if err != nil || result.Version != 1 {
		t.Fatal("cancellation invalidated an acknowledged commit", result, err)
	}
	assertCount(t, t.Context(), f.m, f.id, 1, 1)
}

func TestManagedInvocationOptionsPreserveMaintenanceLease(t *testing.T) {
	f := managedLeaseFixture(t, newMemoryStore(), "success")
	claim, err := f.m.Acquire(t.Context(), f.id, "maintenance")
	if err != nil || !claim.ExpiresAt.Equal(f.initial.Add(api.MaxDurableEntityLease)) {
		t.Fatal("managed invocations changed direct ownership", claim.ExpiresAt, err)
	}
	for _, opts := range []Options{
		{InvocationLeaseDuration: -time.Second},
		{InvocationLeaseDuration: api.MaxDurableEntityLease + time.Second},
		{InvocationRenewInterval: time.Second},
		{InvocationLeaseDuration: time.Second, InvocationRenewInterval: time.Second / 2},
		{InvocationLeaseDuration: time.Second, InvocationRenewInterval: -time.Second},
	} {
		store := wrappedStore{ObjectStore: newMemoryStore(), put: func(context.Context, string, []byte, string) (string, error) {
			t.Error("invalid renewal options reached the provider")
			return "", ErrInvalid
		}}
		if _, err := Open(t.Context(), store, opts); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid renewal timing was accepted", opts, err)
		}
	}
}
