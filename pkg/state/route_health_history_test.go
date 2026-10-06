package state_test

// adr: 456

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestRouteHealthHistoryMemImmutableScopedAndReadOnly(t *testing.T) {
	s := state.NewMemStore()
	a, app, _, d := healthFixture(t, s)
	zero := int64(0)
	g, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 300}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, "")
	if err != nil || page.Entries == nil || len(page.Entries) != 0 {
		t.Fatal("reading created history", err)
	}
	var decision api.RouteHealthDecision
	params := state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision}
	if _, _, err := s.AdvanceCanary(t.Context(), d.ID, params); err == nil || decision.HistoryID == "" {
		t.Fatal("missing held decision", err)
	}
	entry, err := s.GetRouteHealthHistoryEntry(t.Context(), a.ID, app.ID, d.ID, decision.HistoryID)
	if err != nil || !routeHealthDecisionsEqual(entry.Decision, decision) || entry.Policy != routehealth.HistoryPolicy() || entry.Report.Status != "unknown" || entry.Source != "manual" || entry.Report.ObservationAnchor == nil || entry.Report.Routes[0].MaxP95MS != 300 {
		t.Fatalf("lost evidence: %+v %v", entry, err)
	}
	original, _ := json.Marshal(entry)
	entry.Report.Routes[0].Path = "/mutated"
	*entry.Report.ObservationAnchor = time.Time{}
	entry, err = s.GetRouteHealthHistoryEntry(t.Context(), a.ID, app.ID, d.ID, decision.HistoryID)
	body, _ := json.Marshal(entry)
	if err != nil || string(body) != string(original) {
		t.Fatal("caller mutated saved evidence", err)
	}
	for _, scope := range [][3]string{{uuid.NewString(), app.ID, d.ID}, {a.ID, uuid.NewString(), d.ID}, {a.ID, app.ID, uuid.NewString()}} {
		if _, err := s.GetRouteHealthHistoryEntry(t.Context(), scope[0], scope[1], scope[2], entry.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign evidence exposed", err)
		}
	}
	if _, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("missing cursor accepted", err)
	}
	if _, _, err := s.AdvanceCanary(t.Context(), d.ID, state.CanaryAdvanceParams{ExpectedStep: 1, TrafficPercent: 10}); !errors.Is(err, state.ErrCanaryStepConflict) {
		t.Fatal(err)
	}
	if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &g.Revision, Routes: g.Routes}); err != nil {
		t.Fatal(err)
	}
	params.Audit = state.DeploymentAudit{Data: json.RawMessage(`{`)}
	if _, _, err := s.AdvanceCanary(t.Context(), d.ID, params); err == nil {
		t.Fatal("invalid audit accepted")
	}
	page, _ = s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, "")
	if len(page.Entries) != 1 {
		t.Fatal("failed transition left an allowed snapshot")
	}
	params.Audit = state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "history-test"}
	if _, _, err := s.AdvanceCanary(t.Context(), d.ID, params); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountPlan(t.Context(), a.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecoverRollout(t.Context(), app.ID, "abort", ""); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 1, "")
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Decision.Status != "report_only" || page.NextCursor == "" {
		t.Fatalf("history unavailable after downgrade/abort: %+v %v", page, err)
	}
	older, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 1, page.NextCursor)
	if err != nil || len(older.Entries) != 1 || older.Entries[0].ID != entry.ID || older.NextCursor != "" {
		t.Fatal("cursor skipped original evidence", err)
	}
}

