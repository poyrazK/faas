package devbridgeintegration

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDevBridgeStoreParity(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			var store state.Store
			var fresh func() state.DevBridgeStore
			if kind == "memory" {
				store = state.NewMemStore()
				fresh = func() state.DevBridgeStore { return store.(state.DevBridgeStore) }
			} else {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				pg := state.NewPgStore(pool)
				store = pg
				fresh = func() state.DevBridgeStore { return state.NewPgStore(pool) }
			}
			bridges := store.(state.DevBridgeStore)
			ctx := t.Context()
			account, err := store.CreateAccount(ctx, "bridge-parity@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
			if err != nil {
				t.Fatal(err)
			}
			env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "development"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "payments", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 128, WorkloadClass: state.WorkloadClassHTTP})
			if err != nil {
				t.Fatal(err)
			}
			scope := devbridge.Scope{AccountID: account.ID, DeveloperID: "alice", ProjectID: project.ID, EnvironmentID: env.ID, TargetAppID: app.ID}
			now := time.Now().UTC()
			session, credentials, err := devbridge.NewSession(scope, now, now.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			old, _, err := devbridge.NewSession(scope, now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour+time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if err := bridges.CreateDevBridge(ctx, old); err != nil {
				t.Fatal(err)
			}
			if err := bridges.CreateDevBridge(ctx, session); err != nil {
				t.Fatal(err)
			}
			if _, err := bridges.DevBridgeByID(ctx, account.ID, old.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("expired metadata not pruned: %v", err)
			}
			// A fresh PgStore wrapper must recover credentials from persisted digests.
			stored, err := fresh().DevBridgeByID(ctx, account.ID, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := stored.AuthorizeAttachment(now, credentials.AttachmentToken); err != nil {
				t.Fatal(err)
			}
			// adr: 379 — inventory must recover the same durable session in
			// a fresh store, without exposing another account or expired rows.
			rows, err := fresh().ListDevBridges(ctx, account.ID, 1)
			if err != nil || len(rows) != 1 || rows[0].ID != session.ID {
				t.Fatalf("active inventory: %+v %v", rows, err)
			}
			if rows, err := fresh().ListDevBridges(ctx, uuid.NewString(), 1); err != nil || len(rows) != 0 {
				t.Fatalf("foreign inventory: %+v %v", rows, err)
			}
			for _, limit := range []int{0, api.DevBridgeInventoryLimit + 1} {
				if _, err := bridges.ListDevBridges(ctx, account.ID, limit); err == nil {
					t.Fatal("invalid inventory bound accepted")
				}
			}
			second, _, err := devbridge.NewSession(scope, now, now.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if err := bridges.CreateDevBridge(ctx, second); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("session quota bypassed: %v", err)
			}
			if err := bridges.RevokeDevBridge(ctx, account.ID, session.ID, now); err != nil {
				t.Fatal(err)
			}
			if err := bridges.CreateDevBridge(ctx, second); err != nil {
				t.Fatalf("revocation did not release quota: %v", err)
			}
			rows, err = fresh().ListDevBridges(ctx, account.ID, api.DevBridgeInventoryLimit)
			if err != nil || len(rows) != 1 || rows[0].ID != second.ID {
				t.Fatalf("revoked inventory: %+v %v", rows, err)
			}
			testWebhookReplayLedger(t, store, second)
			stored, err = bridges.DevBridgeByID(ctx, account.ID, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.AuthorizeAttachment(now, credentials.AttachmentToken) == nil {
				t.Fatal("stored revocation lost")
			}
			if _, err := bridges.DevBridgeByID(ctx, project.ID, session.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("cross-account read: %v", err)
			}
		})
	}
}

