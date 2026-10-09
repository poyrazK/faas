// adr: 733
package imaged

// Property test for ADR-733 encryption at rest. Random interleavings of
// imaged passes (whole, or one phase at a time, since imaged's phases
// run in sequence and a later phase can hide an earlier one's mistake), fork requests, claims, restores, cancellations and clock
// jumps must keep these properties after every step:
//
//  1. A fork that schedd has claimed (restoring or running) on a ready
//     capture finds the plaintext memory on storage.
//  2. The row's plaintext_state matches storage: present/staged ⇒ the
//     plaintext exists; absent ⇒ it does not.
//  3. An encrypted ready capture has its sealed key and encrypted memory.
//  4. An expired capture has no objects and no sealed key.
//
// And, once the walk stops, imaged settles in at most two passes: an active
// fork's capture is staged and the fork is claimable; otherwise the
// plaintext is gone.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type crashWalk struct {
	t     *testing.T
	fx    *crashCryptoFixture
	keys  []string
	forks map[string]state.AppFork // by ID, latest view
}

func (w *crashWalk) capture() state.CrashCapture {
	c, err := w.fx.store.CrashCaptureForRestore(context.Background(), w.fx.capture.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

func (w *crashWalk) exists(key string) bool {
	_, ok := w.fx.read(w.t, key)
	return ok
}

func (w *crashWalk) record(f state.AppFork, err error) {
	if err == nil {
		w.forks[f.ID] = f
	}
}

func (w *crashWalk) step(op byte) string {
	ctx := context.Background()
	store := w.fx.store
	now := w.fx.clock
	switch op % 12 {
	case 0:
		w.fx.loop.tendCrashCaptures(ctx, now)
		return "imaged pass"
	case 8:
		w.fx.loop.expireCrashCaptures(ctx, w.fx.be, now)
		return "imaged expire phase"
	case 9:
		w.fx.loop.encryptCrashCaptures(ctx, w.fx.be, w.fx.loop.handler.secretboxIdentities, now)
		return "imaged encrypt phase"
	case 10:
		w.fx.loop.purgeCrashPlaintext(ctx, w.fx.be, now)
		return "imaged purge phase"
	case 11:
		w.fx.loop.stageCrashCaptures(ctx, w.fx.be, w.fx.loop.handler.secretboxIdentities, now)
		return "imaged stage phase"
	case 1:
		w.record(store.CreateAppFork(ctx, state.CreateAppForkParams{
			AccountID: w.fx.acctID, AppID: w.fx.appID, RequestedBy: "user:prop", TTLSeconds: 600,
			MaxPerApp: 1, MaxPerAccount: 2, CreatedAt: now, CrashCaptureID: w.fx.capture.ID,
		}))
		return "create fork"
	case 2:
		f, err := store.ClaimNextAppFork(ctx, "sched", now, time.Minute)
		w.record(f, err)
		if err == nil && w.capture().Status == state.CrashCaptureReady && !w.exists(w.keys[0]) {
			w.t.Fatalf("claimed fork %s on capture %s with no plaintext memory", f.ID, w.capture().PlaintextState)
		}
		return "claim fork"
	case 3:
		for _, f := range w.forks {
			if f.Status == state.AppForkRestoring {
				w.record(store.MarkAppForkRunning(ctx, f.ID, *f.LeaseToken, w.fx.capture.ID, "fork-ins", now))
			}
		}
		return "fork running"
	case 4:
		for _, f := range w.forks {
			if f.Status == state.AppForkRestoring || f.Status == state.AppForkRunning {
				w.record(store.FinishAppFork(ctx, state.FinishAppForkParams{
					ForkID: f.ID, LeaseToken: *f.LeaseToken, Status: state.AppForkCancelled, FinishedAt: now,
				}))
			}
		}
		return "finish fork"
	case 5:
		for _, f := range w.forks {
			w.record(store.RequestAppForkCancellation(ctx, w.fx.acctID, w.fx.appID, f.ID, now))
		}
		return "cancel queued fork"
	case 6:
		w.fx.clock = now.Add(17 * time.Minute)
		if _, err := store.ExpireUnclaimedAppForks(ctx, w.fx.clock); err != nil {
			w.t.Fatal(err)
		}
		w.refreshForks()
		return "clock +17m"
	default:
		w.fx.clock = now.Add(time.Second)
		return "clock +1s"
	}
}

func (w *crashWalk) refreshForks() {
	for id := range w.forks {
		f, err := w.fx.store.AppForkByID(context.Background(), w.fx.acctID, w.fx.appID, id)
		if err != nil {
			w.t.Fatal(err)
		}
		w.forks[id] = f
	}
}

func (w *crashWalk) activeFork() (state.AppFork, bool) {
	for _, f := range w.forks {
		if f.Status == state.AppForkQueued || f.Status == state.AppForkRestoring || f.Status == state.AppForkRunning {
			return f, true
		}
	}
	return state.AppFork{}, false
}

func (w *crashWalk) checkInvariants(after string) {
	c := w.capture()
	plain := w.exists(w.keys[0])
	switch {
	case c.Status == state.CrashCaptureExpired:
		for _, key := range w.keys {
			if w.exists(key) || w.exists(key+crashCaptureEncryptedSuffix) {
				w.t.Fatalf("after %s: %s survived expiry", after, key)
			}
		}
		if c.SealedKey != nil {
			w.t.Fatalf("after %s: expired capture kept its sealed key", after)
		}
		return
	case c.Status != state.CrashCaptureReady:
		w.t.Fatalf("after %s: capture left ready: %s", after, c.Status)
	}
	if (c.PlaintextState == state.CrashPlaintextPresent || c.PlaintextState == state.CrashPlaintextStaged) && !plain {
		w.t.Fatalf("after %s: row says %s but the plaintext is gone", after, c.PlaintextState)
	}
	if c.PlaintextState == state.CrashPlaintextAbsent && plain {
		w.t.Fatalf("after %s: row says absent but the plaintext exists", after)
	}
	if c.PlaintextState != state.CrashPlaintextPresent && (len(c.SealedKey) == 0 || !w.exists(w.keys[0]+crashCaptureEncryptedSuffix)) {
		w.t.Fatalf("after %s: %s capture without its sealed key or encrypted memory", after, c.PlaintextState)
	}
	for _, f := range w.forks {
		if (f.Status == state.AppForkRestoring || f.Status == state.AppForkRunning) && !plain {
			w.t.Fatalf("after %s: %s fork %s lost the plaintext (row %s)", after, f.Status, f.ID, c.PlaintextState)
		}
	}
}

// checkSettles runs imaged passes with no other activity.
func (w *crashWalk) checkSettles() {
	for range 2 {
		w.fx.clock = w.fx.clock.Add(time.Second)
		w.fx.loop.tendCrashCaptures(context.Background(), w.fx.clock)
		w.checkInvariants("settling pass")
	}
	c := w.capture()
	if c.Status != state.CrashCaptureReady {
		return
	}
	fork, active := w.activeFork()
	switch {
	case active && c.PlaintextState != state.CrashPlaintextStaged:
		w.t.Fatalf("settled with an active fork but the capture is %s", c.PlaintextState)
	case active && fork.Status == state.AppForkQueued && fork.CancelRequested == nil:
		if _, err := w.fx.store.ClaimNextAppFork(context.Background(), "sched", w.fx.clock, time.Minute); err != nil {
			w.t.Fatalf("settled but the queued fork is not claimable: %v", err)
		}
	case !active && c.PlaintextState != state.CrashPlaintextAbsent:
		w.t.Fatalf("settled with no active fork but the capture is %s", c.PlaintextState)
	}
}

func FuzzCrashCapturePlaintextLifecycle(f *testing.F) {
	f.Add([]byte{0, 1, 0, 2, 3, 0, 4, 0})
	f.Add([]byte{1, 9, 11, 2, 10, 3, 11, 4, 10})
	f.Add([]byte{1, 2, 0, 0, 2, 3, 6, 6, 0, 4, 0})
	f.Add([]byte{1, 0, 5, 0, 1, 0, 2, 6, 6, 6, 6, 6, 6, 6, 6, 6, 0})
	f.Add([]byte{0, 1, 0, 2, 3, 6, 6, 6, 6, 6, 6, 6, 6, 0, 2, 0})
	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 64 {
			ops = ops[:64]
		}
		fx := newCrashCryptoFixture(t, true)
		w := &crashWalk{t: t, fx: fx, keys: crashCaptureKeys(fx.capture), forks: map[string]state.AppFork{}}
		for _, op := range ops {
			w.checkInvariants(w.step(op))
		}
		w.checkSettles()
	})
}

// TestCrashCapturePlaintextLifecycleWalks runs a fixed set of long walks as
// part of `make test`, so the property is checked without -fuzz.
func TestCrashCapturePlaintextLifecycleWalks(t *testing.T) {
	seed := uint32(733)
	for i := range 200 {
		ops := make([]byte, 48)
		for j := range ops {
			seed = seed*1664525 + 1013904223
			ops[j] = byte(seed >> 24)
		}
		t.Run("", func(t *testing.T) {
			fx := newCrashCryptoFixture(t, true)
			w := &crashWalk{t: t, fx: fx, keys: crashCaptureKeys(fx.capture), forks: map[string]state.AppFork{}}
			for _, op := range ops {
				w.checkInvariants(w.step(op))
			}
			w.checkSettles()
		})
		_ = i
	}
}
