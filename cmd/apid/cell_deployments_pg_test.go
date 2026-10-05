//go:build !no_pg

package main

// adr: 603

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func cellDeploymentPGHarness(t *testing.T) pgHandlerEnv {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := (&sqlc.Queries{}).InitializeControlPlaneCell(t.Context(), pool, sqlc.InitializeControlPlaneCellParams{CellID: pgtype.UUID{Bytes: id, Valid: true}, Region: "eu-fsn"}); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(t.Context(), "cell-release-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountEmailVerified(t.Context(), acct.ID); err != nil {
		t.Fatal(err)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), acct.ID, hash, "regional-release", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	configureCellDeploymentTest(t, store, srv, "eu-fsn")
	srv.controlPlaneCell = db.CellIdentity{ID: id.String(), Region: "eu-fsn"}
	return pgHandlerEnv{h: srv.handler(), s: srv, store: store, pool: pool, key: token, acct: acct}
}

func TestCellDeploymentPGDeleteBetweenLookupAndInsert(t *testing.T) {
	e := cellDeploymentPGHarness(t)
	runCellDeploymentDeleteRace(t, e.store, e.s, e.acct, e.key)
}

func TestCellDeploymentPGReplayRejectsDeletedIdentity(t *testing.T) {
	e := cellDeploymentPGHarness(t)
	app := seedCellDeploymentApp(t, e.store, e.acct, "replay-shop", uuid.NewString(), "eu-fsn")
	path := cellDeploymentURL(e.s.controlPlaneCell.ID, app.ID)
	headers := map[string]string{"Idempotency-Key": "regional-retry"}
	first := e.do(t, http.MethodPost, path, cellImageRequest(), headers)
	if first.Code != http.StatusAccepted {
		t.Fatalf("acceptance: %d %s", first.Code, first.Body)
	}
	if _, err := e.store.RenameApp(t.Context(), e.acct.ID, app.Slug, "retired-"+app.ID[:8]); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	replacement := seedCellDeploymentApp(t, e.store, e.acct, app.Slug, uuid.NewString(), "eu-fsn")
	retry := e.do(t, http.MethodPost, path, cellImageRequest(), headers)
	if retry.Code != http.StatusNotFound || retry.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("deleted replay: %d %s", retry.Code, retry.Body)
	}
	deps, err := e.store.ListDeploymentsForApp(t.Context(), replacement.ID, 0, 0)
	if err != nil || len(deps) != 0 {
		t.Fatalf("replacement received stale intent: %d %v", len(deps), err)
	}
}
