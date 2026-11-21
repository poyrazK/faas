package taskrehearsal

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding/taskacl"
)

type testStore interface {
	Store
	rehearsalpg.Catalog
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	UpsertAppSecret(context.Context, string, string, string, []byte) error
	GetAppSecret(context.Context, string, string, string) (*state.AppSecret, error)
}

func TestPostgresRealTaskWorkloadProvisioner(t *testing.T) {
	dsn := os.Getenv("FAAS_REHEARSAL_PG_TEST_ADMIN_URL")
	if dsn == "" {
		t.Skip("requires dedicated isolated PostgreSQL fixture cluster")
	}
	ctx := t.Context()
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid fixture administrator configuration")
	}
	backend, err := pgruntime.NewPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal("cannot connect to fixture administrator")
	}
	defer func() { _ = admin.Close(ctx) }()

	for _, scenario := range []struct {
		name   string
		failAt string
	}{
		{name: "finish and recover cleanup after restart"},
		{name: "migration secret delivery interrupted", failAt: Migration},
		{name: "observer secret delivery interrupted", failAt: Observer},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var store, restartedStore testStore
			if os.Getenv("DATABASE_URL") != "" {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				store, restartedStore = state.NewPgStore(pool), state.NewPgStore(pool)
			} else {
				store = state.NewMemStore()
				restartedStore = store
			}
			ctx := t.Context()
			account, err := store.CreateAccount(ctx, "rehearsal@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			run := state.HostedTaskRehearsalRun{
				ID: uuid.NewString(), ReservedAt: time.Now().UTC().Truncate(time.Microsecond),
				ApprovalExpiresAt: time.Now().Add(time.Minute),
			}
			seal, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			urls := map[string]string{}
			p := &Provisioner{
				AdmissionEnabled: true,
				Store:            store, Postgres: pgruntime.NewService(store, backend),
				BuildApp: func(account state.Account, request api.CreateAppRequest, limits api.Limits) (state.App, *api.Problem) {
					return state.App{AccountID: account.ID, Slug: request.Slug, Type: state.AppTypeApp,
						Visibility: api.AppVisibility(request.Visibility), RAMMB: api.StandaloneAppDefaultRAMMB(limits),
						MaxConcurrency: 1}, nil
				},
				PersistDatabaseURL: func(ctx context.Context, account state.Account, app state.App, connection string) error {
					member, err := store.ScenarioTestMemberByApp(ctx, app.ID)
					if err != nil {
						return err
					}
					urls[member.Workload] = connection
					ciphertext, err := secretbox.SealOneBound(seal.Recipient(), secretbox.Binding{AccountID: account.ID}, databaseKey, connection, 4096)
					if err != nil {
						return err
					}
					if err := store.UpsertAppSecret(ctx, account.ID, app.ID, databaseKey, ciphertext); err != nil {
						return err
					}
					if scenario.failAt == member.Workload {
						return errors.New("secret persistence interrupted after write")
					}
					return nil
				},
			}
			restarted := &Provisioner{Store: restartedStore, Postgres: pgruntime.NewService(restartedStore, backend)}
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
				defer cancel()
				if err := restarted.Cleanup(cleanup, account.ID, run.ID); err != nil {
					t.Error(err)
				}
			})

			prepared, err := p.Provision(ctx, account, run)
			identity, targetErr := restarted.target(account, run.ID)
			if targetErr != nil {
				t.Fatal(targetErr)
			}
			if scenario.failAt == Migration {
				if err == nil || prepared != nil {
					t.Fatal("failed migration secret delivery returned a usable target")
				}
				assertDisposed(t, admin, store, identity)
				return
			}
			if err != nil || prepared.Namespace() != "rehearsal-"+run.ID {
				t.Fatalf("prepare reserved run: %v", err)
			}
			if len(urls) != 1 || urls[Migration] == "" {
				t.Fatal("initial preparation delivered credentials outside the migration workload")
			}
			for _, role := range workloads {
				app, err := store.AppByID(ctx, identity.AppID(role.name))
				if err != nil || !identity.owns(app, role.name) || app.PreviewExpiresAt == nil ||
					!app.PreviewExpiresAt.Equal(run.ReservedAt.Add(leaseDuration)) {
					t.Fatalf("workload %s is outside the reserved private lease: %v", role.name, err)
				}
				member, err := store.ScenarioTestMemberByApp(ctx, app.ID)
				if err != nil || member.RunID != identity.scenarioID() ||
					member.NetworkMode != state.ScenarioNetworkContained || member.WebhookMode != state.ScenarioWebhookCaptureMode {
					t.Fatalf("workload %s was not captured by containment: %v", role.name, err)
				}
			}

			migration := connectFixture(t, adminConfig, urls[Migration])
			defer func() { _ = migration.Close(ctx) }()
			for _, table := range taskacl.ReadTables(false) {
				// Test-only schema installed as the fixture owner, as a guest
				// release command would do with the reviewed migration.
				if _, err := migration.Exec(ctx, "CREATE TABLE public."+pgx.Identifier{table}.Sanitize()+" (item text)"); err != nil {
					t.Fatal("could not install controlled Task grant fixture")
				}
			}
			if _, err := migration.Exec(ctx, "CREATE SEQUENCE public.gregale_mcp_task_claim_order_seq"); err != nil {
				t.Fatal("could not install controlled Task claim sequence")
			}
			err = prepared.BindRuntime(ctx)
			if scenario.failAt != "" {
				if err == nil {
					t.Fatal("partial runtime grant appeared complete")
				}
				assertDisposed(t, admin, store, identity)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := prepared.BindRuntime(ctx); !errors.Is(err, state.ErrConflict) {
				t.Fatal("binding retried the one-shot credential handoff")
			}
			logins := map[string]bool{migration.Config().User: true}
			for _, name := range []string{Web, Worker, Observer} {
				conn := connectFixture(t, adminConfig, urls[name])
				defer func() { _ = conn.Close(ctx) }()
				if conn.Config().Database != migration.Config().Database || logins[conn.Config().User] {
					t.Fatalf("workload %s shares a login or uses a different database", name)
				}
				logins[conn.Config().User] = true
				_, err := conn.Exec(ctx, "INSERT INTO public.gregale_mcp_tasks VALUES ('queued')")
				if name == Observer {
					assertInsufficientPrivilege(t, err)
					var count int
					if err := conn.QueryRow(ctx, "SELECT count(*) FROM public.gregale_mcp_tasks").Scan(&count); err != nil || count != 2 {
						t.Fatalf("observer did not read both runtime writes: %v", err)
					}
				} else {
					if err != nil {
						t.Fatalf("runtime could not write Task state: %s", name)
					}
					_, err = conn.Exec(ctx, "CREATE TABLE public.forbidden (item text)")
					assertInsufficientPrivilege(t, err)
				}
			}
			if err := restarted.Cleanup(ctx, account.ID, run.ID); err != nil {
				t.Fatal(err)
			}
			assertDisposed(t, admin, store, identity)
		})
	}
}

