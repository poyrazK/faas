//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestImagePreparationPhaseTimingMigration(t *testing.T) {
	ctx := t.Context()
	pool := openMigrated(t)
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "phase-timing@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	var deps []state.Deployment
	for _, slug := range []string{"legacy", "old-writer"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example.test/image"})
		if err != nil {
			t.Fatal(err)
		}
		deps = append(deps, dep)
	}
	p, err := store.BeginImagePreparation(ctx, deps[0].ID, "node-a")
	if err != nil || p.PhaseStartedAt.IsZero() {
		t.Fatalf("begin=%+v err=%v", p, err)
	}
	raw, err := migrations.FS.ReadFile("20261017112758515_image_preparation_phase_timing.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	var path, phase string
	if err := pool.QueryRow(ctx, "SELECT input_path,phase FROM deployment_image_preparations WHERE deployment_id=$1", deps[0].ID).Scan(&path, &phase); err != nil || path != deps[0].ImageDigest || phase != "preparing" {
		t.Fatalf("rollback changed work: path=%q phase=%q err=%v", path, phase, err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.BeginImagePreparation(ctx, deps[0].ID, "node-a")
	if err != nil || !legacy.PhaseStartedAt.IsZero() {
		t.Fatalf("reclaim invented legacy timing: %+v %v", legacy, err)
	}
	s, err := store.ImagePreparationProgressSnapshot(ctx, "node-a")
	if err != nil || s.Preparing.Pending != 1 || s.Preparing.UnknownAge != 1 || !s.Preparing.OldestPhaseStartedAt.IsZero() {
		t.Fatalf("legacy snapshot=%+v %v", s, err)
	}

	// An older writer omits the new column and only refreshes updated_at and
	// claim_token. The trigger supplies timing on insert and preserves it on
	// same-phase updates, including attempts to overwrite the timestamp.
	if _, err := pool.Exec(ctx, `INSERT INTO deployment_image_preparations
 (deployment_id,node_name,input_path,input_key,input_bytes,claim_token,phase)
 VALUES ($1,'node-a','example.test/image','',0,$2,'preparing')`, deps[1].ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var before, after time.Time
	if err := pool.QueryRow(ctx, "SELECT phase_started_at FROM deployment_image_preparations WHERE deployment_id=$1", deps[1].ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Replaying expanded DDL must retain both known progress and unknown
	// legacy timing, including when the function and trigger already exist.
	for range 2 {
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("replay existing phase timing schema: %v", err)
		}
	}
	s, err = store.ImagePreparationProgressSnapshot(ctx, "node-a")
	if err != nil || s.Preparing.Pending != 2 || s.Preparing.UnknownAge != 1 || !s.Preparing.OldestPhaseStartedAt.Equal(before) {
		t.Fatalf("replay changed phase timing: %+v %v", s, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE deployment_image_preparations SET updated_at=now(),claim_token=$2,phase_started_at='infinity' WHERE deployment_id=$1", deps[1].ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT phase_started_at FROM deployment_image_preparations WHERE deployment_id=$1", deps[1].ID).Scan(&after); err != nil || !after.Equal(before) {
		t.Fatalf("same-phase update renewed timing: %v %v %v", before, after, err)
	}
	s, err = store.ImagePreparationProgressSnapshot(ctx, "node-a")
	if err != nil || s.Preparing.Pending != 2 || s.Preparing.UnknownAge != 1 || !s.Preparing.OldestPhaseStartedAt.Equal(before) {
		t.Fatalf("mixed known/unknown snapshot=%+v %v", s, err)
	}

	// A rolled-back phase write leaves both progress and timing untouched.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "UPDATE deployment_image_preparations SET phase='layer_published' WHERE deployment_id=$1", deps[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT phase_started_at FROM deployment_image_preparations WHERE deployment_id=$1", deps[1].ID).Scan(&after); err != nil || !after.Equal(before) {
		t.Fatalf("rollback changed timing: %v %v", after, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE deployment_image_preparations SET phase='layer_published',updated_at=now() WHERE deployment_id=$1", deps[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT phase_started_at FROM deployment_image_preparations WHERE deployment_id=$1", deps[1].ID).Scan(&after); err != nil || !after.After(before) {
		t.Fatalf("old writer phase advance did not record progress: %v %v", after, err)
	}
	if err := store.TransitionImagePreparation(ctx, deps[0].ID, legacy.ClaimToken, state.DeployImaging); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishImagePreparationLayer(ctx, deps[0].ID, legacy.ClaimToken, "/prepared", "prepared", 42); err != nil {
		t.Fatal(err)
	}
	s, err = store.ImagePreparationProgressSnapshot(ctx, "node-a")
	if err != nil || s.Preparing.Pending != 0 || s.LayerPublished.Pending != 2 || s.LayerPublished.UnknownAge != 0 || s.LayerPublished.OldestPhaseStartedAt.IsZero() {
		t.Fatalf("progress did not establish known timing: %+v %v", s, err)
	}
}
