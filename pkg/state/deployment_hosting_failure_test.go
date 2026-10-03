// adr: 434 — hosting verdict, failure state and outcome effects commit together.
package state_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

type hostingFailureTestStore interface {
	state.Store
	state.DeploymentHostingReceiptStore
	state.DeploymentHostingFailureStore
}

func forHostingFailureStores(t *testing.T, run func(*testing.T, hostingFailureTestStore)) {
	t.Helper()
	t.Run("mem", func(t *testing.T) { run(t, state.NewMemStore()) })
	t.Run("pg", func(t *testing.T) {
		store, _ := pgStore(t)
		run(t, store)
	})
}

func hostingFailureFixture(t *testing.T, store hostingFailureTestStore) (state.Deployment, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "hosting-failure@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "hosting-failure", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	previous, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:previous"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	return previous, candidate
}

func hostingFailureRaw(t *testing.T, dep state.Deployment, status string) []byte {
	t.Helper()
	raw, err := apihostingreceipt.Encode(apihostingreceipt.Receipt{
		SchemaVersion: apihostingreceipt.SchemaVersion, DeploymentID: dep.ID, AppID: dep.AppID,
		Profile: frameworkprofile.Profile{Version: frameworkprofile.Version},
		Smoke:   apihostingreceipt.SmokeResult{Status: status, Path: "/", StatusCode: 503},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestHostingFailureCommitsVerdictAndOutcome(t *testing.T) {
	forHostingFailureStores(t, func(t *testing.T, store hostingFailureTestStore) {
		ctx := context.Background()
		previous, candidate := hostingFailureFixture(t, store)
		raw := hostingFailureRaw(t, candidate, apihostingreceipt.SmokeFailed)
		changed, err := store.FailDeploymentWithHostingReceipt(ctx, candidate.ID, raw, api.CodeDeploymentSmokeFailed, "candidate unavailable")
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		failed, err := store.DeploymentByID(ctx, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := apihostingreceipt.Decode(failed.APIHostingReceipt)
		if err != nil || receipt.Smoke.Status != apihostingreceipt.SmokeFailed || failed.Status != state.DeployFailed || failed.ErrorCode != api.CodeDeploymentSmokeFailed || failed.TrafficPercent != 0 || failed.RolloutState != "aborted" {
			t.Fatalf("deployment=%+v receipt=%+v err=%v", failed, receipt, err)
		}
		var stages state.StageState
		if err := json.Unmarshal(failed.StageState, &stages); err != nil {
			t.Fatal(err)
		}
		if stages.Current != "" || len(stages.History) == 0 || stages.History[len(stages.History)-1].Status != "failed" {
			t.Fatalf("stage not finalized: %+v", stages)
		}
		live, err := store.LiveDeploymentForScope(ctx, candidate.AppID, state.DefaultEnvScope)
		if err != nil || live.ID != previous.ID || live.TrafficPercent != 100 {
			t.Fatalf("predecessor=%+v err=%v", live, err)
		}
		changed, err = store.FailDeploymentWithHostingReceipt(ctx, candidate.ID, raw, "replacement", "duplicate verdict")
		after, readErr := store.DeploymentByID(ctx, candidate.ID)
		if err != nil || readErr != nil || changed || after.Error != failed.Error || !bytes.Equal(after.APIHostingReceipt, failed.APIHostingReceipt) || !bytes.Equal(after.StageState, failed.StageState) {
			t.Fatalf("redelivery changed outcome: changed=%v err=%v read=%v", changed, err, readErr)
		}
	})
}

func TestHostingFailureFencesStaleVerdicts(t *testing.T) {
	for _, status := range []state.DeploymentStatus{state.DeployCancelled, state.DeployLive, state.DeploySuperseded} {
		t.Run(string(status), func(t *testing.T) {
			forHostingFailureStores(t, func(t *testing.T, store hostingFailureTestStore) {
				ctx := context.Background()
				_, candidate := hostingFailureFixture(t, store)
				var transitionErr error
				if status == state.DeployLive {
					transitionErr = store.MarkDeploymentLive(ctx, candidate.ID)
				} else {
					transitionErr = store.UpdateDeploymentStatus(ctx, candidate.ID, status, "winning transition")
				}
				if transitionErr != nil {
					t.Fatal(transitionErr)
				}
				before, _ := store.DeploymentByID(ctx, candidate.ID)
				changed, err := store.FailDeploymentWithHostingReceipt(ctx, candidate.ID, hostingFailureRaw(t, candidate, apihostingreceipt.SmokeFailed), api.CodeDeploymentSmokeFailed, "stale verdict")
				after, readErr := store.DeploymentByID(ctx, candidate.ID)
				if err != nil || readErr != nil || changed || after.Status != status || after.Error != before.Error || !bytes.Equal(after.APIHostingReceipt, before.APIHostingReceipt) || !bytes.Equal(after.StageState, before.StageState) {
					t.Fatalf("stale verdict changed outcome: changed=%v err=%v read=%v", changed, err, readErr)
				}
			})
		})
	}
}

func TestHostingFailureRejectsInvalidEvidenceAndCancellation(t *testing.T) {
	forHostingFailureStores(t, func(t *testing.T, store hostingFailureTestStore) {
		ctx := context.Background()
		previous, candidate := hostingFailureFixture(t, store)
		for _, raw := range [][]byte{[]byte(`{"schema_version":1}`), hostingFailureRaw(t, previous, apihostingreceipt.SmokeFailed), hostingFailureRaw(t, candidate, apihostingreceipt.SmokeVerified)} {
			if changed, err := store.FailDeploymentWithHostingReceipt(ctx, candidate.ID, raw, api.CodeDeploymentSmokeFailed, "invalid"); err == nil || changed {
				t.Fatalf("invalid evidence accepted: changed=%v err=%v", changed, err)
			}
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if changed, err := store.FailDeploymentWithHostingReceipt(cancelled, candidate.ID, hostingFailureRaw(t, candidate, apihostingreceipt.SmokeFailed), api.CodeDeploymentSmokeFailed, "interrupted"); !errors.Is(err, context.Canceled) || changed {
			t.Fatalf("cancellation accepted: changed=%v err=%v", changed, err)
		}
		after, err := store.DeploymentByID(ctx, candidate.ID)
		if err != nil || after.Status != state.DeploySnapshotting || len(after.APIHostingReceipt) != 0 {
			t.Fatalf("invalid verdict changed candidate: %+v err=%v", after, err)
		}
	})
}

func TestHostingFailureConcurrentFinalizationIsIdempotent(t *testing.T) {
	forHostingFailureStores(t, func(t *testing.T, store hostingFailureTestStore) {
		ctx := context.Background()
		_, candidate := hostingFailureFixture(t, store)
		raw := hostingFailureRaw(t, candidate, apihostingreceipt.SmokeFailed)
		var wg sync.WaitGroup
		results := make(chan bool, 2)
		errs := make(chan error, 2)
		for range 2 {
			wg.Go(func() {
				changed, err := store.FailDeploymentWithHostingReceipt(ctx, candidate.ID, raw, api.CodeDeploymentSmokeFailed, "candidate unavailable")
				results <- changed
				errs <- err
			})
		}
		wg.Wait()
		close(results)
		close(errs)
		changes := 0
		for changed := range results {
			if changed {
				changes++
			}
		}
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if changes != 1 {
			t.Fatalf("committed %d verdicts, want one", changes)
		}
	})
}

func TestPg_HostingFailureRollbackIsReplayable(t *testing.T) {
	for _, timing := range []string{"before update", "at commit"} {
		t.Run(timing, func(t *testing.T) {
			store, pool, ctx := pgStoreWithPool(t)
			previous, candidate := hostingFailureFixture(t, store)
			before, _ := store.DeploymentByID(ctx, candidate.ID)
			_, err := pool.Exec(ctx, `CREATE FUNCTION reject_hosting_failure() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'failed' THEN RAISE EXCEPTION 'injected hosting finalization outage'; END IF;
  RETURN NEW;
END $$`)
			if err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER reject_hosting_failure BEFORE UPDATE ON deployments FOR EACH ROW EXECUTE FUNCTION reject_hosting_failure()`
			if timing == "at commit" {
				trigger = `CREATE CONSTRAINT TRIGGER reject_hosting_failure AFTER UPDATE ON deployments DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_hosting_failure()`
			}
			if _, err := pool.Exec(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			raw := hostingFailureRaw(t, candidate, apihostingreceipt.SmokeFailed)
			changed, err := store.FailDeploymentWithHostingReceipt(ctx, candidate.ID, raw, api.CodeDeploymentSmokeFailed, "candidate unavailable")
			if err == nil || changed {
				t.Fatalf("injected outage was committed: changed=%v err=%v", changed, err)
			}
			after, err := store.DeploymentByID(ctx, candidate.ID)
			if err != nil || after.Status != before.Status || after.Error != before.Error || !bytes.Equal(after.APIHostingReceipt, before.APIHostingReceipt) || !bytes.Equal(after.StageState, before.StageState) {
				t.Fatalf("partial transaction survived: before=%+v after=%+v err=%v", before, after, err)
			}
			if _, err := pool.Exec(ctx, `DROP TRIGGER reject_hosting_failure ON deployments`); err != nil {
				t.Fatal(err)
			}
			changed, err = store.FailDeploymentWithHostingReceipt(ctx, candidate.ID, raw, api.CodeDeploymentSmokeFailed, "candidate unavailable")
			live, liveErr := store.LiveDeploymentForScope(ctx, candidate.AppID, state.DefaultEnvScope)
			if err != nil || !changed || liveErr != nil || live.ID != previous.ID {
				t.Fatalf("replay failed: changed=%v err=%v live=%+v liveErr=%v", changed, err, live, liveErr)
			}
		})
	}
}
