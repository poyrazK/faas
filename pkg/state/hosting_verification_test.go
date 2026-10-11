// adr: 460 — recovery progress is durable, fenced and deadline bounded.
package state_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingVerificationProgressSurvivesReplayAndStageChanges(t *testing.T) {
	forHostingFailureStores(t, func(t *testing.T, store hostingFailureTestStore) {
		ctx := context.Background()
		_, dep := hostingFailureFixture(t, store)
		progressStore := store.(state.DeploymentHostingVerificationStore)
		at := time.Now().UTC()
		begin := state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: at}
		p, err := progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, begin)
		if err != nil || p.Attempts != 1 || !p.StartedAt.Equal(at) || !p.DeadlineAt.Equal(at.Add(api.HostingVerificationRecoveryWindow)) {
			t.Fatalf("begin=%+v err=%v", p, err)
		}
		next := at.Add(5 * time.Second)
		routeChecks := &apihostingreceipt.RouteCheckSet{
			Source: apihostingreceipt.RouteCheckSourceOpenAPI, DocumentSHA256: strings.Repeat("a", 64),
			Status: apihostingreceipt.RouteCheckSetUnavailable,
			Checks: []apihostingreceipt.RouteCheckResult{{Method: "GET", Path: "/v1/health", Status: apihostingreceipt.SmokeSkipped, ErrorCode: apihostingreceipt.SmokeErrorTransportUnavailable}},
		}
		retry := state.HostingVerificationUpdate{Action: state.HostingVerificationRetry, Attempt: p.Attempts, At: at, ErrorCode: apihostingreceipt.SmokeErrorGatewayUnavailable, RouteChecks: routeChecks, RetryNotBefore: next}
		p, err = progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, retry)
		if err != nil || p.LastErrorCode != apihostingreceipt.SmokeErrorGatewayUnavailable || p.RetryNotBefore == nil || !p.RetryNotBefore.Equal(next) || p.LastRouteChecks == nil || p.LastRouteChecks.Checks[0].Path != "/v1/health" {
			t.Fatalf("retry=%+v err=%v", p, err)
		}
		routeChecks.Checks[0].Path = "/mutated"
		if p.LastRouteChecks.Checks[0].Path != "/v1/health" {
			t.Fatal("durable route check evidence aliases the caller's slice")
		}
		before, _ := store.DeploymentByID(ctx, dep.ID)
		begin.At = next.Add(-time.Nanosecond)
		if _, err := progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, begin); !errors.Is(err, state.ErrHostingVerificationDeferred) {
			t.Fatalf("early replay accepted: %v", err)
		}
		after, _ := store.DeploymentByID(ctx, dep.ID)
		if !bytes.Equal(before.StageState, after.StageState) {
			t.Fatal("deferred replay changed durable progress")
		}
		// Stage writes must preserve the nested progress rather than replacing it.
		staged, err := store.AppendDeploymentStage(ctx, dep.ID, state.StageSourceDownload, state.StageReadiness, next, "verification pending")
		if err != nil {
			t.Fatal(err)
		}
		var stages state.StageState
		if err := json.Unmarshal(staged.StageState, &stages); err != nil || stages.HostingVerification == nil || !stages.HostingVerification.DeadlineAt.Equal(p.DeadlineAt) || stages.HostingVerification.LastRouteChecks == nil || stages.HostingVerification.LastRouteChecks.Checks[0].Path != "/v1/health" {
			t.Fatalf("stage update lost recovery: %+v err=%v", stages, err)
		}
		begin.At = next
		p, err = progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, begin)
		if err != nil || p.Attempts != 2 || !p.StartedAt.Equal(at) || !p.DeadlineAt.Equal(at.Add(api.HostingVerificationRecoveryWindow)) || p.RetryNotBefore != nil || p.LastRouteChecks == nil || p.LastRouteChecks.Checks[0].Path != "/v1/health" {
			t.Fatalf("replay renewed deadline: %+v err=%v", p, err)
		}
		if _, err := progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, retry); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale attempt accepted: %v", err)
		}
		p, err = progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationComplete, Attempt: p.Attempts, At: next})
		if err != nil || p.LastErrorCode != "" || p.LastRouteChecks != nil || p.RetryNotBefore != nil || p.CompletedAt == nil || !p.CompletedAt.Equal(next) {
			t.Fatalf("completion=%+v err=%v", p, err)
		}
		after, _ = store.DeploymentByID(ctx, dep.ID)
		if err := json.Unmarshal(after.StageState, &stages); err != nil || stages.Current != state.StageReadiness || stages.HostingVerification.Attempts != 2 {
			t.Fatalf("progress write lost stage: %+v err=%v", stages, err)
		}
	})
}

