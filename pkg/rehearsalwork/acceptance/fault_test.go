package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
)

func (f *fixture) checkpoint(t *testing.T) api.ScenarioSimulationDecision {
	t.Helper()
	return f.checkpointContext(t.Context(), t)
}

func (f *fixture) checkpointContext(ctx context.Context, t *testing.T) api.ScenarioSimulationDecision {
	t.Helper()
	id := f.identities["worker"]
	req := api.ScenarioSimulationRequest{RecorderRequestID: f.workerRequestContext(ctx, t), AccountID: f.account.ID, AppID: id.AppID, RunID: f.run, InstanceID: id.InstanceID, Integration: "checkpoint", Method: "POST", Path: "/after-commit", Body: []byte(`{}`)}
	s := f.store.(state.ScenarioSimulationStore)
	d, err := s.BeginScenarioSimulation(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishScenarioSimulation(ctx, req, d.Attempt.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	return d
}

func (f *fixture) claimWithDeferredBinding(t *testing.T, id string) state.Invocation {
	t.Helper()
	j, err := f.store.ClaimInvocationWithCap(t.Context(), id, "", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if s := f.seal(t); s.Status != "pending" {
		t.Fatalf("unbound dispatch prematurely sealed: %+v", s)
	}
	if err = f.store.StampInstanceInvocation(t.Context(), id, f.identities["worker"].InstanceID); err != nil {
		t.Fatal(err)
	}
	return j
}

func awaitFault(t *testing.T, ctx context.Context, store rehearsalwork.FaultStore, id, want string) rehearsalwork.WorkerFault {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		f, err := store.ScenarioWorkerFault(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if f.State == want {
			return f
		}
		select {
		case <-deadline.C:
			t.Fatalf("fault remained %s, want %s", f.State, want)
		case <-ticker.C:
		}
	}
}

func (f *fixture) holdCheckpoint(t *testing.T, id string, ctx context.Context) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	joined := make(chan struct{})
	var connection *rehearsalrecorder.Connection
	var err error
	connection, err = f.workerSession.Attach(func() { <-joined; connection.Detach(false) })
	if err != nil {
		t.Fatal(err)
	}
	go func() { err := f.workerSession.HoldWorkerCheckpoint(ctx, id); done <- err; close(joined) }()
	awaitFault(t, ctx, f.store.(rehearsalwork.FaultStore), id, "ready")
	return done
}

func (f *fixture) replaceWorker(t *testing.T) {
	t.Helper()
	old := f.identities["worker"]
	row, err := f.store.InstanceByID(t.Context(), old.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.UpdateInstanceState(t.Context(), old.InstanceID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	i, err := f.store.CreateInstance(t.Context(), old.AppID, old.DeploymentID, string(state.StateColdBooting), 128, row.NodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.InstanceID = i.ID
	session, err := f.store.(rehearsalrecorder.Store).StartScenarioRecorder(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	f.identities["worker"], f.recorders["worker"], f.workerSession = next, session, nil
	if f.connection != nil {
		config := f.connection.Config()
		if err := f.connection.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		f.connection, err = pgx.ConnectConfig(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fixture) terminateAtCheckpoint(t *testing.T, j state.Invocation) {
	t.Helper()
	d := f.checkpoint(t)
	store := f.store.(rehearsalwork.FaultStore)
	fault, err := store.ScenarioWorkerFault(t.Context(), d.WorkerFaultID)
	if err != nil {
		t.Fatal(err)
	}
	held := f.holdCheckpoint(t, fault.ID, t.Context())
	if _, err = store.ClaimScenarioWorkerFault(t.Context(), fault.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.workerSession.PrepareWorkerTermination(t.Context(), fault.ID); err != nil {
		t.Fatal(err)
	}
	<-held
	if err = f.store.FailInvocation(t.Context(), j.ID, "controlled worker termination", time.Nanosecond, 3, state.WithInvocationClaim(j)); err != nil {
		t.Fatal(err)
	}
	if err = f.workerSession.FinishWorkerTermination(t.Context(), fault.ID); err != nil {
		t.Fatal(err)
	}
	f.replaceWorker(t)
}

func TestWorkerTerminationRealFixturesDetectDuplicateExports(t *testing.T) {
	if os.Getenv("FAAS_REHEARSAL_PG_TEST_ADMIN_URL") == "" {
		t.Skip("requires dedicated disposable fixture cluster")
	}
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			run := func(duplicate bool) (api.ScenarioActionSnapshot, rehearsalpg.Evidence, rehearsalobj.Evidence) {
				f := newFixture(t, backend, true, true)
				j := f.claimWithDeferredBinding(t, f.enqueue(t).ID)
				if _, err := f.connection.Exec(t.Context(), "INSERT INTO public.imports VALUES ('customer-import') ON CONFLICT DO NOTHING"); err != nil {
					t.Fatal(err)
				}
				export := func() {
					t.Helper()
					if w := f.object(t, "worker", "PUT", "exports/result.txt", []byte("same export")); w.Code != 200 {
						t.Fatalf("export: %d %s", w.Code, w.Body.String())
					}
					f.notify(t)
				}
				if duplicate {
					export()
				}
				f.terminateAtCheckpoint(t, j)
				j = f.claimWithDeferredBinding(t, j.ID)
				if _, err := f.connection.Exec(t.Context(), "INSERT INTO public.imports VALUES ('customer-import') ON CONFLICT DO NOTHING"); err != nil {
					t.Fatal(err)
				}
				export()
				f.complete(t, j)
				s := f.finish(t, "")
				if _, err := actioncompare.ValidateSnapshot(&s); err != nil {
					t.Fatal(err)
				}
				pg, err := f.postgres.Seal(t.Context(), f.account.ID, f.run, f.identities["primary"].DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				objects, err := f.objects.Seal(t.Context(), f.account.ID, f.run, f.identities["primary"].DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				return s, pg, objects
			}
			a, pgA, objA := run(false)
			b, pgB, objB := run(true)
			if rehearsalpg.Compare(&pgA, &pgB).Status != "passed" || rehearsalobj.Hash(objA.After) != rehearsalobj.Hash(objB.After) {
				t.Fatal("final fixture state unexpectedly differs")
			}
			wrap := func(s api.ScenarioActionSnapshot) *actioncompare.Evidence {
				return &actioncompare.Evidence{Version: 1, Status: "complete", Provenance: actioncompare.SimulationProvenance, ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64), Snapshot: &s}
			}
			if c := actioncompare.Compare(wrap(a), wrap(b)); c.Status != "failed" {
				t.Fatalf("duplicate notification passed: %+v", c)
			}
			if c := rehearsalobj.Compare(&objA, &objB); c.Status != "failed" || len(objA.Mutations) != 1 || len(objB.Mutations) != 2 {
				t.Fatalf("duplicate export passed: %+v", c)
			}
		})
	}
}

func TestWorkerTerminationRecoveryComparesIndependentEffects(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			run := func(duplicate bool) api.ScenarioActionSnapshot {
				f := newFixture(t, backend, false, true)
				store := f.store.(rehearsalwork.FaultStore)
				j := f.claimWithDeferredBinding(t, f.enqueue(t).ID)
				f.notify(t)
				d := f.checkpoint(t)
				if d.WorkerFaultID == "" {
					t.Fatal("checkpoint did not arm termination")
				}
				receipt, err := store.ScenarioWorkerFault(t.Context(), d.WorkerFaultID)
				if err != nil {
					t.Fatal(err)
				}
				f.recorders["worker"] = receipt.RecorderSessionID
				if f.pool != nil {
					for _, query := range []string{
						"UPDATE scenario_worker_faults SET instance_id='99999999-9999-4999-8999-999999999999' WHERE id=$1",
						"UPDATE scenario_worker_faults SET state='terminated' WHERE id=$1",
						"DELETE FROM scenario_worker_faults WHERE id=$1",
					} {
						if _, guardErr := f.pool.Exec(t.Context(), query, receipt.ID); guardErr == nil {
							t.Fatal("SQL bypassed retained fault identity or lifecycle")
						}
					}
					if _, guardErr := f.pool.Exec(t.Context(), "UPDATE scenario_recorder_sessions SET status='closed' WHERE id=$1", receipt.RecorderSessionID); guardErr == nil {
						t.Fatal("recorder closed without termination acknowledgement")
					}
				}
				held := f.holdCheckpoint(t, receipt.ID, t.Context())
				if _, err = store.ClaimScenarioWorkerFault(t.Context(), receipt.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = store.ClaimScenarioWorkerFault(t.Context(), receipt.ID); err == nil {
					t.Fatal("fault claimed twice")
				}
				if err = f.workerSession.PrepareWorkerTermination(t.Context(), receipt.ID); err != nil {
					t.Fatal(err)
				}
				if err = <-held; !errors.Is(err, rehearsalrecorder.ErrClosing) {
					t.Fatalf("guest checkpoint resumed: %v", err)
				}
				if err = f.store.FailInvocation(t.Context(), j.ID, "controlled worker termination", time.Nanosecond, 3, state.WithInvocationClaim(j)); err != nil {
					t.Fatal(err)
				}
				if _, err = f.store.ClaimInvocation(t.Context(), j.ID, "", 60); err == nil {
					t.Fatal("retry started before physical cleanup")
				}
				// Portable surrogate for vmmd's independently tested physical cleanup.
				if err = f.workerSession.FinishWorkerTermination(t.Context(), receipt.ID); err != nil {
					t.Fatal(err)
				}
				if blocked, err := store.ScenarioWorkerDispatchBlocked(t.Context(), receipt.AppID); err != nil || !blocked {
					t.Fatal("retry started before scheduler retired old VM")
				}
				f.replaceWorker(t)
				j = f.claimWithDeferredBinding(t, j.ID)
				if d = f.checkpoint(t); d.WorkerFaultID != "" {
					t.Fatal("retry armed a second kill")
				}
				if duplicate {
					f.notify(t)
				}
				f.complete(t, j)
				s := f.finish(t, "")
				if _, err = actioncompare.ValidateSnapshot(&s); err != nil {
					raw, _ := json.Marshal(s)
					t.Fatalf("recovery evidence: %s %v", raw, err)
				}
				if s.ManagedWork.WorkerFault == nil || s.ManagedWork.WorkerFault.RecoveryInstanceID == receipt.InstanceID {
					t.Fatal("fresh recovery evidence missing")
				}
				return s
			}
			baseline, candidate := run(false), run(true)
			wrap := func(s api.ScenarioActionSnapshot) *actioncompare.Evidence {
				return &actioncompare.Evidence{Version: 1, Status: "complete", Provenance: actioncompare.SimulationProvenance, ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64), Snapshot: &s}
			}
			if c := actioncompare.Compare(wrap(baseline), wrap(candidate)); c.Status != "failed" {
				t.Fatalf("duplicate retry notification passed: %+v", c)
			}
			missing := candidate
			missing.SimulatedAttempts = append([]api.ScenarioSimulationAttempt(nil), candidate.SimulatedAttempts...)
			for i, a := range missing.SimulatedAttempts {
				if a.ID == candidate.ManagedWork.WorkerFault.CheckpointAttemptID {
					missing.SimulatedAttempts = append(missing.SimulatedAttempts[:i], missing.SimulatedAttempts[i+1:]...)
					break
				}
			}
			if _, err := actioncompare.ValidateSnapshot(&missing); err == nil {
				t.Fatal("missing checkpoint broker receipt passed")
			}
		})
	}
}

func TestWorkerTerminationLostCheckpointCannotPass(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []string{"never_reached", "owner_lost", "early_disconnect", "unfinished_cleanup", "unknown_attempt", "forged_owner"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := newFixture(t, backend, false, true)
				j := f.claim(t, f.enqueue(t).ID)
				store := f.store.(rehearsalwork.FaultStore)
				if mode == "never_reached" {
					f.complete(t, j)
					if s := f.seal(t); s.Status == "complete" {
						t.Fatal("missing checkpoint passed")
					}
					return
				}
				d := f.checkpoint(t)
				fault, err := store.ScenarioWorkerFault(t.Context(), d.WorkerFaultID)
				if err != nil {
					t.Fatal(err)
				}
				f.recorders["worker"] = fault.RecorderSessionID
				if mode == "owner_lost" {
					f.workerSession.Stop()
					if _, err = store.ClaimScenarioWorkerFault(t.Context(), fault.ID); err == nil {
						t.Fatal("new owner adopted broker receipt")
					}
					return
				}
				if mode == "forged_owner" {
					id := f.identities["worker"]
					id.InstanceID = uuid.NewString()
					if err = f.store.(rehearsalrecorder.WorkerFaultStore).ReadyScenarioWorkerCheckpoint(t.Context(), id, fault.ID, fault.RecorderSessionID); err == nil {
						t.Fatal("foreign owner acknowledged checkpoint")
					}
					return
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				held := f.holdCheckpoint(t, fault.ID, ctx)
				if mode == "early_disconnect" {
					cancel()
					<-held
					awaitFault(t, t.Context(), store, fault.ID, "failed")
					if _, err = store.ClaimScenarioWorkerFault(t.Context(), fault.ID); err == nil {
						t.Fatal("disconnected checkpoint claimed")
					}
					return
				}
				if _, err = store.ClaimScenarioWorkerFault(t.Context(), fault.ID); err != nil {
					t.Fatal(err)
				}
				if err = f.workerSession.PrepareWorkerTermination(t.Context(), fault.ID); err != nil {
					t.Fatal(err)
				}
				<-held
				if mode == "unknown_attempt" {
					if err = f.workerSession.FinishWorkerTermination(t.Context(), fault.ID); err != nil {
						t.Fatal(err)
					}
					f.replaceWorker(t)
					if err = f.store.FailInvocation(t.Context(), j.ID, "dispatch lease expired; requeued", time.Nanosecond, 3); err != nil {
						t.Fatal(err)
					}
					j = f.claim(t, j.ID)
					f.complete(t, j)
					if s := f.finish(t, ""); s.Status == "complete" {
						t.Fatal("unknown attempt excused by declared fault")
					}
				} else {
					if err = store.FailScenarioWorkerFault(t.Context(), fault.ID); err != nil {
						t.Fatal(err)
					}
					if err = f.workerSession.FinishWorkerTermination(t.Context(), fault.ID); err == nil {
						t.Fatal("failed cleanup was acknowledged")
					}
				}
			})
		}
	}
}
