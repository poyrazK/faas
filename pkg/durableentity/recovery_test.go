// adr: 936
package durableentity

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func exhaustedRecoveryFixture(t *testing.T, target string) (fixture, Recovery) {
	t.Helper()
	f := newFixture(t)
	if target == "alarm" {
		alarm := scheduleTestAlarm(t, f)
		for range api.MaxDurableEntityAlarmAttempts {
			if _, err := f.manager.InvokeAlarm(t.Context(), alarm, "worker", func(context.Context, View) (Transition, error) { return Transition{}, errors.New("handler failed") }); err == nil {
				t.Fatal("failed alarm succeeded")
			}
			status, err := f.manager.InspectAlarm(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			f.clock.Store(status.NextAttemptAt.UnixNano())
		}
	} else {
		if _, err := f.manager.Execute(t.Context(), f.claim, request("confirmation"), withOutbox(outboxIntent(), outboxIntent())); err != nil {
			t.Fatal(err)
		}
		message := pendingOutbox(t, f.manager, f.id, 1, 2).Messages[0]
		f.manager.lease = api.MaxDurableEntityLease
		for attempt := 1; attempt <= api.MaxDurableEntityOutboxAttempts; attempt++ {
			renewed, err := f.manager.Renew(t.Context(), f.claim)
			if err != nil {
				t.Fatal(err)
			}
			f.claim = renewed
			if _, err := f.manager.ReserveOutbox(t.Context(), f.claim, message.ID); err != nil {
				t.Fatal(err)
			}
			if attempt < api.MaxDurableEntityOutboxAttempts {
				f.clock.Add(int64(outboxRetryDelay(attempt)))
			}
		}
		if err := f.manager.Release(t.Context(), f.claim); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := f.manager.Inspect(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	request := Recovery{Version: observed.Version, Revision: observed.RecoveryRevision, MessageID: observed.Outbox.HeadID}
	if target == "alarm" {
		request.AlarmAt = &observed.Alarm.Alarm.At
	}
	return f, request
}

func TestRecoveryPreservesCommittedStateAndRejectsRepeat(t *testing.T) {
	for _, target := range []string{"alarm", "outbox"} {
		t.Run(target, func(t *testing.T) {
			f, request := exhaustedRecoveryFixture(t, target)
			before, _, err := f.manager.readManifest(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := f.manager.readSnapshot(t.Context(), before)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.manager.RetryExhausted(t.Context(), f.id, request); err != nil {
				t.Fatal(err)
			}
			after, _, err := f.manager.readManifest(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			current, err := f.manager.readSnapshot(t.Context(), after)
			if err != nil || !reflect.DeepEqual(snapshot, current) || after.SnapshotKey != before.SnapshotKey || after.SnapshotHash != before.SnapshotHash || after.Version != before.Version || after.Generation != before.Generation || after.Epoch != before.Epoch || after.OwnerID != before.OwnerID || !sameUsage(*after.StorageUsage, *before.StorageUsage) {
				t.Fatal("recovery changed business authority", after, err)
			}
			out, err := f.manager.Inspect(t.Context(), f.id)
			if err != nil || out.RecoveryRevision == request.Revision || target == "alarm" && (out.Alarm.Exhausted || out.Alarm.Attempts != 0) || target == "outbox" && (out.Outbox.Exhausted || out.Outbox.Attempts != 0 || out.Outbox.Pending != 2 || out.Outbox.HeadID != request.MessageID) {
				t.Fatal(out, err)
			}
			if err := f.manager.RetryExhausted(t.Context(), f.id, request); !errors.Is(err, ErrRecoveryObsolete) {
				t.Fatal("repeat recovery reset budget", err)
			}
			claim, err := f.manager.Acquire(t.Context(), f.id, "next-worker")
			if err != nil {
				t.Fatal(err)
			}
			if target == "outbox" {
				reservation, err := f.manager.ReserveOutbox(t.Context(), claim, request.MessageID)
				if err != nil || reservation.Message.ID != request.MessageID {
					t.Fatal(reservation, err)
				}
			} else {
				if err := f.manager.reserveAlarmAttempt(t.Context(), claim, Alarm{Entity: f.id, Version: request.Version, At: *request.AlarmAt}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestRecoveryFencesOwnershipAndUncertainWrites(t *testing.T) {
	for _, kind := range []string{"active-owner", "stale-version", "stale-revision", "changed-target", "acquisition-race", "cas-rejected", "ack-lost"} {
		t.Run(kind, func(t *testing.T) {
			f, request := exhaustedRecoveryFixture(t, "outbox")
			want := ErrRecoveryObsolete
			switch kind {
			case "active-owner":
				if _, err := f.manager.Acquire(t.Context(), f.id, "worker"); err != nil {
					t.Fatal(err)
				}
				out, err := f.manager.Inspect(t.Context(), f.id)
				if err != nil {
					t.Fatal(err)
				}
				request.Revision = out.RecoveryRevision
				want = ErrBusy
			case "stale-version":
				request.Version++
			case "stale-revision":
				request.Revision = strings.Repeat("0", len(request.Revision))
			case "changed-target":
				request.MessageID = "0ca08621-0f2d-4e4a-8e0b-1a52c8f6e670"
			case "acquisition-race", "cas-rejected", "ack-lost":
				intercepted := false
				wrapped := wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
					if !intercepted && strings.HasSuffix(key, "/manifest.json") {
						intercepted = true
						if kind == "cas-rejected" {
							return "", ErrConflict
						}
						if kind == "acquisition-race" {
							if _, err := f.manager.Acquire(ctx, f.id, "concurrent"); err != nil {
								t.Fatal(err)
							}
						}
						if kind == "ack-lost" {
							if _, err := f.store.Put(ctx, key, body, etag); err != nil {
								t.Fatal(err)
							}
							return "", errors.New("private provider acknowledgement lost")
						}
					}
					return f.store.Put(ctx, key, body, etag)
				}}
				manager := openManager(t, wrapped, f.clock)
				want = ErrConflict
				if kind == "ack-lost" {
					want = ErrUncertain
				}
				if err := manager.RetryExhausted(t.Context(), f.id, request); !errors.Is(err, want) {
					t.Fatal(kind, err)
				}
				out, err := f.manager.Inspect(t.Context(), f.id)
				if err != nil || out.Outbox.Exhausted != (kind != "ack-lost") {
					t.Fatal(out, err)
				}
				return
			}
			if err := f.manager.RetryExhausted(t.Context(), f.id, request); !errors.Is(err, want) {
				t.Fatal(kind, err)
			}
		})
	}
}

func TestRecoveryNeverCreatesMissingEntity(t *testing.T) {
	f, request := exhaustedRecoveryFixture(t, "outbox")
	id := f.id
	id.Key = "missing"
	f.store.mu.Lock()
	before := len(f.store.objects)
	f.store.mu.Unlock()
	if err := f.manager.RetryExhausted(t.Context(), id, request); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	if len(f.store.objects) != before {
		t.Fatal("recovery created missing entity")
	}
}

func TestRecoveryRejectsEarlierCycleAtSameBusinessVersion(t *testing.T) {
	f, old := exhaustedRecoveryFixture(t, "outbox")
	if err := f.manager.RetryExhausted(t.Context(), f.id, old); err != nil {
		t.Fatal(err)
	}
	for range api.MaxDurableEntityOutboxAttempts {
		claim, err := f.manager.Acquire(t.Context(), f.id, "worker")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.manager.ReserveOutbox(t.Context(), claim, old.MessageID); err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Release(t.Context(), claim); err != nil {
			t.Fatal(err)
		}
		out, err := f.manager.Inspect(t.Context(), f.id)
		if err != nil {
			t.Fatal(err)
		}
		f.clock.Store(out.Outbox.NextAttemptAt.UnixNano())
	}
	out, err := f.manager.Inspect(t.Context(), f.id)
	if err != nil || out.Version != old.Version || !out.Outbox.Exhausted || out.Outbox.HeadID != old.MessageID {
		t.Fatal(out, err)
	}
	if err := f.manager.RetryExhausted(t.Context(), f.id, old); !errors.Is(err, ErrRecoveryObsolete) {
		t.Fatal("earlier recovery reset another retry cycle", err)
	}
	old.Revision = out.RecoveryRevision
	if err := f.manager.RetryExhausted(t.Context(), f.id, old); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryCorruptSnapshotFailsWithoutReset(t *testing.T) {
	f, request := exhaustedRecoveryFixture(t, "outbox")
	base, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	f.store.mu.Lock()
	delete(f.store.objects, base.SnapshotKey)
	f.store.mu.Unlock()
	if err := f.manager.RetryExhausted(t.Context(), f.id, request); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	after, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil || after.Revision != base.Revision || after.OutboxDelivery == nil || after.OutboxDelivery.Attempts != api.MaxDurableEntityOutboxAttempts {
		t.Fatal("corrupt state was modified", after, err)
	}
}

func TestRecoveryAfterExpiryRejectsPredecessorAuthority(t *testing.T) {
	f, request := exhaustedRecoveryFixture(t, "outbox")
	oldClaim, err := f.manager.Acquire(t.Context(), f.id, "old-owner")
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	oldAttempt := before.OutboxDelivery.Token
	out, err := f.manager.Inspect(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	request.Revision = out.RecoveryRevision
	f.clock.Store(oldClaim.ExpiresAt.UnixNano())
	if err := f.manager.RetryExhausted(t.Context(), f.id, request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Renew(t.Context(), oldClaim); !errors.Is(err, ErrStaleOwner) {
		t.Fatal("expired owner retained authority", err)
	}
	nextClaim, err := f.manager.Acquire(t.Context(), f.id, "next-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.AcknowledgeOutbox(t.Context(), nextClaim, request.MessageID, oldAttempt); !errors.Is(err, ErrOutboxObsolete) {
		t.Fatal("old attempt acknowledgement survived recovery", err)
	}
}