func TestHostingVerificationDeadlineCannotBeRenewed(t *testing.T) {
	forHostingFailureStores(t, func(t *testing.T, store hostingFailureTestStore) {
		ctx := context.Background()
		_, dep := hostingFailureFixture(t, store)
		progressStore := store.(state.DeploymentHostingVerificationStore)
		at := time.Now().UTC()
		p, err := progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: at})
		if err != nil {
			t.Fatal(err)
		}
		p, err = progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationRetry, Attempt: p.Attempts, At: p.DeadlineAt.Add(-time.Second), RetryNotBefore: p.DeadlineAt.Add(time.Hour)})
		if err != nil || p.RetryNotBefore == nil || !p.RetryNotBefore.Equal(p.DeadlineAt) {
			t.Fatalf("backoff exceeded deadline: %+v err=%v", p, err)
		}
		before, _ := store.DeploymentByID(ctx, dep.ID)
		p, err = progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: p.DeadlineAt})
		if !errors.Is(err, state.ErrHostingVerificationExpired) || p.Attempts != 1 {
			t.Fatalf("expired replay started new attempt: %+v err=%v", p, err)
		}
		after, _ := store.DeploymentByID(ctx, dep.ID)
		if !bytes.Equal(before.StageState, after.StageState) {
			t.Fatal("expired replay changed progress")
		}
		changed, err := store.FailDeploymentWithHostingReceipt(ctx, dep.ID, hostingFailureRaw(t, dep, apihostingreceipt.SmokeFailed), api.CodeDeploymentVerificationUnavailable, "recovery exhausted")
		after, readErr := store.DeploymentByID(ctx, dep.ID)
		var stages state.StageState
		decodeErr := json.Unmarshal(after.StageState, &stages)
		if err != nil || !changed || readErr != nil || decodeErr != nil || stages.HostingVerification == nil || !stages.HostingVerification.DeadlineAt.Equal(p.DeadlineAt) {
			t.Fatalf("failure lost recovery evidence: %+v changed=%v err=%v read=%v decode=%v", stages, changed, err, readErr, decodeErr)
		}
	})
}

// adr: 461 — changing outage reasons cannot renew a candidate's recovery window.
func TestHostingVerificationRecoveryReasonsShareDeadline(t *testing.T) {
	forHostingFailureStores(t, func(t *testing.T, store hostingFailureTestStore) {
		ctx := context.Background()
		_, dep := hostingFailureFixture(t, store)
		progressStore := store.(state.DeploymentHostingVerificationStore)
		at := time.Now().UTC()
		p, err := progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: at})
		if err != nil {
			t.Fatal(err)
		}
		deadline := p.DeadlineAt
		codes := []string{apihostingreceipt.SmokeErrorAuthorizationUnavailable, apihostingreceipt.SmokeErrorGatewayUnavailable, apihostingreceipt.SmokeErrorTransportUnavailable, apihostingreceipt.SmokeErrorResponseUnproven, apihostingreceipt.SmokeErrorDeploymentMismatch, apihostingreceipt.SmokeErrorContractUnavailable}
		for i, code := range codes {
			next := at.Add(time.Second)
			p, err = progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationRetry, Attempt: p.Attempts, ErrorCode: code, At: at, RetryNotBefore: next})
			if err != nil || p.LastErrorCode != code || !p.DeadlineAt.Equal(deadline) {
				t.Fatalf("reason changed deadline or was not retained: progress=%+v err=%v", p, err)
			}
			if _, err := progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: deadline}); !errors.Is(err, state.ErrHostingVerificationExpired) {
				t.Fatalf("reason %s escaped the recovery deadline: %v", code, err)
			}
			at = next
			p, err = progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: at})
			if err != nil || p.Attempts != i+2 || !p.DeadlineAt.Equal(deadline) {
				t.Fatalf("replay renewed recovery: progress=%+v err=%v", p, err)
			}
		}
		before, _ := store.DeploymentByID(ctx, dep.ID)
		if _, err := progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationRetry, Attempt: p.Attempts, ErrorCode: "secret arbitrary error text", At: at, RetryNotBefore: at}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("unbounded reason accepted: %v", err)
		}
		after, _ := store.DeploymentByID(ctx, dep.ID)
		if !bytes.Equal(before.StageState, after.StageState) {
			t.Fatal("invalid reason changed progress")
		}
	})
}

