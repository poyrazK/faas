// adr: 1359
package acceptance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
)

func (f *fixture) holdRunningCheckpoint(t *testing.T, ctx context.Context) (rehearsalresume.Receipt, <-chan error, func()) {
	t.Helper()
	d := f.checkpointContext(ctx, t)
	if d.SuspendResumeID == "" || d.WorkerFaultID != "" {
		t.Fatal("running checkpoint did not arm its distinct pause receipt")
	}
	store := f.store.(rehearsalresume.Store)
	r, err := store.ScenarioSuspendResume(ctx, d.SuspendResumeID)
	if err != nil {
		t.Fatal(err)
	}
	f.recorders["worker"] = r.RecorderSessionID
	if _, err = store.ClaimScenarioSuspendResume(ctx, r.ID); err == nil {
		t.Fatal("broker response substituted for original held relay")
	}
	var conn *rehearsalrecorder.Connection
	conn, err = f.workerSession.Attach(func() { conn.Detach(false) })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	receiptID := r.ID
	go func() { done <- f.workerSession.HoldSuspendCheckpoint(ctx, receiptID, conn) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		r, err = store.ScenarioSuspendResume(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == "armed" {
			return r, done, func() { conn.Detach(false) }
		}
		select {
		case <-deadline.C:
			t.Fatalf("checkpoint stayed %s", r.State)
		case <-tick.C:
		}
	}
}

func (f *fixture) resumeRunningCheckpoint(t *testing.T, r rehearsalresume.Receipt, held <-chan error, ms int64) {
	t.Helper()
	ctx := t.Context()
	store := f.store.(rehearsalresume.Store)
	if _, err := store.ClaimScenarioSuspendResume(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateInstanceState(ctx, r.InstanceID, "frozen"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.workerSession.PrepareSuspend(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.workerSession.RecordSuspended(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if renewed, err := f.store.(state.InvocationLeaseStore).RenewInvocationLease(ctx, r.InvocationID, 1, 60); err != nil || !renewed {
		t.Fatalf("pause interrupted original lease renewal: %v", err)
	}
	if blocked, err := f.store.(rehearsalwork.FaultStore).ScenarioWorkerDispatchBlocked(ctx, r.AppID); err != nil || !blocked {
		t.Fatal("pending original pause did not fence retry dispatch")
	}

	if _, err := f.workerSession.Begin(ctx); err == nil {
		t.Fatal("paused worker admitted an effect")
	}
	if err := f.workerSession.BeginResume(ctx, r.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.workerSession.RecordResumed(ctx, r.ID, ms); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-held:
		t.Fatalf("checkpoint released before publication: %v", err)
	default:
	}
	if err := store.CompleteScenarioSuspendResume(ctx, r.ID); err == nil {
		t.Fatal("unpublished worker completed checkpoint")
	}
	if err := f.store.UpdateInstanceState(ctx, r.InstanceID, "running"); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteScenarioSuspendResume(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-held:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("published resume did not release original response")
	}
}

func TestRunningSuspendPreservesAttemptAndComparesIndependentEffects(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			run := func(duplicate, retry bool, ms int64) *actioncompare.Evidence {
				f := newFeatureFixture(t, backend, false, fixtureFeatures{running: true})
				j := f.claim(t, f.enqueue(t).ID)
				r, held, detach := f.holdRunningCheckpoint(t, t.Context())
				defer detach()
				if r.InvocationID != j.ID || r.InvocationAttempt != 1 || r.InstanceID != j.InstanceID {
					t.Fatal("checkpoint execution identity differs")
				}
				if _, err := f.store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: f.account.ID, AppID: f.apps["worker"].ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/import", Payload: []byte(`{}`), DueAt: time.Now()}); err == nil {
					t.Fatal("concurrent job crossed checkpoint")
				}
				f.resumeRunningCheckpoint(t, r, held, ms)
				current, err := f.store.InvocationByID(t.Context(), j.ID)
				if err != nil || current.State != state.InvocationDispatching || current.Attempts != 1 || current.InstanceID != j.InstanceID {
					t.Fatal("pause manufactured another dispatch")
				}
				f.notify(t)
				if retry {
					if err = f.store.FailInvocation(t.Context(), j.ID, "application requested retry", time.Nanosecond, 3, state.WithInvocationClaim(j)); err != nil {
						t.Fatal(err)
					}
					j = f.claim(t, j.ID)
					d := f.checkpoint(t)
					if d.SuspendResumeID != "" {
						t.Fatal("retry repeated pause")
					}
				}
				if duplicate {
					f.notify(t)
				}
				f.complete(t, j)
				detach()
				s := f.finish(t, "")
				if s.Status != "complete" {
					t.Fatalf("incomplete running-job proof: %+v", s)
				}
				if _, err = actioncompare.ValidateSnapshot(&s); err != nil {
					t.Fatal(err)
				}
				return &actioncompare.Evidence{Version: 1, Status: "complete", Provenance: actioncompare.SimulationProvenance, ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64), Snapshot: &s}
			}
			baseline := run(false, false, 100)
			for _, tc := range []struct {
				name             string
				duplicate, retry bool
				ms               int64
				want             string
			}{{"same", false, false, 200, "passed"}, {"duplicate", true, false, 200, "failed"}, {"actual_retry", false, true, 200, "failed"}, {"budget", false, false, 1001, "failed"}} {
				t.Run(tc.name, func(t *testing.T) {
					candidate := run(tc.duplicate, tc.retry, tc.ms)
					if c := actioncompare.Compare(baseline, candidate); c.Status != tc.want {
						t.Fatalf("want %s: %+v", tc.want, c)
					}
				})
			}
			broken := *baseline.Snapshot
			broken.SimulatedAttempts = append([]api.ScenarioSimulationAttempt(nil), broken.SimulatedAttempts...)
			for i, a := range broken.SimulatedAttempts {
				if a.ID == broken.SuspendResume.Receipts[0].CheckpointAttemptID {
					broken.SimulatedAttempts = append(broken.SimulatedAttempts[:i], broken.SimulatedAttempts[i+1:]...)
					break
				}
			}
			if _, err := actioncompare.ValidateSnapshot(&broken); err == nil {
				t.Fatal("lost broker checkpoint passed")
			}
		})
	}
}

