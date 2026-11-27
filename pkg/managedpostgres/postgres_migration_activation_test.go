package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresMigrationActivationReservesOnlyRecordedSwitch(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	ps := state.NewPgStore(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	app, err := ps.CreateApp(ctx, state.App{AccountID: account, Slug: "migration-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	release, err := ps.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	source := postgresReadyDatabase(t, store, account, "migration-source", now.Add(-time.Hour))
	target := postgresReadyDatabase(t, store, account, "migration-target", now.Add(-time.Hour))
	b := testBinding(account, source.ID, app.ID, uuid.NewString(), now)
	b.Scope = "default"
	if _, _, err := store.ReserveBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	b, err = store.ClaimBinding(ctx, account, b.ID, "binding", BindingStateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := (&postgresBindingCredentialSink{store: ps}).Put(ctx, b, bindingTestMaterial())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishBindingProvision(ctx, b.ID, "binding", "provider-role", ref, now); err != nil {
		t.Fatal(err)
	}
	request := PrepareCutoverRequest{ID: uuid.NewString(), AccountID: account, AppID: app.ID,
		Scope: "default", SourceDatabaseID: source.ID, TargetDatabaseID: target.ID}
	if _, _, err := store.ReserveCutover(ctx, request, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("ordinary non-restore reservation = %v", err)
	}
	sw, err := ps.CreateDatabaseSwitch(ctx, state.DatabaseSwitch{
		ID: request.ID, AccountID: account, AppID: app.ID, Scope: "default", FromDeploymentID: release.ID,
		TargetDeploymentID: release.ID, SourceDatabaseID: source.ID, TargetDatabaseID: target.ID,
		TargetName: target.Name, MigrationOperationID: uuid.NewString(), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sw.Phase != state.DatabaseSwitchStaging || !sw.RestorePoint.IsZero() || sw.MigrationOperationID == "" {
		t.Fatalf("unexpected migration switch: %+v", sw)
	}
	c, created, err := store.ReserveCutover(ctx, request, now)
	if err != nil || !created || len(c.Credentials) != 1 || c.Credentials[0].SourceBindingID != b.ID {
		t.Fatalf("reserve activation: created=%v cutover=%+v error=%v", created, c, err)
	}
}
