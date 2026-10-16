package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentCacheConvergenceMemory(t *testing.T) { runCacheConvergence(t, state.NewMemStore()) }
func TestEnvironmentCacheConvergencePostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	account, app, node := runCacheConvergence(t, s)
	if _, err := pool.Exec(t.Context(), `UPDATE environment_cache_gateway_sessions SET heartbeat_at=clock_timestamp()-interval '91 seconds' WHERE node_id=$1`, node); err != nil {
		t.Fatal(err)
	}
	out, err := s.GetEnvironmentCacheConvergence(t.Context(), account, app, "staging")
	if err != nil || out.Status != "degraded" || out.StaleGateways != 1 {
		t.Fatalf("stale heartbeat=%+v %v", out, err)
	}

	var session string
	var started time.Time
	if err = pool.QueryRow(t.Context(), `SELECT session_id::text,started_at FROM environment_cache_gateway_sessions WHERE node_id=$1`, node).Scan(&session, &started); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), `UPDATE environment_cache_gateway_sessions SET heartbeat_at=clock_timestamp() WHERE node_id=$1`, node); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), `UPDATE compute_nodes SET gateway_target_url='tcp://127.0.0.1:9090' WHERE id=$1`, node); err != nil {
		t.Fatal(err)
	}
	out, err = s.GetEnvironmentCacheConvergence(t.Context(), account, app, "staging")
	if err != nil || out.Status != "degraded" || out.StaleGateways != 1 {
		t.Fatalf("replaced endpoint=%+v %v", out, err)
	}
	if err = s.BeginEnvironmentCacheGatewaySession(t.Context(), "cache-observer", session, started); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old process reclaimed replaced endpoint=%v", err)
	}
	nextSession := uuid.NewString()
	if err = s.BeginEnvironmentCacheGatewaySession(t.Context(), "cache-observer", nextSession, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordEnvironmentCacheGatewayObserved(t.Context(), "cache-observer", nextSession, app, out.EnvironmentID, out.Revision); err != nil {
		t.Fatal(err)
	}
	out, err = s.GetEnvironmentCacheConvergence(t.Context(), account, app, "staging")
	if err != nil || out.Status != "converged" {
		t.Fatalf("new endpoint session=%+v %v", out, err)
	}
}
func runCacheConvergence(t *testing.T, s state.Store) (string, string, string) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@cache-convergence.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "convergence"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "convergence", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	var zero int64
	req := api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &zero, Rules: []api.EnvironmentCacheRule{}}
	policies := s.(state.EnvironmentCachePolicyStore)
	saved, err := policies.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	reader := s.(state.EnvironmentCacheConvergenceReader)
	observer := s.(state.EnvironmentCacheGatewayObserver)
	check := func(status string, ack, pending, stale int64) {
		t.Helper()
		out, err := reader.GetEnvironmentCacheConvergence(ctx, account.ID, app.ID, "staging")
		if err != nil || out.Status != status || out.AcknowledgedGateways != ack || out.PendingGateways != pending || out.StaleGateways != stale || out.AcknowledgedGateways+out.PendingGateways+out.StaleGateways != out.ServingGateways {
			t.Fatalf("want %s, got %+v %v", status, out, err)
		}
	}
	check("degraded", 0, 0, 0) // no vacuous fleet success.
	role, target := "compute-node", "tcp://127.0.0.1:8080"
	node, err := s.CreateComputeNode(ctx, state.ComputeNode{Name: "cache-observer", TargetURL: "tcp://127.0.0.1:50051", GatewayTargetURL: &target, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true, Role: &role})
	if err != nil {
		t.Fatal(err)
	}
	check("degraded", 0, 0, 1) // a registered but unobserved gateway is not success.
	session, started := uuid.NewString(), time.Now().UTC().Add(-time.Second)
	if err = observer.BeginEnvironmentCacheGatewaySession(ctx, node.Name, session, started); err != nil {
		t.Fatal(err)
	}
	check("pending", 0, 1, 0)
	rows, err := observer.ListEnvironmentCacheGatewayPolicies(ctx, node.Name, session, "")
	if err != nil || len(rows) != 1 || rows[0].Revision != 1 {
		t.Fatalf("repair list=%+v %v", rows, err)
	}
	if err = observer.RecordEnvironmentCacheGatewayObserved(ctx, node.Name, session, app.ID, env.ID, 1); err != nil {
		t.Fatal(err)
	}
	check("converged", 1, 0, 0)
	rows, err = observer.ListEnvironmentCacheGatewayPolicies(ctx, node.Name, session, "")
	if err != nil || len(rows) != 0 {
		t.Fatal("already acknowledged revision reloaded", err)
	}
	// Changing intent immediately invalidates the old observation, with no notification.
	req.RequestID = uuid.NewString()
	req.ExpectedRevision = &saved.Revision
	newer, err := policies.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	check("pending", 0, 1, 0)
	if err = observer.RecordEnvironmentCacheGatewayObserved(ctx, node.Name, session, app.ID, env.ID, 1); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("superseded ack=%v", err)
	}
	if err = observer.RecordEnvironmentCacheGatewayObserved(ctx, node.Name, session, app.ID, env.ID, newer.Revision); err != nil {
		t.Fatal(err)
	}
	// A restarted process cannot reuse prior observations or be overwritten by the old boot.
	restarted := uuid.NewString()
	if err = observer.BeginEnvironmentCacheGatewaySession(ctx, node.Name, restarted, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	check("pending", 0, 1, 0)
	if err = observer.RefreshEnvironmentCacheGatewaySession(ctx, node.Name, session); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old heartbeat=%v", err)
	}
	if err = observer.BeginEnvironmentCacheGatewaySession(ctx, node.Name, session, started); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old boot re-registered=%v", err)
	}
	if err = observer.RecordEnvironmentCacheGatewayObserved(ctx, node.Name, session, app.ID, env.ID, newer.Revision); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old session ack=%v", err)
	}
	if err = observer.RecordEnvironmentCacheGatewayObserved(ctx, node.Name, restarted, app.ID, env.ID, newer.Revision); err != nil {
		t.Fatal(err)
	}
	check("converged", 1, 0, 0)
	if _, err = reader.GetEnvironmentCacheConvergence(ctx, uuid.NewString(), app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account=%v", err)
	}
	otherTarget := "tcp://127.0.0.1:8081"
	otherNode, err := s.CreateComputeNode(ctx, state.ComputeNode{Name: "other-cache-observer", TargetURL: "tcp://127.0.0.1:50052", GatewayTargetURL: &otherTarget, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true, Role: &role})
	if err != nil {
		t.Fatal(err)
	}
	check("degraded", 1, 0, 1)
	otherSession := uuid.NewString()
	if err = observer.BeginEnvironmentCacheGatewaySession(ctx, otherNode.Name, otherSession, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	check("pending", 1, 1, 0)
	if err = observer.RecordEnvironmentCacheGatewayObserved(ctx, otherNode.Name, otherSession, app.ID, env.ID, newer.Revision); err != nil {
		t.Fatal(err)
	}
	check("converged", 2, 0, 0)
	return account.ID, app.ID, node.ID
}
