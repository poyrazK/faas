package state_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgRouteLatencyInvestigationSuccessfulResponsesBoundedScopesAndWakeJoin(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, stable, candidate := healthFixture(t, s)
	_, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", CheckLatency: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at=clock_timestamp()-interval '1 hour' WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_step_started_at=clock_timestamp()-interval '1 hour',rollout_state='rolling_out' WHERE id=$1", candidate.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "latency", "Private tenant", 30)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:latency-other", TrafficPercent: 1, CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	windows := routehealth.Windows(time.Now())
	q := &sqlc.Queries{}
	insert := func(dep, tenantID, method, path, wakeID, instanceID string, at time.Time, count, latency, guest, spanMS int32) {
		t.Helper()
		trace := strings.ReplaceAll(uuid.NewString(), "-", "")
		p := sqlc.InsertRequestTelemetryParams{AccountID: mustPgUUID(t, a.ID), AppID: mustPgUUID(t, app.ID), DeploymentID: mustPgUUID(t, dep), Route: method + " " + path, Method: method, Status: 200, Count: count, LatencyMs: latency, ReceivedAt: state.NewPgtypeTime(at), TraceID: pgtype.Text{String: trace, Valid: true}, UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "node22", GuestOutcome: "ok", GuestDurationMs: guest, ColdBoot: wakeID != "", WakeID: pgtype.Text{String: wakeID, Valid: wakeID != ""}, InstanceID: pgtype.Text{String: instanceID, Valid: instanceID != ""}, FlagEvidenceJson: "[]"}
		if tenantID != "" {
			p.PlatformTenantID = mustPgUUID(t, tenantID)
		}
		if err := q.InsertRequestTelemetry(t.Context(), pool, p); err != nil {
			t.Fatal(err)
		}
		if spanMS <= 0 {
			return
		}
		span := debugger.StoredSpan{TraceID: trace, SpanID: "db", Name: "secret database host", DBStatement: "SELECT password", StartTimeUnixNano: uint64(at.UnixNano()), EndTimeUnixNano: uint64(at.Add(time.Duration(spanMS) * time.Millisecond).UnixNano()), DurationNanos: uint64(time.Duration(spanMS) * time.Millisecond), Attributes: map[string]string{"gregale.dependency.type": "managed_binding", "gregale.dependency.kind": "postgres"}}
		body, _ := json.Marshal([]debugger.StoredSpan{span})
		if _, err := q.UpdateSpansSummary(t.Context(), pool, sqlc.UpdateSpansSummaryParams{TraceID: pgtype.Text{String: trace, Valid: true}, Column2: body, Column3: mustPgUUID(t, a.ID)}); err != nil {
			t.Fatal(err)
		}
	}
	emitWake := func(wake, instance, appID string, at time.Time, ms int) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"wake_id": wake, "app_id": appID, "instance_id": instance})
		if err := s.AppendEventAt(t.Context(), "schedd", "wake.boot_started", nil, body, at.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEventAt(t.Context(), "schedd", "wake.boot_completed", nil, body, at.Add(-time.Second).Add(time.Duration(ms)*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	for _, w := range windows {
		insert(other.ID, tenant.ID, "POST", "/checkout", "", "", w.Start, 999, 9999, 9999, 9999)
		wake, instance := uuid.NewString(), uuid.NewString()
		emitWake(wake, instance, app.ID, w.Start, 250)
		// Earlier events with the same wake but different app/instance must not
		// turn the selected deployment's boot into a long unrelated wake.
		emitWake(wake, instance, uuid.NewString(), w.Start.Add(-time.Minute), 7000)
		emitWake(wake, uuid.NewString(), app.ID, w.Start.Add(-time.Minute), 8000)
		insert(candidate.ID, tenant.ID, "POST", "/checkout", wake, instance, w.Start, 4, 9999, 9000, 9000)
		for i := 1; i <= 32; i++ {
			spanMS := int32(500)
			if i == 32 {
				spanMS = 0
			}
			insert(candidate.ID, tenant.ID, "POST", "/checkout", wake, instance, w.Start.Add(time.Duration(i)*time.Second), 4, 600, 200, spanMS)
		}
		stableWake, stableInstance := uuid.NewString(), uuid.NewString()
		emitWake(stableWake, stableInstance, app.ID, w.Start, 50)
		insert(stable.ID, tenant.ID, "POST", "/checkout", stableWake, stableInstance, w.Start, 132, 100, 30, 40)
		for _, dep := range []string{candidate.ID, stable.ID} {
			insert(dep, "", "POST", "/checkout", "", "", w.Start, 999, 9999, 9999, 9999)
			insert(dep, tenant.ID, "GET", "/checkout", "", "", w.Start, 999, 9999, 9999, 9999)
			insert(dep, tenant.ID, "POST", "/checkout/123", "", "", w.Start, 999, 9999, 9999, 9999)
			insert(dep, tenant.ID, "POST", "/checkout", "", "", windows[1].End, 999, 9999, 9999, 9999)
		}
	}
	opts := api.RouteHealthInvestigationOptions{Method: "POST", Path: "/checkout", Signal: "latency", CustomerID: tenant.ID}
	r, err := s.GetRouteHealthInvestigation(t.Context(), a.ID, app.ID, candidate.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "regressed" || r.Finding.ErrorStatus != "healthy" || r.Selection.Signal != "latency" || routehealth.ValidateInvestigation(r, opts, app.Slug) != nil {
		t.Fatalf("latency scope/verdict: %+v", r)
	}
	for _, w := range r.Windows {
		d := w.Diagnostics
		if w.Candidate.MatchingRequests != 132 || w.Candidate.ObservedRows != 33 || w.Candidate.Examples[0].LatencyMS != 9999 || w.Candidate.Examples[0].Status != 200 || d.Candidate.SampledRows != 32 || d.Candidate.SampledRequests != 128 || !d.Candidate.SamplesTruncated || d.Candidate.SpanRows != 31 || d.Candidate.MissingSpanRows != 1 || *d.Candidate.GuestP95MS != 200 || d.Candidate.WakeSamples != 1 || *d.Candidate.WakeBootP95MS != 250 || *d.Stable.WakeBootP95MS != 50 {
			t.Fatalf("independent full/sample inventories: %+v", w)
		}
		dep := d.Dependencies[0]
		if dep.Type != "managed_binding" || dep.Kind != "postgres" || dep.Candidate.RepresentedCalls != 124 || *dep.P95DeltaMS != 460 || *dep.ExclusiveP95DeltaMS != 460 {
			t.Fatalf("dependency comparison: %+v", dep)
		}
	}
	body, _ := json.Marshal(r)
	if strings.Contains(string(body), "secret database host") || strings.Contains(string(body), "SELECT password") || strings.Contains(string(body), "Private tenant") {
		t.Fatal("span or identity details escaped")
	}
	history, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, candidate.ID, 5, "")
	if err != nil || len(history.Entries) != 0 {
		t.Fatal("investigation persisted decisions")
	}
	dep, err := s.DeploymentByID(t.Context(), candidate.ID)
	if err != nil || dep.TrafficPercent != 1 || dep.CanaryStep != 0 {
		t.Fatal("investigation changed traffic")
	}
}
