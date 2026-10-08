package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedHealthyRouteCoverage(t *testing.T, pool *pgxpool.Pool, ctx context.Context) {
	t.Helper()
	// Mature heartbeat history is fixture data; runtime writers cannot backdate it.
	_, err := pool.Exec(ctx, `INSERT INTO request_telemetry_coverage(node_name,boot_id,sequence,enabled,sampling_basis_points,dropped_total,pending_count,source_at,received_at,healthy_since)
 SELECT name,gen_random_uuid(),1,true,10000,0,0,clock_timestamp(),clock_timestamp(),clock_timestamp()-interval '2 hours' FROM compute_nodes WHERE active`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRouteRemovalPGTelemetryCoverage(t *testing.T) {
	for _, tc := range []struct{ name, sql, blocker string }{
		{"missing", "DELETE FROM request_telemetry_coverage", "telemetry_coverage_missing"},
		{"new_node", "INSERT INTO compute_nodes(name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb) VALUES('coverage-new','unix:///run/test.sock',2,1024,10,512)", "telemetry_coverage_missing"},
		{"stale", "UPDATE request_telemetry_coverage SET received_at=clock_timestamp()-interval '1 minute'", "telemetry_coverage_stale"},
		{"sampled", "UPDATE request_telemetry_coverage SET sampling_basis_points=5000,healthy_since=NULL", "telemetry_sampled"},
		{"disabled", "UPDATE request_telemetry_coverage SET enabled=false,healthy_since=NULL", "telemetry_disabled"},
		{"backlog", "UPDATE request_telemetry_coverage SET pending_count=10,healthy_since=NULL", "telemetry_ingestion_pending"},
		{"delayed", "UPDATE request_telemetry_coverage SET source_at=clock_timestamp()-interval '5 minutes'", "telemetry_ingestion_pending"},
		{"gap", "UPDATE request_telemetry_coverage SET healthy_since=clock_timestamp(),received_at=clock_timestamp()", "telemetry_window_incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
			approval := routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
			if _, err := pool.Exec(ctx, tc.sql); err != nil {
				t.Fatal(err)
			}
			check, err := store.CheckRouteRemoval(ctx, account.ID, app.ID, candidate.ID)
			if err != nil || check.Status != "blocked" || !strings.Contains(strings.Join(check.Blockers, ","), tc.blocker) || len(check.NextActions) == 0 {
				t.Fatalf("coverage check: %+v %v", check, err)
			}
			_, err = store.ApproveRouteRemoval(ctx, account.ID, app.ID, "owner:test", api.ApproveRouteRemovalRequest{ExpectedPolicyRevision: &policy.Revision, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: approval.BaselineContractSHA256, CandidateContractSHA256: approval.CandidateContractSHA256, Mappings: approval.Mappings, AcknowledgeObservedOnly: true}, func(state.RouteRemovalContract, state.RouteRemovalContract, []api.RouteRemovalMapping) error {
				return nil
			})
			if err == nil {
				t.Fatal("approved removal with missing coverage")
			}
			if _, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); err == nil {
				t.Fatal("approved receipt bypassed missing coverage")
			}
		})
	}
}

func TestRouteRemovalPGCoverageHeartbeat(t *testing.T) {
	store, pool, ctx, _, _, _, _, _ := routeRemovalFixture(t)
	var node, boot string
	if err := pool.QueryRow(ctx, `SELECT node_name,boot_id::text FROM request_telemetry_coverage LIMIT 1`).Scan(&node, &boot); err != nil {
		t.Fatal(err)
	}
	report := state.TelemetryCoverage{NodeName: node, BootID: boot, Sequence: 2, Enabled: true, SamplingBasisPoints: 10000, SourceAt: time.Now().UTC()}
	if err := store.RecordTelemetryCoverage(ctx, report); err != nil {
		t.Fatal(err)
	}
	var mature bool
	if err := pool.QueryRow(ctx, `SELECT healthy_since<clock_timestamp()-interval '1 hour' FROM request_telemetry_coverage WHERE node_name=$1`, node).Scan(&mature); err != nil || !mature {
		t.Fatalf("healthy heartbeat erased history: %v", err)
	}
	if err := store.RecordTelemetryCoverage(ctx, report); err == nil {
		t.Fatal("replayed heartbeat accepted")
	}
	for _, change := range []string{"drops", "restart", "outage", "backlog"} {
		t.Run(change, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `UPDATE request_telemetry_coverage SET healthy_since=clock_timestamp()-interval '2 hours' WHERE node_name=$1`, node); err != nil {
				t.Fatal(err)
			}
			report.Sequence++
			report.SourceAt = time.Now().UTC()
			switch change {
			case "drops":
				report.DroppedTotal++
			case "restart":
				report.BootID = uuid.NewString()
			case "outage":
				if _, err := pool.Exec(ctx, `UPDATE request_telemetry_coverage SET received_at=clock_timestamp()-interval '1 minute' WHERE node_name=$1`, node); err != nil {
					t.Fatal(err)
				}
			case "backlog":
				report.PendingCount = 1
			}
			if err := store.RecordTelemetryCoverage(ctx, report); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT coalesce(healthy_since<clock_timestamp()-interval '1 hour',false) FROM request_telemetry_coverage WHERE node_name=$1`, node).Scan(&mature); err != nil || mature {
				t.Fatalf("gap retained healthy history: %v", err)
			}
		})
	}
	report.Sequence++
	report.PendingCount = 0
	report.SourceAt = time.Now().Add(time.Hour)
	if err := store.RecordTelemetryCoverage(ctx, report); err == nil {
		t.Fatal("future coverage accepted")
	}
}

func TestRouteRemovalPGCoverageLockRace(t *testing.T) {
	store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
	routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE request_telemetry_coverage SET healthy_since=NULL,pending_count=1`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); done <- err }()
	waitRouteRemovalLock(t, ctx, pool, done)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("used healthy coverage from before lock wait")
	}
}

func TestRouteRemovalPGCoverageShortApproval(t *testing.T) {
	store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
	revision := policy.Revision
	policy, err := store.SetRouteRemovalPolicy(ctx, account.ID, app.ID, api.SetRouteRemovalPolicyRequest{ExpectedRevision: &revision, Mode: "enforce", GracePeriod: "1h", MaxApprovalAge: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	approval := routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
	if approval.ObservationUntil.After(approval.ApprovedAt.Add(-2 * time.Minute)) {
		t.Fatal("unsettled observation window")
	}
	check, err := store.CheckRouteRemoval(ctx, account.ID, app.ID, candidate.ID)
	if err != nil || check.Status != "passed" {
		t.Fatalf("short receipt rejected: %+v %v", check, err)
	}
	if _, err = store.UpdateDeploymentTraffic(ctx, candidate.ID, 100); err != nil {
		t.Fatal(err)
	}
}