func TestRouteHealthHistoryPostgresAtomicityAndRecovery(t *testing.T) {
	for _, scenario := range []string{"recovery", "report", "worker", "audit_failure", "traffic_failure", "lease_expired", "write_failure", "default"} {
		t.Run(scenario, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			s := state.NewPgStore(pool)
			a, app, stable, d := healthFixture(t, s)
			if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_step_started_at = clock_timestamp() - interval '1 hour', rollout_state = 'rolling_out' WHERE id = $1", d.ID); err != nil {
				t.Fatal(err)
			}
			if scenario != "default" {
				mode := "enforce"
				if scenario == "report" || scenario == "audit_failure" {
					mode = "report"
				}
				zero := int64(0)
				if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: mode, ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 300}}}); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at = clock_timestamp() - interval '1 hour' WHERE app_id = $1", app.ID); err != nil {
					t.Fatal(err)
				}
			}
			windows := routehealth.Windows(time.Now().UTC())
			seed := func(latency int32) {
				t.Helper()
				for _, window := range windows {
					for _, dep := range []state.Deployment{stable, d} {
						ms := int32(100)
						if dep.ID == d.ID {
							ms = latency
						}
						if err := sqlc.New().InsertRequestTelemetry(t.Context(), pool, sqlc.InsertRequestTelemetryParams{AccountID: pgtype.UUID{Bytes: uuid.MustParse(a.ID), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(dep.ID), Valid: true}, Route: "POST /checkout", Method: "POST", Status: 200, LatencyMs: ms, ReceivedAt: state.NewPgtypeTime(window.Start.Add(time.Second)), Count: 100, UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			latency := int32(500)
			if scenario == "traffic_failure" || scenario == "lease_expired" {
				latency = 120
			}
			seed(latency)
			if _, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			page, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, "")
			if err != nil || len(page.Entries) != 0 {
				t.Fatal("report read wrote history", err)
			}
			var decision api.RouteHealthDecision
			params := state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "history-test"}}
			if scenario == "worker" {
				if err := s.StampSafeReleaseWorkerLease(t.Context(), time.Minute); err != nil {
					t.Fatal(err)
				}
				params.RequireSafeReleaseLease = true
			}
			if scenario == "traffic_failure" {
				if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_history_traffic() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced traffic failure'; END; $$;
				CREATE TRIGGER reject_history_traffic BEFORE UPDATE OF traffic_percent ON deployments FOR EACH ROW EXECUTE FUNCTION reject_history_traffic()`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "lease_expired" {
				if err := s.StampSafeReleaseWorkerLease(t.Context(), time.Second); err != nil {
					t.Fatal(err)
				}
				params.RequireSafeReleaseLease = true
				if _, err := pool.Exec(t.Context(), `CREATE FUNCTION delay_health_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(1.2); RETURN NEW; END; $$;
				CREATE TRIGGER delay_health_history BEFORE INSERT ON route_health_history FOR EACH ROW EXECUTE FUNCTION delay_health_history()`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "audit_failure" {
				params.Audit.Data = json.RawMessage(`{`)
			}
			if scenario == "write_failure" {
				if _, err := pool.Exec(t.Context(), "DROP TABLE route_health_history"); err != nil {
					t.Fatal(err)
				}
			}
			_, audit, err := s.AdvanceCanary(t.Context(), d.ID, params)
			if scenario == "write_failure" {
				current, _ := s.DeploymentByID(t.Context(), d.ID)
				if err == nil || audit != 0 || current.CanaryStep != 0 || current.TrafficPercent != 1 {
					t.Fatal("history write failed open", err)
				}
				return
			}
			page, pageErr := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, "")
			if pageErr != nil {
				t.Fatal(pageErr)
			}
			if scenario == "audit_failure" || scenario == "traffic_failure" || scenario == "lease_expired" || scenario == "default" {
				if len(page.Entries) != 0 {
					t.Fatal("failed or disabled evaluation retained a snapshot")
				}
				if scenario != "default" && err == nil || scenario == "default" && (err != nil || decision.HistoryID != "") {
					t.Fatal("unexpected advance", err)
				}
				if scenario != "default" {
					current, _ := s.DeploymentByID(t.Context(), d.ID)
					audits, _ := s.ListDeploymentAudit(t.Context(), d.ID, 10)
					if current.CanaryStep != 0 || current.TrafficPercent != 1 || len(audits) != 0 {
						t.Fatal("failed transaction retained traffic or audit")
					}
				}
				return
			}
			if len(page.Entries) != 1 || !routeHealthDecisionsEqual(page.Entries[0].Decision, decision) || page.Entries[0].Policy != routehealth.HistoryPolicy() || *page.Entries[0].Report.Routes[0].Windows[0].Candidate.P95LatencyMS != 500 {
				t.Fatalf("snapshot: %+v %v", page, err)
			}
			first := page.Entries[0]
			if scenario == "report" {
				if err != nil || audit == 0 || decision.Status != "report_only" {
					t.Fatal("report mode blocked", err)
				}
				return
			}
			var blocked *state.RouteHealthBlockedError
			if !errors.As(err, &blocked) || audit != 0 || first.Decision.Status != "blocked" {
				t.Fatal("regression advanced", err)
			}
			if scenario == "worker" && first.Source != "worker" {
				t.Fatal("worker origin lost")
			}
			// Concurrent identical retries serialize on the parent lock and retain one immutable snapshot.
			var wg sync.WaitGroup
			errs := make(chan error, 4)
			for range 4 {
				wg.Go(func() {
					retryParams := params
					var retryDecision api.RouteHealthDecision
					retryParams.RouteHealthDecision = &retryDecision
					_, _, err := s.AdvanceCanary(t.Context(), d.ID, retryParams)
					if !routeHealthDecisionsEqual(retryDecision, first.Decision) {
						err = fmt.Errorf("retry decision differs from saved evidence: %+v", retryDecision)
					}
					errs <- err
				})
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if !errors.As(err, &blocked) {
					t.Fatal(err)
				}
			}
			page, _ = s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, "")
			if len(page.Entries) != 1 || !reflect.DeepEqual(page.Entries[0], first) {
				t.Fatal("retry replaced immutable evidence")
			}
			if _, err := s.GetRouteHealthHistoryEntry(t.Context(), uuid.NewString(), app.ID, d.ID, first.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("tenant leak", err)
			}
			if scenario == "worker" {
				return
			}
			// Replace observations in the same windows without changing stage or configuration.
			if _, err := pool.Exec(t.Context(), "DELETE FROM request_telemetry WHERE app_id = $1", app.ID); err != nil {
				t.Fatal(err)
			}
			seed(120)
			_, audit, err = s.AdvanceCanary(t.Context(), d.ID, params)
			if err != nil || audit == 0 || decision.Status != "allowed" || decision.HistoryID == first.ID {
				t.Fatal("healthy recovery did not commit", err)
			}
			page, err = s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 1, "")
			if err != nil || len(page.Entries) != 1 || page.Entries[0].Report.Status != "healthy" || page.NextCursor != decision.HistoryID {
				t.Fatal("recovery history lost", err)
			}
			old, err := s.GetRouteHealthHistoryEntry(t.Context(), a.ID, app.ID, d.ID, first.ID)
			if err != nil || !reflect.DeepEqual(old, first) {
				t.Fatal("current telemetry changed saved evidence", err)
			}
			audits, _ := s.ListDeploymentAudit(t.Context(), d.ID, 10)
			var fields struct {
				RouteHealth api.RouteHealthDecision `json:"route_health"`
			}
			if len(audits) != 1 || json.Unmarshal(audits[0].Data, &fields) != nil || !routeHealthDecisionsEqual(fields.RouteHealth, decision) {
				t.Fatal("traffic audit not correlated")
			}
			if err := s.UpdateAccountPlan(t.Context(), a.ID, api.PlanFree); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetRouteHealthHistoryEntry(t.Context(), a.ID, app.ID, d.ID, first.ID); err != nil {
				t.Fatal("downgrade hid evidence", err)
			}
		})
	}
}

func TestRouteHealthHistoryPostgresRetentionAndCursor(t *testing.T) {
	for _, padding := range []int{0, 60 << 10} {
		t.Run(fmt.Sprint(padding), func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			s := state.NewPgStore(pool)
			a, app, _, d := healthFixture(t, s)
			q := sqlc.New()
			first := ""
			bytes := 0
			at := time.Now().UTC().Add(-time.Hour)
			for i := range api.RouteHealthHistoryMaxEntries + 10 {
				id := uuid.NewString()
				checked := at.Add(time.Duration(i) * time.Second)
				r := api.RouteHealthReport{AppID: app.ID, DeploymentID: d.ID, CheckedAt: checked, CandidateCommitSHA: strings.Repeat("x", padding)}
				e := api.RouteHealthHistoryEntry{Version: 1, ID: id, CheckedAt: checked, Source: "manual", Policy: routehealth.HistoryPolicy(), Report: r, Decision: api.RouteHealthDecision{DeploymentID: d.ID, CheckedAt: checked, HistoryID: id}}
				body, err := json.Marshal(e)
				if err != nil || len(body) > api.RouteHealthHistoryEntryMaxBytes {
					t.Fatal("bad retention fixture", err)
				}
				bytes = len(body)
				if _, err := q.InsertRouteHealthHistory(t.Context(), pool, sqlc.InsertRouteHealthHistoryParams{ID: id, AccountID: a.ID, AppID: app.ID, DeploymentID: d.ID, DecisionKey: fmt.Sprintf("%064x", i), CheckedAt: state.NewPgtypeTime(checked), EncodedBytes: int32(bytes), Entry: body}); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					first = id
				}
			}
			if err := q.PruneRouteHealthHistory(t.Context(), pool, sqlc.PruneRouteHealthHistoryParams{DeploymentID: d.ID, MaxEntries: api.RouteHealthHistoryMaxEntries, MaxBytes: api.RouteHealthHistoryMaxBytes}); err != nil {
				t.Fatal(err)
			}
			want := min(api.RouteHealthHistoryMaxEntries, api.RouteHealthHistoryMaxBytes/bytes)
			seen := map[string]bool{}
			cursor := ""
			for {
				page, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 10, cursor)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range page.Entries {
					if seen[entry.ID] {
						t.Fatal("cursor repeated entry")
					}
					seen[entry.ID] = true
				}
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
			if len(seen) != want {
				t.Fatalf("retained %d want %d", len(seen), want)
			}
			if _, err := s.GetRouteHealthHistoryEntry(t.Context(), a.ID, app.ID, d.ID, first); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("pruned evidence readable", err)
			}
			if _, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, first); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("pruned cursor silently skipped", err)
			}
		})
	}
}

// PostgreSQL timestamps and JSON timestamps can describe the same instant with
// different location metadata. Keep comparing every decision field and compare
// CheckedAt by its instant rather than time.Time's internal representation.
func routeHealthDecisionsEqual(a, b api.RouteHealthDecision) bool {
	if !a.CheckedAt.Equal(b.CheckedAt) {
		return false
	}
	a.CheckedAt, b.CheckedAt = time.Time{}, time.Time{}
	return a == b
}