func TestRunningSuspendMissingOrLostCheckpointCannotPass(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []string{"never_reached", "disconnect", "changed_attempt", "forged_owner"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := newFeatureFixture(t, backend, false, fixtureFeatures{running: true})
				j := f.claim(t, f.enqueue(t).ID)
				store := f.store.(rehearsalresume.Store)
				if _, err := store.BeginScenarioSuspendResume(t.Context(), f.account.ID, f.run); err == nil {
					t.Fatal("API bypassed application checkpoint")
				}
				if mode == "never_reached" {
					f.complete(t, j)
					if s := f.seal(t); s.Status == "complete" {
						t.Fatal("unexercised checkpoint passed")
					}
					return
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				r, held, detach := f.holdRunningCheckpoint(t, ctx)
				defer detach()
				if mode == "forged_owner" {
					id := f.identities["worker"]
					if _, err := f.store.(rehearsalrecorder.SuspendStore).AdvanceScenarioSuspendResume(t.Context(), id, uuid.NewString(), r.ID, "claimed", "fenced", 0, 0); err == nil {
						t.Fatal("adopted recorder supplied proof")
					}
				}
				if mode == "changed_attempt" {
					if err := f.store.FailInvocation(t.Context(), j.ID, "attempt ended", time.Nanosecond, 3, state.WithInvocationClaim(j)); err != nil {
						t.Fatal(err)
					}
					if _, err := f.store.ClaimInvocation(t.Context(), j.ID, r.InstanceID, 60); err == nil {
						t.Fatal("retry crossed an unfinished original pause")
					}
					if _, err := store.ClaimScenarioSuspendResume(t.Context(), r.ID); err == nil {
						t.Fatal("ended dispatch was paused")
					}
				}
				cancel()
				select {
				case err := <-held:
					if err == nil {
						t.Fatal("disconnected checkpoint released successfully")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("lost owner did not settle")
				}
				r, err := store.ScenarioSuspendResume(t.Context(), r.ID)
				if err != nil || r.State != "failed" {
					t.Fatal("lost held response remained eligible")
				}
			})
		}
	}
}