func TestHostingVerificationFencesTerminalDeployments(t *testing.T) {
	for _, status := range []state.DeploymentStatus{state.DeployLive, state.DeployCancelled, state.DeployFailed, state.DeploySuperseded} {
		t.Run(string(status), func(t *testing.T) {
			forHostingFailureStores(t, func(t *testing.T, store hostingFailureTestStore) {
				ctx := context.Background()
				_, dep := hostingFailureFixture(t, store)
				progressStore := store.(state.DeploymentHostingVerificationStore)
				at := time.Now().UTC()
				p, err := progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: at})
				if err != nil {
					t.Fatal(err)
				}
				if status == state.DeployLive {
					err = store.MarkDeploymentLive(ctx, dep.ID)
				} else {
					err = store.UpdateDeploymentStatus(ctx, dep.ID, status, "winning transition")
				}
				if err != nil {
					t.Fatal(err)
				}
				before, _ := store.DeploymentByID(ctx, dep.ID)
				for _, u := range []state.HostingVerificationUpdate{
					{Action: state.HostingVerificationBegin, At: at},
					{Action: state.HostingVerificationRetry, Attempt: p.Attempts, At: at, RetryNotBefore: at},
					{Action: state.HostingVerificationComplete, Attempt: p.Attempts, At: at},
				} {
					if _, err := progressStore.UpdateDeploymentHostingVerification(ctx, dep.ID, u); !errors.Is(err, state.ErrHostingVerificationFinalized) {
						t.Fatalf("terminal %s accepted %s: %v", status, u.Action, err)
					}
				}
				after, _ := store.DeploymentByID(ctx, dep.ID)
				if !bytes.Equal(before.StageState, after.StageState) || after.Status != status {
					t.Fatal("stale verifier changed winning transition")
				}
			})
		})
	}
}

func TestPg_HostingVerificationCommitOutageRemainsReplayable(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	_, dep := hostingFailureFixture(t, store)
	at := time.Now().UTC()
	before, _ := store.DeploymentByID(ctx, dep.ID)
	_, err := pool.Exec(ctx, `CREATE FUNCTION reject_verification_progress() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.stage_state ? 'hosting_verification' THEN RAISE EXCEPTION 'injected progress commit outage'; END IF;
  RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER reject_verification_progress AFTER UPDATE ON deployments
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_verification_progress()`)
	if err != nil {
		t.Fatal(err)
	}
	begin := state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: at}
	if _, err := store.UpdateDeploymentHostingVerification(ctx, dep.ID, begin); err == nil {
		t.Fatal("injected commit outage accepted progress")
	}
	after, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil || after.Status != before.Status || !bytes.Equal(before.StageState, after.StageState) {
		t.Fatalf("partial progress survived rollback: before=%s after=%s err=%v", before.StageState, after.StageState, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_verification_progress ON deployments`); err != nil {
		t.Fatal(err)
	}
	p, err := store.UpdateDeploymentHostingVerification(ctx, dep.ID, begin)
	if err != nil || p.Attempts != 1 || !p.StartedAt.Equal(at) {
		t.Fatalf("commit recovery could not replay: %+v err=%v", p, err)
	}
}
