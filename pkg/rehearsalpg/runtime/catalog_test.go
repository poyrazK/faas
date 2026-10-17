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
