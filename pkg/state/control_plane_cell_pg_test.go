//go:build !no_pg

package state_test

import (
	"errors"
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

func TestControlPlaneCellStoreIntent(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := (&sqlc.Queries{}).InitializeControlPlaneCell(ctx, pool, sqlc.InitializeControlPlaneCellParams{
		CellID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Region: "eu-fsn",
	}); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "cell-intent@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	// A registry record alone cannot make another region part of this cell.
	if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: "eu-hel", DisplayName: "Helsinki", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	for _, region := range []string{"", "eu-fsn", "eu-hel"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "cell-" + uuid.NewString(), Runtime: "node22", ComputeRegion: region})
		if region == "eu-hel" {
			var problem *api.Problem
			if !errors.As(err, &problem) || problem.Code != api.CodeComputeRegionOutsideCell || problem.Status != http.StatusUnprocessableEntity || problem.ComputeRegion != region {
				t.Fatalf("foreign intent: %v", err)
			}
			if api.StatusForCode(problem.Code) != problem.Status {
				t.Fatal("foreign cell error lost its status across transport")
			}
			continue
		}
		if err != nil || app.ComputeRegion != region {
			t.Fatalf("allowed intent region=%q app=%+v err=%v", region, app, err)
		}
	}
}
