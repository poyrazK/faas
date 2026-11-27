package runtime

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRehearsalPostgresJournalPinsFreshRunAndFencesRetirement(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newPostgresCatalogFixture(t, backend)
			expires := time.Now().Add(time.Hour)
			app, e := f.base.CreateApp(t.Context(), state.App{ID: uuid.NewString(), AccountID: f.account.ID, Slug: "pg-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
			if e != nil {
				t.Fatal(e)
			}
			plan := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
			body, _, e := api.NormalizeScenarioSimulationPlan(plan, []string{"primary"})
			if e != nil {
				t.Fatal(e)
			}
			if e = f.base.RegisterScenarioTestMembers(t.Context(), f.account.ID, postgresJournalRun, []state.ScenarioTestMember{{AppID: app.ID, Workload: "primary", WebhookMode: "capture", NetworkMode: "contained", SimulationJSON: string(body)}}); e != nil {
				t.Fatal(e)
			}
			id := uuid.NewString()
			r := rehearsalpg.Resource{ID: id, AccountID: f.account.ID, RunID: postgresJournalRun, AppID: app.ID, OwnerID: uuid.NewString(), DatabaseName: "grpg_" + strings.ReplaceAll(id, "-", ""), RoleName: "grpg_" + strings.ReplaceAll(id, "-", ""), Status: "provisioning", BackendSHA256: strings.Repeat("f", 64), ExpiresAt: time.Now().Add(time.Minute), Spec: rehearsalpg.Spec{Version: 1, FixtureSHA256: strings.Repeat("a", 64), Tables: []rehearsalpg.Table{{Schema: "public", Name: "imports", Columns: []string{"item"}}}, MaxLockWaitMS: 50}}
			catalog := f.base.(rehearsalpg.Catalog)
			r, e = catalog.ReserveScenarioPostgres(t.Context(), r)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = catalog.ReserveScenarioPostgres(t.Context(), r); e == nil {
				t.Fatal("run allocated a second database")
			}
			if _, e = catalog.GetScenarioPostgres(t.Context(), uuid.NewString(), r.RunID); !errors.Is(e, state.ErrNotFound) {
				t.Fatalf("foreign account read fixture: %v", e)
			}
			if _, e = catalog.ScenarioPostgresForApp(t.Context(), app.ID); !errors.Is(e, state.ErrNotFound) {
				t.Fatal("provisioning fixture was reachable")
			}
			r.Status = "ready"
			r.Initial = []rehearsalpg.Snapshot{{Table: "public.imports", Rows: []json.RawMessage{}}}
			r, e = catalog.TransitionScenarioPostgres(t.Context(), r, "provisioning")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = catalog.ScenarioPostgresForApp(t.Context(), app.ID); e != nil {
				t.Fatal(e)
			}
			if _, e = catalog.ScenarioPostgresForApp(t.Context(), f.app.ID); !errors.Is(e, state.ErrNotFound) {
				t.Fatal("ordinary or cross-run caller reached a rehearsal database")
			}
			r.Spec.Tables[0].Columns[0] = "mutated"
			got, e := catalog.GetScenarioPostgres(t.Context(), r.AccountID, r.RunID)
			if e != nil || got.Spec.Tables[0].Columns[0] != "item" {
				t.Fatal("fixture intent aliased caller memory")
			}
			r = got
			r.Status = "sealing"
			r, e = catalog.TransitionScenarioPostgres(t.Context(), r, "ready")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = catalog.ScenarioPostgresForApp(t.Context(), app.ID); !errors.Is(e, state.ErrNotFound) {
				t.Fatal("fenced fixture remained reachable")
			}
			r.Status = "ready"
			if _, e = catalog.TransitionScenarioPostgres(t.Context(), r, "sealing"); e == nil {
				t.Fatal("retired fixture was reopened")
			}
			if f.pool != nil {
				if _, e = f.pool.Exec(t.Context(), `UPDATE scenario_postgres_fixtures SET backend_sha256=$2 WHERE id=$1`, r.ID, strings.Repeat("1", 64)); e == nil {
					t.Fatal("SQL changed the pinned backend")
				}
				if _, e = f.pool.Exec(t.Context(), `DELETE FROM scenario_postgres_fixtures WHERE id=$1`, r.ID); e == nil {
					t.Fatal("active cleanup journal could be removed")
				}
				if _, e = f.pool.Exec(t.Context(), `UPDATE scenario_postgres_fixtures SET role_name='grpg_11111111111111111111111111111111' WHERE id=$1`, r.ID); e == nil {
					t.Fatal("SQL changed immutable login")
				}
				if _, e = f.pool.Exec(t.Context(), `UPDATE scenario_postgres_fixtures SET status='ready' WHERE id=$1`, r.ID); e == nil {
					t.Fatal("SQL reopened fixture")
				}
				r.Status = "deleted"
				if _, e = catalog.TransitionScenarioPostgres(t.Context(), r, "sealing"); e != nil {
					t.Fatal(e)
				}
				if _, e = f.pool.Exec(t.Context(), `DELETE FROM scenario_postgres_fixtures WHERE id=$1`, r.ID); e != nil {
					t.Fatalf("confirmed cleanup journal blocked retention: %v", e)
				}
			}
		})
	}
}