func connectFixture(t *testing.T, admin *pgx.ConnConfig, connection string) *pgx.Conn {
	t.Helper()
	config, err := pgx.ParseConfig(connection)
	if err != nil {
		t.Fatal("invalid fixture connection")
	}
	config.Host, config.Port = admin.Host, admin.Port
	conn, err := pgx.ConnectConfig(t.Context(), config)
	if err != nil {
		t.Fatal("could not authenticate with the workload login")
	}
	return conn
}

func assertInsufficientPrivilege(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatal("expected PostgreSQL insufficient privilege")
	}
}

func assertDisposed(t *testing.T, admin *pgx.Conn, store testStore, identity *Prepared) {
	t.Helper()
	ctx := t.Context()
	fixture, err := store.GetScenarioPostgres(ctx, identity.account.ID, identity.scenarioID())
	if err != nil || fixture.Status != "deleted" {
		t.Fatalf("fixture journal did not record confirmed deletion: %v", err)
	}
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", fixture.DatabaseName).Scan(&exists); err != nil || exists {
		t.Fatalf("fixture database remains: %v", err)
	}
	for _, role := range workloads {
		appID := identity.AppID(role.name)
		app, err := store.AppByID(ctx, appID)
		if err != nil || app.Status != state.AppDeleted {
			t.Fatalf("workload %s was not tombstoned: %v", role.name, err)
		}
		if _, err := store.GetAppSecret(ctx, identity.account.ID, appID, databaseKey); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("credential for %s remains in the store: %v", role.name, err)
		}
		login := fixture.RoleName
		if role.name != Migration {
			login = rehearsalwork.PostgresParticipantRole(fixture.ID, appID, role.access)
		}
		if err := admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)", login).Scan(&exists); err != nil || exists {
			t.Fatalf("login for %s remains: %v", role.name, err)
		}
	}
}
