package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteRemovalPGAppCoverageIsolation(t *testing.T) {
	for _, name := range []string{"unrelated_loss", "own_loss", "unrelated_backlog", "own_backlog", "unknown_loss", "unreported_loss", "missing_heartbeat", "restart"} {
		t.Run(name, func(t *testing.T) {
			store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
			approval := routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
			_, other, _, _ := bindingPromotionFixture(t, store)
			var node, boot string
			if err := pool.QueryRow(ctx, `SELECT node_name,boot_id::text FROM request_telemetry_coverage LIMIT 1`).Scan(&node, &boot); err != nil {
				t.Fatal(err)
			}
			report := state.TelemetryCoverage{NodeName: node, BootID: boot, Sequence: 2, Enabled: true, SamplingBasisPoints: 10000, SourceAt: time.Now().UTC(), AppScoped: true}
			target := other.ID
			if strings.HasPrefix(name, "own_") {
				target = app.ID
			}
			switch name {
			case "own_loss", "unrelated_loss":
				report.DroppedTotal = 1
				report.AppGaps = []state.TelemetryAppGap{{AppID: target, DroppedCount: 1}}
			case "own_backlog", "unrelated_backlog":
				report.PendingCount = 1
				report.AppGaps = []state.TelemetryAppGap{{AppID: target, PendingCount: 1}}
			case "unreported_loss":
				report.DroppedTotal = 1
			case "unknown_loss":
				report.DroppedTotal = 1
				report.UnattributedDroppedTotal = 1
			case "restart":
				report.BootID = candidate.ID
			}
			if err := store.RecordTelemetryCoverage(ctx, report); err != nil {
				t.Fatal(err)
			}
			if name == "missing_heartbeat" {
				if _, err := pool.Exec(ctx, `UPDATE request_telemetry_coverage SET received_at=clock_timestamp()-interval '1 minute'`); err != nil {
					t.Fatal(err)
				}
			}
			check, err := store.CheckRouteRemoval(ctx, account.ID, app.ID, candidate.ID)
			healthy := strings.HasPrefix(name, "unrelated_")
			if err != nil || (check.Status == "passed") != healthy {
				t.Fatalf("app check: %+v %v", check, err)
			}
			if healthy && check.ApprovalID != approval.ID {
				t.Fatal("unrelated app replaced approval")
			}
			_, err = store.ApproveRouteRemoval(ctx, account.ID, app.ID, "owner:test", api.ApproveRouteRemovalRequest{ExpectedPolicyRevision: &policy.Revision, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: approval.BaselineContractSHA256, CandidateContractSHA256: approval.CandidateContractSHA256, Mappings: approval.Mappings, AcknowledgeObservedOnly: true}, func(state.RouteRemovalContract, state.RouteRemovalContract, []api.RouteRemovalMapping) error {
				return nil
			})
			if (err == nil) != healthy {
				t.Fatalf("app approval: %v", err)
			}
			_, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100)
			if (err == nil) != healthy {
				t.Fatalf("app traffic: %v", err)
			}
			if name == "own_backlog" {
				report.Sequence++
				report.PendingCount = 0
				report.AppGaps = nil
				report.SourceAt = time.Now().UTC()
				if err := store.RecordTelemetryCoverage(ctx, report); err != nil {
					t.Fatal(err)
				}
				check, err = store.CheckRouteRemoval(ctx, account.ID, app.ID, candidate.ID)
				if err != nil || check.Status != "blocked" || !strings.Contains(strings.Join(check.Blockers, ","), "telemetry_window_incomplete") {
					t.Fatalf("clearing backlog erased grace: %+v %v", check, err)
				}
			}
		})
	}
}

func TestRouteRemovalPGAppCoverageLock(t *testing.T) {
	for _, own := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrelated", true: "own"}[own], func(t *testing.T) {
			store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
			routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
			_, other, _, _ := bindingPromotionFixture(t, store)
			target := other.ID
			if own {
				target = app.ID
			}
			if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry_app_gaps(node_name,app_id,last_gap_at,pending_count,dropped_total,updated_at) SELECT node_name,$1,clock_timestamp()-interval '2 hours',0,0,clock_timestamp() FROM request_telemetry_coverage`, target); err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `UPDATE request_telemetry_app_gaps SET last_gap_at=clock_timestamp(),updated_at=clock_timestamp(),pending_count=1 WHERE app_id=$1`, target); err != nil {
				t.Fatal(err)
			}
			transitionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := store.UpdateDeploymentTraffic(transitionCtx, candidate.ID, 100); done <- err }()
			if own {
				waitRouteRemovalLock(t, ctx, pool, done)
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err = <-done; err == nil {
					t.Fatal("used app coverage from before lock wait")
				}
			} else {
				if err = <-done; err != nil {
					t.Fatalf("unrelated gap locked promotion: %v", err)
				}
			}
		})
	}
}
