package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

func routeRemovalFixture(t *testing.T) (*state.PgStore, *pgxpool.Pool, context.Context, state.Account, state.App, state.Deployment, state.Deployment, api.RouteRemovalPolicy) {
	t.Helper()
	store, pool, ctx := pgStoreWithPool(t)
	account, app, baseline, candidate := bindingPromotionFixture(t, store)
	oldDoc := []byte(`{"openapi":"3.0.3","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	newDoc := []byte(`{"openapi":"3.0.3","paths":{"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	for id, doc := range map[string][]byte{baseline.ID: oldDoc, candidate.ID: newDoc} {
		if err := store.UpsertDeploymentOpenAPIDoc(ctx, id, account.ID, app.ID, doc, "manual_upload", false); err != nil {
			t.Fatal(err)
		}
	}
	revision := int64(0)
	policy, err := store.SetRouteRemovalPolicy(ctx, account.ID, app.ID, api.SetRouteRemovalPolicyRequest{ExpectedRevision: &revision, Mode: "enforce", GracePeriod: "1h", MaxApprovalAge: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	seedHealthyRouteCoverage(t, pool, ctx)
	return store, pool, ctx, account, app, baseline, candidate, policy
}

func TestRouteRemovalPGEnforcement(t *testing.T) {
	store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
	revision := policy.Revision
	check, err := store.CheckRouteRemoval(ctx, account.ID, app.ID, candidate.ID)
	if err != nil || check.Status != "blocked" || len(check.Removed) != 1 {
		t.Fatalf("check: %+v %v", check, err)
	}
	if _, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); err == nil {
		t.Fatal("unapproved removal accepted")
	}
	if _, err = pool.Exec(ctx, `UPDATE deployments SET traffic_percent=10 WHERE id=$1`, candidate.ID); err == nil {
		t.Fatal("direct SQL bypassed guard")
	}
	if _, err = pool.Exec(ctx, `UPDATE app_route_removal_policies SET baseline_since=clock_timestamp()-interval '2 hours' WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE deployment_openapi_docs SET captured_at=clock_timestamp()-interval '2 hours' WHERE deployment_id=$1`, baseline.ID); err != nil {
		t.Fatal(err)
	}
	check, err = store.CheckRouteRemoval(ctx, account.ID, app.ID, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision = policy.Revision
	request := api.ApproveRouteRemovalRequest{ExpectedPolicyRevision: &revision, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: check.BaselineContractSHA256, CandidateContractSHA256: check.CandidateContractSHA256, Mappings: []api.RouteRemovalMapping{{Method: "GET", Path: "/old", SuccessorMethod: "GET", SuccessorPath: "/new"}}, AcknowledgeObservedOnly: true}
	// Compatibility is separately enforced by the API callback; this suite
	// exercises the database's evidence and traffic transaction boundaries.
	validate := func(state.RouteRemovalContract, state.RouteRemovalContract, []api.RouteRemovalMapping) error {
		return nil
	}
	approval, err := store.ApproveRouteRemoval(ctx, account.ID, app.ID, "owner:test", request, validate)
	if err != nil {
		t.Fatal(err)
	}
	check, err = store.CheckRouteRemoval(ctx, account.ID, app.ID, candidate.ID)
	if err != nil || check.Status != "passed" || check.ApprovalID != approval.ID {
		t.Fatalf("approved check: %+v %v", check, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE route_removal_approvals SET valid_until=approved_at+interval '1 microsecond' WHERE id=$1`, approval.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); err == nil {
		t.Fatal("expired approval accepted")
	}
	_, err = store.ApproveRouteRemoval(ctx, account.ID, app.ID, "owner:test", request, validate)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.SetRouteRemovalPolicy(ctx, account.ID, app.ID, api.SetRouteRemovalPolicyRequest{ExpectedRevision: &revision, Mode: "enforce", GracePeriod: "1h", MaxApprovalAge: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); err == nil {
		t.Fatal("approval survived policy revision")
	}
	if _, err = store.ApproveRouteRemoval(context.Background(), account.ID, app.ID, "owner:test", request, validate); !errors.Is(err, state.ErrRouteRemovalPolicyRevision) {
		t.Fatalf("stale revision: %v", err)
	}
	dep, err := store.DeploymentByID(ctx, baseline.ID)
	if err != nil || dep.TrafficPercent != 100 {
		t.Fatalf("baseline changed after rejection: %+v %v", dep, err)
	}
}

func TestRouteRemovalPGTransitions(t *testing.T) {
	for _, transition := range []string{"activation", "canary", "recover_promote", "rollback", "scope", "zero_baseline", "supersede_baseline", "insert"} {
		t.Run(transition, func(t *testing.T) {
			store, pool, ctx, _, app, baseline, candidate, _ := routeRemovalFixture(t)
			var err error
			switch transition {
			case "activation":
				_, err = pool.Exec(ctx, `UPDATE deployments SET status='building',traffic_percent=100 WHERE id=$1`, candidate.ID)
				if err != nil {
					t.Fatal(err)
				}
				err = store.MarkDeploymentLive(ctx, candidate.ID)
			case "canary":
				_, err = pool.Exec(ctx, `UPDATE deployments SET rollout_state='rolling_out',canary_total_steps=3,canary_step=0 WHERE id=$1`, candidate.ID)
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = store.AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10})
			case "recover_promote":
				_, err = pool.Exec(ctx, `UPDATE deployments SET rollout_state='rolling_out',canary_total_steps=3,canary_step=0 WHERE id=$1`, candidate.ID)
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = store.RecoverRollout(ctx, app.ID, "promote", "route guard test")
			case "rollback":
				_, err = pool.Exec(ctx, `UPDATE deployments SET status='superseded' WHERE id=$1`, candidate.ID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.AutoRollbackDeploymentsTx(ctx, app.ID, baseline.ID)
			case "scope":
				_, err = pool.Exec(ctx, `UPDATE deployments SET scope='staging',traffic_percent=100 WHERE id=$1`, candidate.ID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = pool.Exec(ctx, `UPDATE deployments SET scope='production' WHERE id=$1`, candidate.ID)
			case "zero_baseline", "supersede_baseline":
				tx, txerr := pool.Begin(ctx)
				if txerr != nil {
					t.Fatal(txerr)
				}
				defer tx.Rollback(ctx)
				query := `UPDATE deployments SET traffic_percent=0 WHERE id=$1`
				if transition == "supersede_baseline" {
					query = `UPDATE deployments SET status='superseded' WHERE id=$1`
				}
				if _, txerr = tx.Exec(ctx, query, baseline.ID); txerr != nil {
					t.Fatal(txerr)
				}
				_, err = tx.Exec(ctx, `UPDATE deployments SET traffic_percent=100 WHERE id=$1`, candidate.ID)
			case "insert":
				_, err = pool.Exec(ctx, `INSERT INTO deployments(app_id,kind,status,scope,traffic_percent,image_digest) VALUES($1,'image','live','prod',100,'sha256:test')`, app.ID)
			}
			if err == nil || !strings.Contains(err.Error(), "route removal") {
				t.Fatalf("%s did not reach route guard: %v", transition, err)
			}
		})
	}
}

func TestRouteRemovalPGPolicyLockRace(t *testing.T) {
	store, pool, _, account, app, _, candidate, policy := routeRemovalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	revision := policy.Revision
	policy, err := store.SetRouteRemovalPolicy(ctx, account.ID, app.ID, api.SetRouteRemovalPolicyRequest{ExpectedRevision: &revision, Mode: "report", GracePeriod: "1h", MaxApprovalAge: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE app_route_removal_policies SET mode='enforce',revision=revision+1 WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); done <- err }()
	waitRouteRemovalLock(t, ctx, pool, done)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil || !strings.Contains(err.Error(), "route removal") {
		t.Fatalf("used policy from before lock wait: %v", err)
	}
}

func routeRemovalApprove(t *testing.T, store *state.PgStore, pool *pgxpool.Pool, ctx context.Context, account state.Account, app state.App, baseline, candidate state.Deployment, policy api.RouteRemovalPolicy) api.RouteRemovalApproval {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE app_route_removal_policies SET baseline_since=clock_timestamp()-interval '2 hours' WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE deployment_openapi_docs SET captured_at=clock_timestamp()-interval '2 hours' WHERE deployment_id=$1`, baseline.ID); err != nil {
		t.Fatal(err)
	}
	check, err := store.CheckRouteRemoval(ctx, account.ID, app.ID, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision := policy.Revision
	approval, err := store.ApproveRouteRemoval(ctx, account.ID, app.ID, "owner:test", api.ApproveRouteRemovalRequest{ExpectedPolicyRevision: &revision, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: check.BaselineContractSHA256, CandidateContractSHA256: check.CandidateContractSHA256, Mappings: []api.RouteRemovalMapping{{Method: "GET", Path: "/old", SuccessorMethod: "GET", SuccessorPath: "/new"}}, AcknowledgeObservedOnly: true}, func(state.RouteRemovalContract, state.RouteRemovalContract, []api.RouteRemovalMapping) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return approval
}

func TestRouteRemovalPGEvidenceLockRaces(t *testing.T) {
	for _, change := range []string{"expiry", "candidate_capture", "baseline_capture", "old_traffic", "revision"} {
		t.Run(change, func(t *testing.T) {
			store, pool, _, account, app, baseline, candidate, policy := routeRemovalFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			approval := routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "expiry":
				_, err = tx.Exec(ctx, `UPDATE route_removal_approvals SET valid_until=approved_at+interval '1 microsecond' WHERE id=$1`, approval.ID)
			case "candidate_capture", "baseline_capture":
				id := candidate.ID
				if change == "baseline_capture" {
					id = baseline.ID
				}
				_, err = tx.Exec(ctx, `UPDATE deployment_openapi_docs SET doc_sha256=decode(repeat('f',64),'hex') WHERE deployment_id=$1`, id)
			case "revision":
				_, err = tx.Exec(ctx, `UPDATE app_route_removal_policies SET revision=revision+1 WHERE app_id=$1`, app.ID)
			case "old_traffic":
				_, err = tx.Exec(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,method,route,status,latency_ms) VALUES($1,$2,$3,'GET','/old',200,1)`, account.ID, app.ID, baseline.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); done <- err }()
			waitRouteRemovalLock(t, ctx, pool, done)
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err == nil || !strings.Contains(err.Error(), "route removal") {
				t.Fatalf("accepted %s committed during lock wait: %v", change, err)
			}
			dep, err := store.DeploymentByID(ctx, baseline.ID)
			if err != nil || dep.TrafficPercent != 100 {
				t.Fatalf("rejection mutated traffic: %+v %v", dep, err)
			}
		})
	}
}

func TestRouteRemovalPGApprovedCutover(t *testing.T) {
	store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
	routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
	if _, err := store.UpdateDeploymentTraffic(ctx, candidate.ID, 10); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetRouteRemovalPolicy(ctx, account.ID, app.ID)
	if err != nil || current.BaselineDeploymentID != baseline.ID {
		t.Fatalf("partial canary changed baseline: %+v %v", current, err)
	}
	if _, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); err != nil {
		t.Fatal(err)
	}
	current, err = store.GetRouteRemovalPolicy(ctx, account.ID, app.ID)
	if err != nil || current.BaselineDeploymentID != candidate.ID {
		t.Fatalf("cutover did not advance baseline: %+v %v", current, err)
	}
}

func TestRouteRemovalPGReportAndScope(t *testing.T) {
	for _, scope := range []string{"default", "prod", "production", "staging"} {
		t.Run(scope, func(t *testing.T) {
			store, pool, ctx, account, app, _, candidate, policy := routeRemovalFixture(t)
			if _, err := pool.Exec(ctx, `UPDATE deployments SET scope=$2 WHERE id=$1`, candidate.ID, scope); err != nil {
				t.Fatal(err)
			}
			if scope == "staging" {
				if _, err := pool.Exec(ctx, `UPDATE deployments SET traffic_percent=100 WHERE id=$1`, candidate.ID); err != nil {
					t.Fatalf("staging excluded: %v", err)
				}
				return
			}
			if _, err := pool.Exec(ctx, `UPDATE deployments SET traffic_percent=10 WHERE id=$1`, candidate.ID); err == nil {
				t.Fatal("production alias bypassed guard")
			}
			revision := policy.Revision
			if _, err := store.SetRouteRemovalPolicy(ctx, account.ID, app.ID, api.SetRouteRemovalPolicyRequest{ExpectedRevision: &revision, Mode: "report", GracePeriod: "1h", MaxApprovalAge: "1h"}); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE deployments SET traffic_percent=10 WHERE id=$1`, candidate.ID); err != nil {
				t.Fatalf("report mode blocked: %v", err)
			}
		})
	}
}

func TestRouteRemovalPGNoRemoval(t *testing.T) {
	store, _, ctx, account, app, baseline, candidate, _ := routeRemovalFixture(t)
	doc, _, err := store.GetDeploymentOpenAPIDoc(ctx, baseline.ID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertDeploymentOpenAPIDoc(ctx, candidate.ID, account.ID, app.ID, doc, "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); err != nil {
		t.Fatalf("unchanged contract blocked: %v", err)
	}
}

func waitRouteRemovalLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, done <-chan error) {
	t.Helper()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND pid<>pg_backend_pid())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("traffic transition completed before lock release: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRouteRemovalPGContractFence(t *testing.T) {
	for _, change := range []string{"unchanged", "report", "revision", "replacement", "deleted"} {
		t.Run(change, func(t *testing.T) {
			store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
			approval := routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
			ctx = state.WithRouteRemovalFence(ctx, state.RouteRemovalFence{DeploymentID: candidate.ID, BaselineDeploymentID: baseline.ID, ApprovalID: approval.ID, PolicyRevision: policy.Revision, BaselineSHA256: approval.BaselineContractSHA256, CandidateSHA256: approval.CandidateContractSHA256})
			var err error
			switch change {
			case "report":
				_, err = pool.Exec(ctx, `UPDATE app_route_removal_policies SET mode='report' WHERE app_id=$1`, app.ID)
			case "revision":
				_, err = pool.Exec(ctx, `UPDATE app_route_removal_policies SET revision=revision+1 WHERE app_id=$1`, app.ID)
			case "replacement":
				routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
			case "deleted":
				_, err = pool.Exec(ctx, `DELETE FROM app_route_removal_policies WHERE app_id=$1`, app.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100)
			if change == "unchanged" && err != nil {
				t.Fatal(err)
			}
			if change != "unchanged" && (err == nil || !strings.Contains(err.Error(), "route removal")) {
				t.Fatalf("accepted stale contract fence (%s): %v", change, err)
			}
		})
	}
}

func TestRouteRemovalPGApprovedContractException(t *testing.T) {
	for _, change := range []string{"unchanged", "baseline_snapshot", "candidate_snapshot", "deleted_snapshot"} {
		t.Run(change, func(t *testing.T) {
			store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
			routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
			for _, deployment := range []state.Deployment{baseline, candidate} {
				doc, _, err := store.GetDeploymentOpenAPIDoc(ctx, deployment.ID, account.ID)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, _, err := openapidiff.SnapshotFromDocument(deployment.ID, app.ID, "default", doc, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.UpdateDeploymentOpenAPISnapshot(ctx, snapshot); err != nil {
					t.Fatal(err)
				}
			}
			check, err := openapidiff.CheckLiveContract(ctx, store, app.ID, candidate.ID, "default", true)
			if err != nil {
				t.Fatal(err)
			}
			if check.Baseline.DeploymentID != baseline.ID {
				t.Fatal("dark snapshot replaced serving baseline")
			}
			check, fence, err := openapidiff.ApplyRemovalException(ctx, store, check, false)
			if err != nil || fence == nil || check.Diff.Blocking() {
				t.Fatalf("approved exception: %+v %+v %v", check, fence, err)
			}
			ctx = state.WithRouteRemovalFence(ctx, *fence)
			switch change {
			case "baseline_snapshot", "candidate_snapshot":
				id := baseline.ID
				if change == "candidate_snapshot" {
					id = candidate.ID
				}
				if _, err = pool.Exec(ctx, `UPDATE deployment_openapi_snapshots SET sha256=repeat('f',64) WHERE deployment_id=$1`, id); err != nil {
					t.Fatal(err)
				}
			case "deleted_snapshot":
				if _, err = pool.Exec(ctx, `DELETE FROM deployment_openapi_snapshots WHERE deployment_id=$1`, candidate.ID); err != nil {
					t.Fatal(err)
				}
			}
			_, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100)
			if change == "unchanged" && err != nil {
				t.Fatal(err)
			}
			if change != "unchanged" && (err == nil || !strings.Contains(err.Error(), "route removal")) {
				t.Fatalf("accepted changed snapshot: %v", err)
			}
		})
	}
}

func TestRouteRemovalPGRecoveryContractFence(t *testing.T) {
	for _, change := range []string{"report", "newer_rollout"} {
		t.Run(change, func(t *testing.T) {
			store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
			approval := routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
			ctx = state.WithRouteRemovalFence(ctx, state.RouteRemovalFence{DeploymentID: candidate.ID, BaselineDeploymentID: baseline.ID, ApprovalID: approval.ID, PolicyRevision: policy.Revision, BaselineSHA256: approval.BaselineContractSHA256, CandidateSHA256: approval.CandidateContractSHA256})
			if _, err := pool.Exec(ctx, `UPDATE deployments SET canary_total_steps=2,rollout_state='rolling_out' WHERE id=$1`, candidate.ID); err != nil {
				t.Fatal(err)
			}
			if change == "report" {
				if _, err := pool.Exec(ctx, `UPDATE app_route_removal_policies SET mode='report' WHERE app_id=$1`, app.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				extra, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, TrafficPercent: 0, TrafficPercentExplicit: true})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(ctx, `UPDATE deployments SET status='live',canary_total_steps=2,rollout_state='rolling_out' WHERE id=$1`, extra.ID); err != nil {
					t.Fatal(err)
				}
			}
			result, _, err := store.RecoverRollout(ctx, app.ID, "promote", "contract fence regression")
			if change == "report" && (err == nil || !strings.Contains(err.Error(), "route removal")) {
				t.Fatalf("recovery bypassed contract fence: %v", err)
			}
			if change == "newer_rollout" && (err != nil || result.ID != candidate.ID) {
				t.Fatalf("recovery switched to unreviewed rollout: %+v %v", result, err)
			}
		})
	}
}

func TestRouteRemovalPGGatewayRouteLabels(t *testing.T) {
	store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
	approval := routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,method,route,status,latency_ms) VALUES($1,$2,$3,'GET','GET /old',200,1)`, account.ID, app.ID, baseline.ID); err != nil {
		t.Fatal(err)
	}
	check, err := store.CheckRouteRemoval(ctx, account.ID, app.ID, candidate.ID)
	if err != nil || check.Status != "blocked" || !strings.Contains(strings.Join(check.Blockers, ","), "old_route_observed") {
		t.Fatalf("missed gateway route label: %+v %v", check, err)
	}
	if _, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); err == nil {
		t.Fatal("renewed gateway traffic did not block cutover")
	}
	revision := policy.Revision
	_, err = store.ApproveRouteRemoval(ctx, account.ID, app.ID, "owner:test", api.ApproveRouteRemovalRequest{ExpectedPolicyRevision: &revision, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: approval.BaselineContractSHA256, CandidateContractSHA256: approval.CandidateContractSHA256, Mappings: approval.Mappings, AcknowledgeObservedOnly: true}, func(state.RouteRemovalContract, state.RouteRemovalContract, []api.RouteRemovalMapping) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "old_route_observed") {
		t.Fatalf("approved active gateway route: %v", err)
	}
}
