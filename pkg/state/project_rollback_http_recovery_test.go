package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgProjectRollbackHTTPRecovery(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE compute_nodes SET gateway_target_url = NULL`)
	for _, node := range []string{"rollback-edge-a", "rollback-edge-b"} {
		exec(`INSERT INTO compute_nodes(name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb,role,lifecycle,gateway_target_url)
		      VALUES($1,'unix:///run/test.sock',2,1024,10,512,'compute-only','active','tcp://127.0.0.1:8080')`, node)
	}
	acct, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "rollback-recovery"})
	if err != nil {
		t.Fatal(err)
	}
	createApp := func(name string) state.App {
		t.Helper()
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "rollback-" + name, WorkloadName: name,
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	app, downstream := createApp("http"), createApp("downstream")
	deploy := func(app state.App) state.Deployment {
		t.Helper()
		suffix := uuid.NewString()
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + suffix, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentRootfs(ctx, d.ID, "/layers/"+suffix, "layers/"+suffix, 4096); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		d, err = s.DeploymentByID(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for range api.SnapshotRollbackRetentionDeployments - 1 {
		deploy(app)
	}
	target, current := deploy(app), deploy(app)
	// Simulate an older revision already restored to production. Once this
	// cutover retires it the normal generation window does not protect it.
	exec(`UPDATE deployments SET created_at = created_at - interval '1 day' WHERE id = $1`, current.ID)
	sidecar := current.RootfsKey + "-sidecar"
	if _, err := s.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
		DeploymentID: current.ID, SidecarName: "side", StorageKey: sidecar, Bytes: 2048, ContentDigest: "sha256:" + uuid.NewString(),
	}); err != nil {
		t.Fatal(err)
	}
	undoKeys := []string{current.RootfsKey, sidecar, "sigs/" + current.RootfsKey + ".sig", "sigs/" + sidecar + ".sig"}
	assertUndoKeysRetained := func() {
		t.Helper()
		for _, key := range undoKeys {
			if _, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key); err != nil || eligible {
				t.Fatalf("GC claimed undo artifact %q: eligible %v, error %v", key, eligible, err)
			}
		}
	}
	peerTarget, peerCurrent := deploy(downstream), deploy(downstream)
	plan := func(app state.App, target, current state.Deployment) api.ProjectRollbackStep {
		return api.ProjectRollbackStep{AppID: app.ID, Scope: api.DefaultEnvScope, CurrentID: current.ID, TargetID: target.ID,
			CurrentServedAt: current.RolloutCompletedAt, AttemptID: uuid.NewString(), Status: "planned"}
	}
	step, peerStep := plan(app, target, current), plan(downstream, peerTarget, peerCurrent)
	step.Health = &api.ProjectRollbackHealth{Status: "pending"}
	step.LiveTraffic = &api.ProjectRollbackLiveTraffic{Status: "pending"}
	budget := int64(500)
	policy := state.ProjectRollbackLiveTrafficPolicy{Revision: 1, Routes: []api.RouteMonitorRoute{
		{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget},
	}}
	preparationDeadline := time.Now().Add(5 * time.Minute)
	record := state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
		ID: uuid.NewString(), ProjectID: project.ID, Verify: true, VerifyBeforeCutover: true, StabilizeForSeconds: 30, VerifyLiveTraffic: true, Status: "running", Phase: api.ProjectRollbackPreparing,
		PreparationDeadline: &preparationDeadline, Steps: []api.ProjectRollbackStep{step, peerStep},
	}, HealthProbes: map[string][]api.ProjectReleaseProbe{
		app.ID:        {{AppID: app.ID, Slug: app.Slug, Method: "GET", Path: "/ready"}},
		downstream.ID: {{AppID: downstream.ID, Slug: downstream.Slug, Method: "HEAD", Path: "/ready"}},
	}, LiveTrafficPolicies: map[string]state.ProjectRollbackLiveTrafficPolicy{app.ID: policy}}
	if err := s.CreateProjectRollback(ctx, acct.ID, record); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateCheckedRollback(state.WithProjectRollbackStep(ctx, claim, step), acct.ID, app.ID, target.ID, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	read := func(want string) api.ProjectRollbackTraffic {
		t.Helper()
		rows, err := s.ProjectRollbackHTTPRecovery(ctx, record.ID)
		got := rows[child.ID]
		if err != nil || got.Status != want {
			t.Fatalf("traffic = %+v, err = %v, want %s", got, err, want)
		}
		return got
	}
	read("awaiting_cutover")
	peer, err := s.CreateCheckedRollback(state.WithProjectRollbackStep(ctx, claim, peerStep), acct.ID, downstream.ID, peerTarget.ID, peerCurrent.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	markPrepared := func(operation api.RollbackOperation) {
		t.Helper()
		if err := s.MarkDeploymentLive(ctx, operation.TargetDeploymentID); err != nil {
			t.Fatal(err)
		}
		operation, err := s.GetCheckedRollback(ctx, acct.ID, operation.AppID, operation.ID)
		if err != nil || operation.ProjectRollbackID != record.ID {
			t.Fatalf("project child = %+v, %v", operation, err)
		}
		if _, err := s.CommitCheckedRollback(ctx, operation); !errors.Is(err, state.ErrProjectRollbackHeld) {
			t.Fatalf("unarmed cutover returned %v", err)
		}
		if err := s.UpdateCheckedRollback(ctx, operation, "ready", api.CodeProjectRollbackPrepared, nil); err != nil {
			t.Fatal(err)
		}
	}
	markPrepared(child)
	// A process that knows only the original checked-rollback token must
	// still be unable to put this target into production.
	old, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(ctx, `SELECT set_config('faas.checked_rollback_request', $1, true)`, child.ID); err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(ctx, `UPDATE deployments SET traffic_percent = 100 WHERE id = $1`, target.ID)
	_ = old.Rollback(ctx)
	var dbErr *pgconn.PgError
	if !errors.As(err, &dbErr) || dbErr.ConstraintName != "checked_rollback_required" || dbErr.Message != "project rollback preparation has not released this target" {
		t.Fatalf("old traffic writer received %v", err)
	}
	// Even a preparation-aware writer needs the separate health-aware
	// authorization for this opt-in operation.
	old, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(ctx, `SELECT set_config('faas.checked_rollback_request', $1, true),
		set_config('faas.project_rollback_cutover', $1, true)`, child.ID); err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(ctx, `UPDATE deployments SET traffic_percent = 100 WHERE id = $1`, target.ID)
	_ = old.Rollback(ctx)
	if !errors.As(err, &dbErr) || dbErr.Message != "verified project rollback requires health authorization" {
		t.Fatalf("preparation-only traffic writer received %v", err)
	}
	if result, err := pool.Exec(ctx, `UPDATE project_rollback_operations SET receipt = receipt - 'verify' - 'health_probes' WHERE id = $1`, claim.ID); err != nil || result.RowsAffected() != 0 {
		t.Fatalf("old receipt writer result = %v, %v", result, err)
	}

	old, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(ctx, `SELECT set_config('faas.checked_rollback_request', $1, true),
		set_config('faas.project_rollback_cutover', $1, true),
		set_config('faas.project_rollback_verified_cutover', $1, true)`, child.ID); err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(ctx, `UPDATE deployments SET traffic_percent = 100 WHERE id = $1`, target.ID)
	_ = old.Rollback(ctx)
	if !errors.As(err, &dbErr) || dbErr.Message != "project rollback requires pre-cutover health authorization" {
		t.Fatalf("post-cutover-only traffic writer received %v", err)
	}
	if result, err := pool.Exec(ctx, `UPDATE project_rollback_operations SET receipt = receipt - 'verify_before_cutover' WHERE id = $1`, claim.ID); err != nil || result.RowsAffected() != 0 {
		t.Fatalf("pre-cutover unaware receipt writer result = %v, %v", result, err)
	}

	if result, err := pool.Exec(ctx, `UPDATE project_rollback_operations SET receipt = receipt - 'stabilize_for_seconds' WHERE id = $1`, claim.ID); err != nil || result.RowsAffected() != 0 {
		t.Fatalf("single-probe coordinator could rewrite stabilization policy: %v, %v", result, err)
	}
	old, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(ctx, `SELECT set_config('faas.checked_rollback_request', $1, true),
		set_config('faas.project_rollback_cutover', $1, true),
		set_config('faas.project_rollback_verified_cutover', $1, true),
		set_config('faas.project_rollback_pre_cutover_verified', $1, true)`, child.ID); err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(ctx, `UPDATE deployments SET traffic_percent = 100 WHERE id = $1`, target.ID)
	_ = old.Rollback(ctx)
	if !errors.As(err, &dbErr) || dbErr.Message != "project rollback requires live-traffic health authorization" {
		t.Fatalf("live-traffic unaware writer received %v", err)
	}
	if result, err := pool.Exec(ctx, `UPDATE project_rollback_operations SET receipt = receipt - 'live_traffic_policies' WHERE id = $1`, claim.ID); err != nil || result.RowsAffected() != 0 {
		t.Fatalf("older coordinator could rewrite live-traffic budgets: %v, %v", result, err)
	}

	checked := time.Now()
	claim.Steps[0].PreCutoverHealth = &api.ProjectRollbackPreCutoverHealth{Status: "passed", AttemptID: child.ID, CheckedAt: &checked}
	claim.Steps[1].PreCutoverHealth = &api.ProjectRollbackPreCutoverHealth{Status: "passed", AttemptID: peer.ID, CheckedAt: &checked}

	claim.Phase = api.ProjectRollbackCutover
	if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitCheckedRollback(ctx, child); !errors.Is(err, state.ErrProjectRollbackHeld) {
		t.Fatalf("cutover without the downstream receipt ready returned %v", err)
	}
	claim, err = s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim.Phase = api.ProjectRollbackPreparing
	if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	markPrepared(peer)
	claim, err = s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim.Phase = api.ProjectRollbackCutover
	claim.Steps[1].PreCutoverHealth.AttemptID = uuid.NewString()
	if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitCheckedRollback(ctx, child); !errors.Is(err, state.ErrProjectRollbackHeld) {
		t.Fatalf("first cutover with stale downstream probe evidence returned %v", err)
	}
	claim, err = s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim.Steps[1].PreCutoverHealth.AttemptID = peer.ID
	if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitCheckedRollback(ctx, peer); !errors.Is(err, state.ErrProjectRollbackHeld) {
		t.Fatalf("out-of-order cutover returned %v", err)
	}
	read("awaiting_cutover")
	if _, err := s.CommitCheckedRollback(ctx, child); err != nil {
		t.Fatal(err)
	}
	if holds, err := s.ProjectRollbackUndoArtifactHolds(ctx, record.ID); err != nil ||
		len(holds) != 1 || holds[current.ID].DeploymentID != current.ID || holds[current.ID].ExpiresAt != nil {
		t.Fatalf("cutover did not atomically retain the original revision: %+v, %v", holds, err)
	}
	assertUndoKeysRetained()
	cutover := read("awaiting_gateways")
	if cutover.RouteGeneration == nil || cutover.RoutingCommittedAt == nil {
		t.Fatalf("missing cutover: %+v", cutover)
	}
	route := db.DeploymentRouteChangedPayload{AppID: app.ID, DeploymentID: target.ID, Generation: *cutover.RouteGeneration}
	exec(`UPDATE project_rollback_http_recovery SET expires_at = clock_timestamp() - interval '1 second',
		next_notify_at = NULL WHERE rollback_id = $1`, child.ID)
	if r, err := s.ProjectRollbackHealthRoute(ctx, record.ID, child.ID); err != nil ||
		r.RouteGeneration == nil || *r.RouteGeneration != route.Generation || r.GatewaysAppliedAt != nil {
		t.Fatalf("rearmed gateway wait = %+v, %v", r, err)
	}
	read("awaiting_gateways")

	ack := func(route db.DeploymentRouteChangedPayload, node string) error {
		_, err := s.AcknowledgeProjectRollbackHTTPRoute(ctx, route, node)
		return err
	}
	response := func(route db.DeploymentRouteChangedPayload, started, finished time.Time, want bool) {
		t.Helper()
		got, err := s.RecordProjectRollbackHTTPResponse(ctx, route, "rollback-edge-a", started, finished)
		if err != nil || got != want {
			t.Fatalf("response for %+v: recorded %v, err %v, want %v", route, got, err, want)
		}
	}
	tmin := time.Now().UTC()
	oldGeneration := route
	oldGeneration.Generation--
	if err := ack(oldGeneration, "rollback-edge-b"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale generation ack: %v", err)
	}
	if err := ack(route, "rollback-edge-b"); err != nil {
		t.Fatal(err)
	}
	response(route, tmin, time.Now().UTC(), false)
	read("awaiting_gateways")

	replay := func(rollbackID string) {
		t.Helper()
		exec(`UPDATE project_rollback_http_recovery SET next_notify_at = clock_timestamp() WHERE rollback_id = $1`, rollbackID)
		if err := s.ReplayProjectRollbackRoutes(ctx); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE compute_nodes SET lifecycle = 'unavailable' WHERE name = 'rollback-edge-a'`)
	replay(child.ID)
	read("awaiting_gateways")
	exec(`UPDATE compute_nodes SET lifecycle = 'active' WHERE name = 'rollback-edge-a'`)

	exec(`UPDATE compute_nodes SET lifecycle = 'retired' WHERE name = 'rollback-edge-b'`)
	exec(`UPDATE project_rollback_http_recovery SET expires_at = clock_timestamp() - interval '1 second',
		next_notify_at = NULL WHERE rollback_id = $1`, child.ID)
	if r, err := s.ProjectRollbackHealthRoute(ctx, record.ID, child.ID); err != nil ||
		r.RouteGeneration == nil || *r.RouteGeneration != route.Generation {
		t.Fatalf("rearmed gateway wait after retirement = %+v, %v", r, err)
	}
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := holder.Exec(ctx, `SELECT rollback_id FROM project_rollback_http_recovery WHERE rollback_id = $1 FOR UPDATE`, child.ID); err != nil {
		t.Fatal(err)
	}
	ackCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.AcknowledgeProjectRollbackHTTPRoute(ackCtx, route, "rollback-edge-a")
		done <- err
	}()
	for deadline := time.Now().Add(5 * time.Second); ; {
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			    WHERE datname = current_database() AND $1 = ANY(pg_blocking_pids(pid)))`,
			int32(holder.Conn().PgConn().PID())).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("gateway acknowledgement did not wait on the receipt lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Commit the replacement and the same roster replay would publish while
	// the ACK is blocked. A must still see C when it acquires the lock.
	if _, err := holder.Exec(ctx, `
		INSERT INTO compute_nodes(name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb,role,lifecycle,gateway_target_url)
		VALUES('rollback-edge-c','unix:///run/test.sock',2,1024,10,512,'compute-only','active','tcp://127.0.0.1:8080')`); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, `
		UPDATE project_rollback_http_recovery
		SET expected_gateways = ARRAY['rollback-edge-a','rollback-edge-c'], acknowledged_gateways = '{}'
		WHERE rollback_id = $1`, child.ID); err != nil {
		t.Fatal(err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	read("awaiting_gateways")
	replay(child.ID)
	read("awaiting_gateways")
	if err := ack(route, "rollback-edge-b"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("retired gateway ack: %v", err)
	}
	s = state.NewPgStore(pool)
	if err := ack(route, "rollback-edge-c"); err != nil {
		t.Fatal(err)
	}
	claim, err = s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim.Steps[0].Status = "rolled_back"
	if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitCheckedRollback(ctx, peer); !errors.Is(err, state.ErrProjectRollbackHeld) {
		t.Fatalf("downstream cutover without a successful health checkpoint returned %v", err)
	}
	barrier := read("awaiting_traffic")
	if barrier.GatewaysAppliedAt == nil {
		t.Fatal("missing gateway barrier timestamp")
	}
	outgoing := route
	outgoing.DeploymentID = current.ID
	started := time.Now().UTC()
	response(outgoing, started, time.Now().UTC(), false)
	response(route, tmin, time.Now().UTC(), false)
	read("awaiting_traffic")
	if err := s.ReplayProjectRollbackRoutes(ctx); err != nil {
		t.Fatal(err)
	}
	response(route, started, time.Now().UTC(), true)
	recovered := read("verified")
	if recovered.FirstSuccessAt == nil || recovered.FirstSuccessAt.Before(*barrier.GatewaysAppliedAt) {
		t.Fatalf("invalid recovery milestones: %+v", recovered)
	}
	healthRoute, err := s.ProjectRollbackHealthRoute(ctx, record.ID, child.ID)
	if err != nil || healthRoute.RouteGeneration == nil || *healthRoute.RouteGeneration != route.Generation || healthRoute.GatewaysAppliedAt == nil {
		t.Fatalf("health route = %+v, %v", healthRoute, err)
	}
	persistHealth := func(generation int64, elapsed time.Duration) {
		t.Helper()
		claim, err := s.ClaimProjectRollback(ctx, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		last := time.Now()
		first := last.Add(-elapsed)
		claim.Steps[0].Health = &api.ProjectRollbackHealth{Status: "passed", RouteGeneration: &generation, HealthySince: &first, CheckedAt: &last}
		if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
			t.Fatal(err)
		}
	}
	persistHealth(route.Generation+1, 30*time.Second)
	if _, err := s.CommitCheckedRollback(ctx, peer); !errors.Is(err, state.ErrProjectRollbackHeld) {
		t.Fatalf("downstream cutover with a stale health checkpoint returned %v", err)
	}
	persistHealth(route.Generation, 5*time.Second)
	if _, err := s.CommitCheckedRollback(ctx, peer); !errors.Is(err, state.ErrProjectRollbackHeld) {
		t.Fatalf("downstream cutover on an incomplete window returned %v", err)
	}
	persistHealth(route.Generation, 30*time.Second)
	if _, err := s.CommitCheckedRollback(ctx, peer); !errors.Is(err, state.ErrProjectRollbackHeld) {
		t.Fatalf("downstream cutover without live-traffic evidence returned %v", err)
	}

	step.LiveTraffic.RouteGeneration = &route.Generation
	windows := routehealth.Windows(time.Now())
	readReport := func(want, reason string) api.RouteMonitorReport {
		t.Helper()
		got, err := s.ProjectRollbackLiveTrafficReport(ctx, record.ID, step, policy)
		if err != nil || got.Status != want || (reason != "" && got.Routes[0].Windows[0].ErrorReason != reason) {
			t.Fatalf("live-traffic report = %+v, %v, want %s (%s)", got, err, want, reason)
		}
		return got
	}
	insertRequest := func(deployment, path string, status, count int32, at time.Time) {
		t.Helper()
		id := func(s string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(s), Valid: true} }
		err := sqlc.New().InsertRequestTelemetry(ctx, pool, sqlc.InsertRequestTelemetryParams{
			AccountID: id(acct.ID), AppID: id(app.ID), DeploymentID: id(deployment),
			Method: "POST", Route: "POST " + path, Status: status, Count: count, LatencyMs: 1,
			ReceivedAt: state.NewPgtypeTime(at), UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", FlagEvidenceJson: "[]",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sampleTimes := []time.Time{windows[0].Start.Add(time.Second), windows[1].Start.Add(time.Second), windows[1].End.Add(time.Second)}
	for _, at := range sampleTimes {
		insertRequest(current.ID, "/checkout", 503, 40, at)
		insertRequest(target.ID, "/other", 200, 80, at)
	}
	insertRequest(target.ID, "/checkout", 503, 40, windows[0].Start.Add(-time.Second))
	readReport("unknown", "observation_window_not_elapsed")
	// Place the completed serving period immediately before the sampled windows.
	cutoverAt := windows[0].Start.Add(-2 * time.Second)
	exec(`UPDATE deployments SET rollout_completed_at = $2 WHERE id = $1`, target.ID, cutoverAt)
	exec(`UPDATE project_rollback_http_recovery SET routing_committed_at = $2, gateways_applied_at = $3 WHERE rollback_id = $1`,
		child.ID, cutoverAt, windows[0].Start)
	if got := readReport("unknown", "insufficient_requests"); got.Routes[0].Windows[0].Observed.Requests != 0 {
		t.Fatalf("observations included another route, revision or serving period: %+v", got)
	}
	for _, at := range sampleTimes {
		insertRequest(target.ID, "/checkout", 200, 30, at)
		insertRequest(target.ID, "/checkout", 503, 10, at)
	}
	readReport("violated", "server_error_budget_exceeded")
	for _, at := range sampleTimes {
		insertRequest(target.ID, "/checkout", 200, 200, at)
	}
	report := readReport("healthy", "error_budget_satisfied")
	resumeAt := time.Now()
	step.LiveTraffic.ObserveAfter = &resumeAt
	readReport("unknown", "observation_window_not_elapsed")
	step.LiveTraffic.ObserveAfter = nil
	staleGeneration := route.Generation + 1
	step.LiveTraffic.RouteGeneration = &staleGeneration
	if _, err := s.ProjectRollbackLiveTrafficReport(ctx, record.ID, step, policy); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("wrong route generation returned %v", err)
	}
	claim, err = s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim.Steps[0].LiveTraffic = &api.ProjectRollbackLiveTraffic{Status: "passed", CheckedAt: &report.CheckedAt, RouteGeneration: &route.Generation}
	if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitCheckedRollback(ctx, peer); err != nil {
		t.Fatalf("downstream cutover with the exact health checkpoint returned %v", err)
	}
	peerTraffic, err := s.ProjectRollbackHealthRoute(ctx, record.ID, peer.ID)
	if err != nil || peerTraffic.RouteGeneration == nil {
		t.Fatalf("downstream gateway barrier = %+v, %v", peerTraffic, err)
	}
	peerRoute := db.DeploymentRouteChangedPayload{AppID: downstream.ID, DeploymentID: peerTarget.ID, Generation: *peerTraffic.RouteGeneration}
	if err := ack(peerRoute, "rollback-edge-a"); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE compute_nodes SET lifecycle = 'retired' WHERE name = 'rollback-edge-c'`)
	exec(`UPDATE project_rollback_http_recovery SET next_notify_at = clock_timestamp() WHERE rollback_id = $1`, peer.ID)
	replayWhileGatewayJoins(t, pool, peerRoute)
	if r, err := s.ProjectRollbackHealthRoute(ctx, record.ID, peer.ID); err != nil || r.GatewaysAppliedAt != nil {
		t.Fatalf("replay bypassed the replacement gateway: %+v, %v", r, err)
	}
	exec(`UPDATE compute_nodes SET lifecycle = 'retired' WHERE name = 'rollback-edge-d'`)
	replay(peer.ID)
	if r, err := s.ProjectRollbackHealthRoute(ctx, record.ID, peer.ID); err != nil || r.GatewaysAppliedAt == nil {
		t.Fatalf("gateway retirement did not release the acknowledged route: %+v, %v", r, err)
	}

	// The paused receipt owns both committed children. Undo only accepts
	// their exact inverse in reverse order and reserves one durable receipt.
	claim, err = s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim.Status = "blocked"
	claim.Steps[0].ReceiptID = child.ID
	claim.Steps[1].Status, claim.Steps[1].ReceiptID = "rolled_back", peer.ID
	if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	source, err := s.GetProjectRollback(ctx, acct.ID, project.ID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	holds, err := s.ProjectRollbackUndoArtifactHolds(ctx, source.ID)
	deadline := holds[current.ID].ExpiresAt
	if err != nil || len(holds) != 2 || deadline == nil || time.Until(*deadline) < 23*time.Hour ||
		time.Until(*deadline) > 24*time.Hour {
		t.Fatalf("paused artifact holds = %+v, %v", holds, err)
	}
	assertUndoKeysRetained()
	resuming := source
	resuming.Status = "running"
	resuming.ResumePlanHash = strings.Repeat("c", 64)
	var pins []state.ProjectRollbackResumePin
	for _, pair := range []struct {
		app    state.App
		target string
	}{{app, target.ID}, {downstream, peerTarget.ID}} {
		live, err := s.DeploymentByID(ctx, pair.target)
		if err != nil || live.RolloutCompletedAt == nil {
			t.Fatalf("read serving period = %+v, %v", live, err)
		}
		pins = append(pins, state.ProjectRollbackResumePin{
			AppID: pair.app.ID, Slug: pair.app.Slug, Scope: api.DefaultEnvScope,
			CurrentID: live.ID, CurrentServedAt: *live.RolloutCompletedAt,
		})
	}
	period := pins[1].CurrentServedAt
	pins[1].CurrentServedAt = period.Add(-time.Second)
	if err := s.ResumeProjectRollback(state.WithProjectRollbackResumeReview(ctx, pins), acct.ID, resuming); !errors.Is(err, state.ErrProjectRollbackResumePlanChanged) {
		t.Fatalf("reviewed resume accepted a changed serving period: %v", err)
	}
	if paused, err := s.GetProjectRollback(ctx, acct.ID, project.ID, record.ID); err != nil ||
		paused.Status != "blocked" || paused.Version != source.Version || paused.ResumePlanHash != "" {
		t.Fatalf("stale reviewed resume changed source: %+v, %v", paused, err)
	}
	pins[1].CurrentServedAt = period
	if err := s.ResumeProjectRollback(state.WithProjectRollbackResumeReview(ctx, pins), acct.ID, resuming); err != nil {
		t.Fatalf("resume with live undo holds: %v", err)
	}
	if holds, err := s.ProjectRollbackUndoArtifactHolds(ctx, source.ID); err != nil ||
		len(holds) != 2 || holds[current.ID].ExpiresAt != nil {
		t.Fatalf("resumed artifact holds = %+v, %v", holds, err)
	}
	claim, err = s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil || claim.ID != source.ID || claim.ResumePlanHash != resuming.ResumePlanHash {
		t.Fatalf("claim resumed rollback = %+v, %v", claim, err)
	}
	claim.Status = "blocked"
	claim.ResumePlanHash = "" // A worker from the preceding release omits the field.
	if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	source, err = s.GetProjectRollback(ctx, acct.ID, project.ID, record.ID)
	if err != nil || source.ResumePlanHash != resuming.ResumePlanHash {
		t.Fatalf("resume approval missing from worker save: %+v, %v", source, err)
	}

	// Exercise the committed audit fallback for non-HTTP cuts.
	exec(`DELETE FROM project_rollback_http_recovery WHERE rollback_id = $1`, peer.ID)
	// A service handoff stores nanoseconds in JSON alongside the database
	// timestamp. A sub-microsecond encoding difference is still the same cutover.
	exec(`UPDATE deployments SET service_rollout_handoff = jsonb_build_object(
	    'completed_at', to_char(rollout_completed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US') || '9Z',
	    'bindings_check', (SELECT jsonb_build_object('request_id', id, 'audit_id', receipt->>'audit_id')
	      FROM deployment_rollback_operations WHERE id = $2)) WHERE id = $1`, target.ID, child.ID)
	exec(`UPDATE deployment_rollback_operations SET receipt = receipt || '{"service":true}' WHERE id = $1`, child.ID)
	if _, err := s.ProjectRollbackUndoServingAt(ctx, source.ID, source.Steps[0]); err != nil {
		t.Fatalf("completed service serving period: %v", err)
	}
	exec(`UPDATE deployment_rollback_operations SET receipt = receipt || '{"service":false}' WHERE id = $1`, child.ID)
	exec(`UPDATE deployments SET service_rollout_handoff = '{}' WHERE id = $1`, target.ID)
	inverse := func(i int) api.ProjectRollbackStep {
		t.Helper()
		step := source.Steps[i]
		at, err := s.ProjectRollbackUndoServingAt(ctx, source.ID, step)
		if err != nil || at == nil {
			t.Fatalf("completed serving period for step %d: %v %v", i, at, err)
		}
		return api.ProjectRollbackStep{AppID: step.AppID, Scope: step.Scope, CurrentID: step.TargetID,
			TargetID: step.CurrentID, CurrentServedAt: at, AttemptID: uuid.NewString(), Status: "planned"}
	}
	firstInverse, secondInverse := inverse(1), inverse(0)
	undo := state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
		ID: uuid.NewString(), ProjectID: project.ID, UndoOf: source.ID, Status: "running",
		Phase: api.ProjectRollbackPreparing, PreparationDeadline: &preparationDeadline, PlanHash: strings.Repeat("a", 64),
		Steps: []api.ProjectRollbackStep{secondInverse, firstInverse},
	}}
	if _, err := s.CreateProjectRollbackUndo(ctx, acct.ID, source, undo); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted undo in original cutover order: %v", err)
	}
	undo.Steps = []api.ProjectRollbackStep{firstInverse, secondInverse}
	wrongPeriod := secondInverse.CurrentServedAt.Add(-time.Second)
	undo.Steps[1].CurrentServedAt = &wrongPeriod
	if _, err := s.CreateProjectRollbackUndo(ctx, acct.ID, source, undo); !errors.Is(err, state.ErrProjectRollbackUndoPlanChanged) {
		t.Fatalf("reviewed undo accepted a different serving period: %v", err)
	}
	undo.Steps[1] = secondInverse
	undo.ReleaseReviews = map[string][]state.ReleaseRun{
		secondInverse.AppID: {{DeploymentID: uuid.NewString(), Revision: 99, Status: state.AppTaskSucceeded, StartedAt: time.Now()}},
	}
	if _, err := s.CreateProjectRollbackUndo(ctx, acct.ID, source, undo); !errors.Is(err, state.ErrProjectRollbackUndoPlanChanged) {
		t.Fatalf("reviewed undo accepted different release runs: %v", err)
	}
	undo.ReleaseReviews = nil
	accepted, err := s.CreateProjectRollbackUndo(ctx, acct.ID, source, undo)
	if err != nil || accepted.ID != undo.ID || accepted.PlanHash != undo.PlanHash {
		t.Fatalf("undo receipt = %+v, %v", accepted, err)
	}
	if holds, err := s.ProjectRollbackUndoArtifactHolds(ctx, accepted.ID); err != nil ||
		len(holds) != 2 || holds[current.ID].DeploymentID != current.ID || holds[current.ID].ExpiresAt != nil {
		t.Fatalf("undo did not inherit its target holds: %+v, %v", holds, err)
	}
	assertUndoKeysRetained()
	undo.ID = uuid.NewString()
	replayed, err := s.CreateProjectRollbackUndo(ctx, acct.ID, source, undo)
	if err != nil || replayed.ID != accepted.ID || replayed.PlanHash != accepted.PlanHash {
		t.Fatalf("undo replay = %+v, %v", replayed, err)
	}
	undo.PlanHash = strings.Repeat("b", 64)
	if _, err := s.CreateProjectRollbackUndo(ctx, acct.ID, source, undo); !errors.Is(err, state.ErrProjectRollbackUndoPlanChanged) {
		t.Fatalf("reviewed undo replay accepted a different plan: %v", err)
	}
	undoClaim, err := s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil || undoClaim.ID != accepted.ID {
		t.Fatalf("undo claim = %+v, %v", undoClaim, err)
	}
	undoClaim.Status = "blocked"
	if err := s.SaveProjectRollback(ctx, undoClaim, 0); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE project_rollback_undo_artifact_holds SET expires_at = clock_timestamp() - interval '1 second'
		WHERE operation_id = $1`, accepted.ID)
	for _, key := range undoKeys {
		if _, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key); err != nil || !eligible {
			t.Fatalf("expired undo artifact %q: eligible %v, error %v", key, eligible, err)
		}
	}
	source, err = s.GetProjectRollback(ctx, acct.ID, project.ID, source.ID)
	if err != nil || source.UndoOperationID != accepted.ID {
		t.Fatalf("source link = %+v, %v", source, err)
	}
	if err := s.ResumeProjectRollback(ctx, acct.ID, source); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("linked source was resumable: %v", err)
	}
	tag, err := pool.Exec(ctx, `UPDATE project_rollback_operations SET status = 'running',
	receipt = receipt - 'undo_operation_id' WHERE id = $1`, source.ID)
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("legacy resume could strip the undo link: %v, %v", tag, err)
	}
	deploy(app)
	if _, err := s.ProjectRollbackUndoServingAt(ctx, source.ID, source.Steps[0]); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("past route still qualified for undo: %v", err)
	}
	if _, err := s.ProjectRollbackHealthRoute(ctx, record.ID, child.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("past route still qualified for health verification: %v", err)
	}
	read("verified")

}

// A view pauses only the replay connection after it has taken a statement
// snapshot but before it locks the due receipt.
func replayWhileGatewayJoins(t *testing.T, pool *pgxpool.Pool, route db.DeploymentRouteChangedPayload) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION pause_project_rollback_replay() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$
		BEGIN
			IF current_setting('application_name') = 'pgtest_project_rollback_replay' THEN
				PERFORM pg_advisory_xact_lock(93473893, hashtext(current_schema()));
			END IF;
			RETURN true;
		END $$;
		ALTER TABLE project_rollback_http_recovery RENAME TO project_rollback_http_recovery_rows;
		CREATE VIEW project_rollback_http_recovery AS
			SELECT * FROM project_rollback_http_recovery_rows WHERE pause_project_rollback_replay();
	`); err != nil {
		t.Fatal(err)
	}
	cfg := pool.Config()
	cfg.ConnConfig.RuntimeParams["application_name"] = "pgtest_project_rollback_replay"
	replayPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer replayPool.Close()
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock(93473893, hashtext(current_schema()))`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- state.NewPgStore(replayPool).ReplayProjectRollbackRoutes(ctx) }()
	for deadline := time.Now().Add(5 * time.Second); ; {
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			    WHERE datname = current_database() AND $1 = ANY(pg_blocking_pids(pid)))`,
			int32(gate.Conn().PgConn().PID())).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("route replay did not wait before locking the receipt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO compute_nodes(name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb,role,lifecycle,gateway_target_url)
		VALUES('rollback-edge-d','unix:///run/test.sock',2,1024,10,512,'compute-only','active','tcp://127.0.0.1:8080')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.NewPgStore(pool).AcknowledgeProjectRollbackHTTPRoute(ctx, route, "rollback-edge-a"); err != nil {
		t.Fatal(err)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		DROP VIEW project_rollback_http_recovery;
		ALTER TABLE project_rollback_http_recovery_rows RENAME TO project_rollback_http_recovery;
		DROP FUNCTION pause_project_rollback_replay();
	`); err != nil {
		t.Fatal(err)
	}
}

func TestPgProjectRollbackPauseFencesCutover(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	for _, legacy := range []bool{false, true} {
		name := "concurrent cutover and pause"
		if legacy {
			name = "legacy checked child"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			acct, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "rollback-pause"})
			if err != nil {
				t.Fatal(err)
			}
			makeStep := func(name string) api.ProjectRollbackStep {
				t.Helper()
				suffix := uuid.NewString()[:8]
				app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "pause-" + name + "-" + suffix, WorkloadName: name,
					Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
				if err != nil {
					t.Fatal(err)
				}
				deploy := func() state.Deployment {
					t.Helper()
					key := uuid.NewString()
					d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
						ImageDigest: "sha256:" + key, TrafficPercent: 100})
					if err != nil {
						t.Fatal(err)
					}
					if err := s.SetDeploymentRootfs(ctx, d.ID, "/layers/"+key, "layers/"+key, 4096); err != nil {
						t.Fatal(err)
					}
					if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
						t.Fatal(err)
					}
					d, err = s.DeploymentByID(ctx, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					return d
				}
				target, current := deploy(), deploy()
				return api.ProjectRollbackStep{AppID: app.ID, Scope: api.DefaultEnvScope, CurrentID: current.ID, TargetID: target.ID,
					CurrentServedAt: current.RolloutCompletedAt, AttemptID: uuid.NewString(), Status: "planned"}
			}
			web, apiStep := makeStep("web"), makeStep("api")
			op := state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
				ID: uuid.NewString(), ProjectID: project.ID, Status: "running", Steps: []api.ProjectRollbackStep{web, apiStep},
			}}
			if !legacy {
				deadline := time.Now().Add(5 * time.Minute)
				op.Phase, op.PreparationDeadline = api.ProjectRollbackPreparing, &deadline
			}
			if err := s.CreateProjectRollback(ctx, acct.ID, op); err != nil {
				t.Fatal(err)
			}
			claim, err := s.ClaimProjectRollback(ctx, time.Minute)
			if err != nil || claim.ID != op.ID {
				t.Fatalf("claim = %+v, %v", claim, err)
			}
			prepare := func(step api.ProjectRollbackStep) api.RollbackOperation {
				t.Helper()
				child, err := s.CreateCheckedRollback(state.WithProjectRollbackStep(ctx, claim, step),
					acct.ID, step.AppID, step.TargetID, step.CurrentID, "")
				if err != nil {
					t.Fatal(err)
				}
				if err := s.MarkDeploymentLive(ctx, step.TargetID); err != nil {
					t.Fatal(err)
				}
				ready, err := s.GetCheckedRollback(ctx, acct.ID, step.AppID, child.ID)
				if err != nil || ready.Status != "ready" {
					t.Fatalf("child not ready: %+v, %v", ready, err)
				}
				if !legacy {
					if err := s.UpdateCheckedRollback(ctx, ready, "ready", api.CodeProjectRollbackPrepared, nil); err != nil {
						t.Fatal(err)
					}
				}
				return ready
			}
			caller := prepare(web)
			var downstream api.RollbackOperation
			if !legacy {
				downstream = prepare(apiStep)
				claim.Phase = api.ProjectRollbackCutover
				if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
					t.Fatal(err)
				}
			} else if caller.ProjectRollbackID != "" {
				t.Fatalf("expected an unlinked legacy child: %+v", caller)
			}

			var cutoverErr error
			if legacy {
				if _, changed, err := s.PauseProjectRollback(ctx, acct.ID, project.ID, op.ID); err != nil || !changed {
					t.Fatalf("pause = %v, %v", changed, err)
				}
				_, cutoverErr = s.CommitCheckedRollback(ctx, caller)
				if !errors.Is(cutoverErr, state.ErrCheckedRollbackRequired) {
					t.Fatalf("legacy cutover after pause = %v", cutoverErr)
				}
			} else {
				start := make(chan struct{})
				finished := make(chan error, 1)
				go func() {
					<-start
					_, err := s.CommitCheckedRollback(ctx, caller)
					finished <- err
				}()
				close(start)
				_, requested, pauseErr := s.PauseProjectRollback(ctx, acct.ID, project.ID, op.ID)
				cutoverErr = <-finished
				if pauseErr != nil || !requested {
					t.Fatalf("pause = %v, %v", requested, pauseErr)
				}
				if cutoverErr != nil && !errors.Is(cutoverErr, state.ErrProjectRollbackPaused) {
					t.Fatalf("unexpected cutover result: %v", cutoverErr)
				}
				if _, err := s.CommitCheckedRollback(ctx, downstream); !errors.Is(err, state.ErrProjectRollbackPaused) {
					t.Fatalf("downstream cutover after pause = %v", err)
				}
			}
			webServing := web.TargetID
			if cutoverErr != nil {
				webServing = web.CurrentID
			}
			for _, id := range []string{webServing, apiStep.CurrentID} {
				d, err := s.DeploymentByID(ctx, id)
				if err != nil || d.TrafficPercent != 100 {
					t.Fatalf("serving deployment %s = %+v, %v", id, d, err)
				}
			}
			tag, err := pool.Exec(ctx, `UPDATE project_rollback_operations
				SET receipt = (receipt - 'pause_requested_at') || jsonb_build_object('phase', 'cutover')
				WHERE id = $1`, op.ID)
			if err != nil || tag.RowsAffected() != 0 {
				t.Fatalf("old controller reopened the pause: %v, %v", tag, err)
			}

			current, err := s.ClaimProjectRollback(ctx, time.Minute)
			if err != nil || current.ID != op.ID || current.PauseRequestedAt == nil || current.Phase != api.ProjectRollbackReleasing {
				t.Fatalf("cleanup claim = %+v, %v", current, err)
			}
			current.Status = "blocked"
			if err := s.SaveProjectRollback(ctx, current, 0); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPgProjectRollbackHistoryOrderingLimitAndScope(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	makeProject := func(slug string) (state.Account, state.Project) {
		t.Helper()
		acct, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		return acct, project
	}
	acct, project := makeProject("rollback-history")
	foreign, other := makeProject("other-rollback-history")
	add := func(account string, project state.Project, status string, at time.Time) string {
		t.Helper()
		id := uuid.NewString()
		if err := s.CreateProjectRollback(ctx, account, state.ProjectRollbackRecord{
			ProjectRollbackOperation: api.ProjectRollbackOperation{
				ID: id, ProjectID: project.ID, Project: project.Slug, Status: status, CreatedAt: &at,
				Steps: []api.ProjectRollbackStep{{Status: "rolled_back"}, {Status: "skipped"}},
			},
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	start := time.Now().UTC().Add(-time.Hour)
	active := add(acct.ID, project, "running", start)
	if _, err := s.ActiveProjectRollbackID(ctx, foreign.ID, project.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account saw an active rollback: %v", err)
	}
	add(foreign.ID, other, "running", start)
	var last string
	for i := 1; i <= api.ProjectRollbackHistoryLimit+2; i++ {
		last = add(acct.ID, project, "complete", start.Add(time.Duration(i)*time.Second))
	}
	items, err := s.ListProjectRollbacks(ctx, acct.ID, project.ID)
	if err != nil || len(items) != api.ProjectRollbackHistoryLimit || items[0].ID != active ||
		items[1].ID != last || items[1].CutoversCompleted != 1 || items[1].CutoversTotal != 1 {
		t.Fatalf("history = %+v, err = %v", items, err)
	}
	if leaked, err := s.ListProjectRollbacks(ctx, foreign.ID, project.ID); err != nil || len(leaked) != 0 {
		t.Fatalf("foreign account saw project rollbacks: %+v %v", leaked, err)
	}
	if id, err := s.ActiveProjectRollbackID(ctx, acct.ID, project.ID); err != nil || id != active {
		t.Fatalf("active rollback = %q, %v", id, err)
	}
}

func TestPgProjectRollbackStartRequestIsAtomic(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "rollback-start-request"})
	if err != nil {
		t.Fatal(err)
	}
	req := api.ProjectRollbackRequest{
		RequestID: uuid.NewString(), AcknowledgeReleases: true, ExpectedPlanHash: strings.Repeat("a", 64),
	}
	now := time.Now().UTC()
	candidate := func(project state.Project) state.ProjectRollbackRecord {
		return state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
			ID: uuid.NewString(), Project: project.Slug, ProjectID: project.ID, Status: "running",
			AcknowledgeReleases: req.AcknowledgeReleases, PlanHash: req.ExpectedPlanHash, CreatedAt: &now,
		}}
	}
	type result struct {
		op      state.ProjectRollbackRecord
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			op, created, err := s.CreateProjectRollbackStart(ctx, acct.ID, candidate(project), req)
			results <- result{op: op, created: created, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.created == second.created ||
		first.op.ID == "" || first.op.ID != second.op.ID ||
		first.op.RequestID != req.RequestID || second.op.RequestID != req.RequestID {
		t.Fatalf("concurrent attempts produced different receipts: %+v %+v", first, second)
	}

	claim, err := s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil || claim.ID != first.op.ID {
		t.Fatalf("claim = %+v, %v", claim, err)
	}
	claim.Status = "complete"
	if err := s.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	// A mixed-version worker can rewrite a receipt without these fields.
	if _, err := pool.Exec(ctx, `UPDATE project_rollback_operations SET receipt = receipt - 'request_id' - 'plan_hash' WHERE id = $1`, claim.ID); err != nil {
		t.Fatal(err)
	}
	s = state.NewPgStore(pool)
	replay, created, err := s.CreateProjectRollbackStart(ctx, acct.ID, candidate(project), req)
	if err != nil || created || replay.ID != claim.ID || replay.Status != "complete" ||
		replay.RequestID != req.RequestID || replay.PlanHash != req.ExpectedPlanHash {
		t.Fatalf("completed receipt did not replay: %+v, %v, %v", replay, created, err)
	}
	changed := req
	changed.Verify = true
	if _, _, err := s.CreateProjectRollbackStart(ctx, acct.ID, candidate(project), changed); !errors.Is(err, state.ErrProjectRollbackRequestReused) {
		t.Fatalf("changed options accepted under same request ID: %v", err)
	}
	items, err := s.ListProjectRollbacks(ctx, acct.ID, project.ID)
	if err != nil || len(items) != 1 || items[0].ID != claim.ID {
		t.Fatalf("retries created an extra rollback: %+v, %v", items, err)
	}
	other, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "other-rollback-start-request"})
	if err != nil {
		t.Fatal(err)
	}
	if op, created, err := s.CreateProjectRollbackStart(ctx, acct.ID, candidate(other), req); err != nil || !created || op.ProjectID != other.ID {
		t.Fatalf("request ID was not scoped to the project: %+v, %v, %v", op, created, err)
	}
}

func TestPgProjectRollbackAutomaticUndoTransfersRunningSlotOnce(t *testing.T) {
	t.Run("probe", func(t *testing.T) { testPgProjectRollbackAutomaticUndoTransfersRunningSlotOnce(t, false) })
	t.Run("live traffic", func(t *testing.T) { testPgProjectRollbackAutomaticUndoTransfersRunningSlotOnce(t, true) })
}

func testPgProjectRollbackAutomaticUndoTransfersRunningSlotOnce(t *testing.T, liveTraffic bool) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "rollback-auto-undo"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source := state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
		ID: uuid.NewString(), Project: project.Slug, ProjectID: project.ID, Status: "running",
		Phase: api.ProjectRollbackReleasing, Verify: true, UndoOnHealthFailure: true,
	}}
	for range 2 {
		source.Steps = append(source.Steps, api.ProjectRollbackStep{
			AppID: uuid.NewString(), CurrentID: uuid.NewString(), TargetID: uuid.NewString(), Status: "rolled_back",
		})
	}
	triggerStep := &source.Steps[1]
	source.AutoUndo = &api.ProjectRollbackAutoUndo{Status: api.ProjectRollbackAutoUndoReady, TriggerAppID: triggerStep.AppID}
	if liveTraffic {
		source.UndoOnHealthFailure, source.VerifyLiveTraffic, source.UndoOnLiveTrafficFailure = false, true, true
		generation, budget := int64(15), int64(500)
		end := now.Truncate(time.Minute)
		start := end.Add(-2 * time.Minute)
		triggerStep.LiveTraffic = &api.ProjectRollbackLiveTraffic{
			Status: "failed", Reason: "sustained_budget_violation", RouteGeneration: &generation,
		}
		source.LiveTrafficPolicies = map[string]state.ProjectRollbackLiveTrafficPolicy{
			triggerStep.AppID: {Revision: 1, Routes: []api.RouteMonitorRoute{{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget}}},
		}
		source.AutoUndo.Trigger, source.AutoUndo.RouteGeneration = api.ProjectRollbackAutoUndoLiveTrafficTrigger, &generation
		source.AutoUndo.WindowStart, source.AutoUndo.WindowEnd = &start, &end
	}
	if err := s.CreateProjectRollback(ctx, acct.ID, source); err != nil {
		t.Fatal(err)
	}
	if liveTraffic {
		for _, update := range []string{
			`UPDATE project_rollback_operations SET receipt = receipt - 'undo_on_live_traffic_failure' WHERE id = $1`,
			`UPDATE project_rollback_operations SET receipt = receipt #- '{auto_undo,trigger}' WHERE id = $1`,
		} {
			if result, err := pool.Exec(ctx, update, source.ID); err != nil || result.RowsAffected() != 0 {
				t.Fatalf("older worker could rewrite automatic undo policy or evidence: %v, %v", result, err)
			}
		}
	}
	inverse := func() state.ProjectRollbackRecord {
		undo := state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
			ID: uuid.NewString(), Project: project.Slug, ProjectID: project.ID, Status: "running",
			Phase: api.ProjectRollbackPreparing, UndoOf: source.ID,
		}}
		for i := len(source.Steps) - 1; i >= 0; i-- {
			step := source.Steps[i]
			undo.Steps = append(undo.Steps, api.ProjectRollbackStep{
				AppID: step.AppID, CurrentID: step.TargetID, TargetID: step.CurrentID,
				CurrentServedAt: &now, AttemptID: uuid.NewString(), Status: "planned",
			})
		}
		return undo
	}
	expired, err := s.ClaimProjectRollback(ctx, -time.Second)
	if err != nil || expired.ID != source.ID {
		t.Fatalf("expired claim: %+v %v", expired, err)
	}
	if _, err := s.CreateProjectRollbackUndo(ctx, acct.ID, expired, inverse()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker committed an undo: %v", err)
	}
	s = state.NewPgStore(pool)
	claimed, err := s.ClaimProjectRollback(ctx, time.Minute)
	if err != nil || claimed.ID != source.ID {
		t.Fatalf("replacement claim: %+v %v", claimed, err)
	}
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			op, err := s.CreateProjectRollbackUndo(ctx, acct.ID, claimed, inverse())
			results <- result{id: op.ID, err: err}
		}()
	}
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.id == "" || a.id != b.id {
		t.Fatalf("workers produced different undo receipts: %+v %+v", a, b)
	}
	parent, err := s.GetProjectRollback(ctx, acct.ID, project.ID, source.ID)
	if err != nil || parent.Status != "blocked" || parent.UndoOperationID != a.id ||
		parent.AutoUndo == nil || parent.AutoUndo.Status != api.ProjectRollbackAutoUndoStarted {
		t.Fatalf("automatic undo link: %+v %v", parent, err)
	}
	if liveTraffic && !parent.AutoUndo.MatchesLiveTrafficFailure(parent.Steps[1]) {
		t.Fatalf("linked undo lost the production evidence: %+v", parent.AutoUndo)
	}
	active, err := s.ActiveProjectRollbackID(ctx, acct.ID, project.ID)
	if err != nil || active != a.id {
		t.Fatalf("active operation = %q, %v", active, err)
	}
}