func testWebhookReplayLedger(t *testing.T, store state.Store, session devbridge.Session) {
	ledger := store.(state.DevBridgeWebhookStore)
	in := devbridge.WebhookReplay{ID: uuid.NewString(), SessionID: session.ID, AccountID: session.Scope.AccountID, InvocationID: uuid.NewString(), IdempotencyKey: "selected-delivery"}
	var created atomic.Int32
	var workers sync.WaitGroup
	for n := 0; n < 8; n++ {
		workers.Go(func() {
			out, first, err := ledger.ReserveDevBridgeWebhookReplay(t.Context(), in)
			if err != nil || out.ID != in.ID {
				t.Errorf("reserve: %v", err)
			}
			if first {
				created.Add(1)
			}
		})
	}
	workers.Wait()
	if created.Load() != 1 {
		t.Fatal("same key admitted more than one dispatch")
	}
	if err := ledger.FinishDevBridgeWebhookReplay(t.Context(), in.AccountID, in.ID, "completed", 204); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < api.DevBridgeMaxWebhookReplays-1; n++ {
		copy := in
		copy.ID, copy.IdempotencyKey = uuid.NewString(), uuid.NewString()
		if _, first, err := ledger.ReserveDevBridgeWebhookReplay(t.Context(), copy); err != nil || !first {
			t.Fatalf("bounded replay reserve: %v", err)
		}
	}
	out, first, err := ledger.ReserveDevBridgeWebhookReplay(t.Context(), in)
	if err != nil || first || out.State != "completed" || out.HTTPStatus != 204 {
		t.Fatalf("dedupe at quota: %+v first=%v err=%v", out, first, err)
	}
	copy := in
	copy.ID, copy.IdempotencyKey = uuid.NewString(), "above-quota"
	if _, _, err := ledger.ReserveDevBridgeWebhookReplay(t.Context(), copy); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replay quota bypassed: %v", err)
	}
	copy = in
	copy.InvocationID = uuid.NewString()
	if _, _, err := ledger.ReserveDevBridgeWebhookReplay(t.Context(), copy); !errors.Is(err, state.ErrConflict) {
		t.Fatal("key retargeted another delivery")
	}
	if _, err := ledger.DevBridgeWebhookReplayByID(t.Context(), uuid.NewString(), session.ID, in.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign replay inspection accepted")
	}
	if _, err := ledger.DevBridgeWebhookReplayByID(t.Context(), in.AccountID, "another-session", in.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-session replay inspection accepted")
	}
}

func TestDevBridgeRelayDatabaseIsReadOnly(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	cfg := pool.Config().ConnConfig
	dsn := cfg.ConnString()
	searchPath := cfg.RuntimeParams["search_path"]
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		u.Path = "/" + cfg.Database
		query := u.Query()
		if searchPath != "" {
			query.Set("search_path", searchPath)
		} else {
			// Template clones use PostgreSQL's default path; an empty value
			// would hide their public schema from the read-only connection.
			query.Del("search_path")
		}
		u.RawQuery = query.Encode()
		dsn = u.String()
	} else {
		quote := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
		dsn += " dbname='" + quote.Replace(cfg.Database) + "'"
		if searchPath != "" {
			dsn += " search_path='" + quote.Replace(searchPath) + "'"
		}
	}
	readonly, err := db.OpenReadOnlyWithAppName(t.Context(), dsn, "bridged")
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	first, err := readonly.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := readonly.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	for _, conn := range []*pgxpool.Conn{first, second} {
		var setting string
		if err := conn.QueryRow(t.Context(), "SELECT current_setting('default_transaction_read_only')").Scan(&setting); err != nil || setting != "on" {
			t.Fatalf("read-only connection: setting=%s err=%v", setting, err)
		}
		var count int
		if err := conn.QueryRow(t.Context(), "SELECT count(*) FROM dev_bridge_sessions").Scan(&count); err != nil {
			t.Fatal(err)
		}
		_, err := conn.Exec(t.Context(), "UPDATE dev_bridge_sessions SET revoked_at=now()")
		var denied *pgconn.PgError
		if !errors.As(err, &denied) || denied.Code != "25006" {
			t.Fatalf("relay database accepted write: %v", err)
		}
	}
}
