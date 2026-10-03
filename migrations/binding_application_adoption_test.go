//go:build !no_pg

package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingApplicationAdoptionMigrationPreservesReceipts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	acct, app := seedAccount(t, ctx, pool), ""
	app = seedApp(t, ctx, pool, acct)
	store := state.NewPgStore(pool)
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: "default", ImageDigest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app, deployment.ID, "running", 256, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, acct, app, "default", "DATABASE_URL", "kid", "0123456789abcdef", []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	revision, err := store.ReadBindingPromotionRevision(ctx, acct, app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordAppSecretRuntimeReload(ctx, state.AppSecretRuntimeReloadResult{AccountID: acct, AppID: app, InstanceID: instance.ID, Revision: strings.Repeat("a", 64), Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent, AttemptedAt: time.Now().UTC(), Candidates: []state.AppSecretDeliveryCandidate{{Scope: "default", Key: "DATABASE_URL", Version: 1}}}); err != nil {
		t.Fatal(err)
	}
	after, err := store.ReadBindingPromotionRevision(ctx, acct, app)
	if err != nil || after == revision {
		t.Fatalf("observation revision unchanged: %v", err)
	}
	if _, err := store.RecordAppSecretRuntimeReloadAck(ctx, state.AppSecretRuntimeReloadAckResult{AccountID: acct, AppID: app, InstanceID: instance.ID, Revision: strings.Repeat("a", 64), Status: state.SecretApplicationReloadAckApplied, AttemptedAt: time.Now().UTC(), Candidates: []state.AppSecretDeliveryCandidate{{Scope: "default", Key: "DATABASE_URL", Version: 1}}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.ListAppSecretRuntimeReloadObservations(ctx, acct, app, "default")
	if err != nil || len(before) != 1 {
		t.Fatalf("receipt: %+v %v", before, err)
	}
	source, err := os.ReadFile("20261002212247902_binding_application_adoption.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	for _, sql := range []string{down, up} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
		receipts, err := store.ListAppSecretRuntimeReloadObservations(ctx, acct, app, "default")
		if err != nil || len(receipts) != 1 || receipts[0].Version != 1 || receipts[0].ApplicationAckVersion != 1 || receipts[0].ApplicationAck != state.SecretApplicationReloadAckApplied || receipts[0].ApplicationAckAt == nil || !receipts[0].ApplicationAckAt.Equal(*before[0].ApplicationAckAt) {
			t.Fatalf("migration changed receipt: %+v %v", receipts, err)
		}
	}
}