const postgresJournalRun = "abababababababababababababababab"

type postgresCatalogFixture struct {
	base    state.Store
	pool    *pgxpool.Pool
	account state.Account
	app     state.App
}

func newPostgresCatalogFixture(t *testing.T, backend string) postgresCatalogFixture {
	t.Helper()
	f := postgresCatalogFixture{}
	if backend == "postgres" {
		f.pool = pgtest.OpenMigrated(t)
		f.base = state.NewPgStore(f.pool)
	} else {
		f.base = state.NewMemStore()
	}
	var err error
	f.account, err = f.base.CreateAccount(t.Context(), uuid.NewString()+"@postgres-fixture.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = f.base.CreateApp(t.Context(), state.App{AccountID: f.account.ID, Slug: "ordinary-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTaskFixtureCatalogProjectsEnrolledAccess(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newPostgresCatalogFixture(t, backend)
			ctx := t.Context()
			expires := time.Now().Add(time.Hour)
			plan := rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{
				{Workload: "primary", Postgres: rehearsalwork.PostgresReadWrite},
				{Workload: "worker", Postgres: rehearsalwork.PostgresTaskRuntime, Async: true},
				{Workload: "observer", Postgres: rehearsalwork.PostgresTaskObserver},
				{Workload: "unbound"},
			}}
			names := []string{"primary", "worker", "observer", "unbound"}
			managed, _, err := rehearsalwork.Normalize(plan, names)
			if err != nil {
				t.Fatal(err)
			}
			simulation := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{
				Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails",
				Responses: []api.ScenarioSimulationResponse{{Status: 200}},
			}}}
			sim, _, err := api.NormalizeScenarioSimulationPlan(simulation, names)
			if err != nil {
				t.Fatal(err)
			}
			var members []state.ScenarioTestMember
			for _, name := range names {
				app, err := f.base.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: f.account.ID, Slug: name + "-" + uuid.NewString()[:8],
					Type: state.AppTypeApp, RAMMB: 128, PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
				if err != nil {
					t.Fatal(err)
				}
				members = append(members, state.ScenarioTestMember{AppID: app.ID, Workload: name, WebhookMode: "capture", NetworkMode: "contained",
					SimulationJSON: string(sim), ManagedWorkJSON: string(managed)})
			}
			if err := f.base.RegisterScenarioTestMembers(ctx, f.account.ID, postgresJournalRun, members); err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			role := "grpg_" + strings.ReplaceAll(id, "-", "")
			r := rehearsalpg.Resource{
				ID: id, AccountID: f.account.ID, RunID: postgresJournalRun, AppID: members[0].AppID, OwnerID: uuid.NewString(),
				DatabaseName: role, RoleName: role, BackendSHA256: strings.Repeat("f", 64), ExpiresAt: time.Now().Add(time.Minute),
				Spec: rehearsalpg.Spec{Version: 1, FixtureSHA256: strings.Repeat("a", 64), MaxLockWaitMS: 50,
					Tables: []rehearsalpg.Table{{Schema: "public", Name: "gregale_mcp_tasks", Columns: []string{"item"}}}},
			}
			catalog := f.base.(rehearsalpg.Catalog)
			r, err = catalog.ReserveScenarioPostgres(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			r.Status = "ready"
			r.Initial = []rehearsalpg.Snapshot{{Table: "public.gregale_mcp_tasks", Rows: []json.RawMessage{}}}
			if _, err := catalog.TransitionScenarioPostgres(ctx, r, "provisioning"); err != nil {
				t.Fatal(err)
			}
			participants, err := catalog.ScenarioPostgresTaskParticipants(ctx, r)
			if err != nil || len(participants) != 2 {
				t.Fatalf("expected enrolled runtime and observer: %v %v", participants, err)
			}
			expected := map[string]string{members[1].AppID: rehearsalwork.PostgresTaskRuntime, members[2].AppID: rehearsalwork.PostgresTaskObserver}
			for _, participant := range participants {
				if expected[participant.AppID] != participant.Access {
					t.Fatalf("wrong participant for sealed fixture: %+v", participant)
				}
				delete(expected, participant.AppID)
			}
			for _, member := range members {
				got, err := catalog.ScenarioPostgresForApp(ctx, member.AppID)
				wantAccess := ""
				wantRole := r.RoleName
				switch member.Workload {
				case "unbound":
					if !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("unenrolled access: %v", err)
					}
					continue
				case "worker", "observer":
					part, _ := plan.Participant(member.Workload)
					wantAccess = part.Postgres
					wantRole = rehearsalwork.PostgresParticipantRole(r.ID, member.AppID, wantAccess)
				}
				if err != nil || got.ID != r.ID || got.RoleName != wantRole || got.ParticipantAccess != wantAccess {
					t.Fatalf("fixture projection for %s: %+v %v", member.Workload, got, err)
				}
			}
			r.Status = "sealing"
			r, err = catalog.TransitionScenarioPostgres(ctx, r, "ready")
			if err != nil {
				t.Fatal(err)
			}
			participants, err = catalog.ScenarioPostgresTaskParticipants(ctx, r)
			if err != nil || len(participants) != 2 {
				t.Fatalf("sealing lost the enrolled roles: %v %v", participants, err)
			}
		})
	}
}
