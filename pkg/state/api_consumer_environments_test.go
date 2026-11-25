package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type apiConsumerEnvironmentTestStore = contractTestStore

// adr: 20261104120000 — a stage's consumers and keys are its own: production
// reads never see them, a stage host answers with production's keys until
// the stage has consumers and with only its own after, plan assignments
// follow their consumer and trials stay production's.
func exerciseAPIConsumerEnvironments(t *testing.T, store apiConsumerEnvironmentTestStore) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "consumer-env-"+suffix+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "cenv-" + suffix})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "cenv-" + suffix,
		Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 4})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	stage, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatalf("CreateProjectEnvironment: %v", err)
	}
	newKey := func(consumerID, name string) state.ConsumerKey {
		t.Helper()
		_, prefix, hash, err := api.GenerateConsumerKey()
		if err != nil {
			t.Fatal(err)
		}
		k, err := store.CreateConsumerKeyForConsumer(ctx, account.ID, consumerID, name, prefix, hash, []string{"read"}, nil)
		if err != nil {
			t.Fatalf("CreateConsumerKeyForConsumer(%s): %v", name, err)
		}
		return k
	}

	prod, err := store.CreateAPIConsumerInEnvironment(ctx, account.ID, app.ID, "production", "customer-1", "Acme")
	if err != nil || prod.EnvironmentID != "" {
		t.Fatalf("production consumer = %+v, %v; want no environment", prod, err)
	}
	prodKey := newKey(prod.ID, "primary")
	if k, err := store.ConsumerKeyForEnvironment(ctx, account.ID, app.ID, stage.ID, prodKey.Prefix); err != nil || k.ID != prodKey.ID {
		t.Fatalf("stage without consumers = %+v, %v; want production's key", k, err)
	}

	// The app-wide identity indexes stay until the contract release.
	if _, err := store.CreateAPIConsumerInEnvironment(ctx, account.ID, app.ID, "staging", "customer-1", "Acme staging"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stage reusing production's external_ref = %v, want ErrConflict until the contract release", err)
	}
	staged, err := store.CreateAPIConsumerInEnvironment(ctx, account.ID, app.ID, "staging", "customer-stage", "Acme staging")
	if err != nil || staged.EnvironmentID != stage.ID {
		t.Fatalf("stage consumer = %+v, %v; want environment %s", staged, err, stage.ID)
	}
	if _, err := store.CreateAPIConsumerInEnvironment(ctx, account.ID, app.ID, "staging", "customer-stage", "again"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate stage external_ref = %v, want ErrConflict", err)
	}
	if _, err := store.CreateAPIConsumerInEnvironment(ctx, account.ID, app.ID, "missing", "customer-2", "x"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown environment = %v, want ErrNotFound", err)
	}
	stageKey := newKey(staged.ID, "stage")
	if stageKey.EnvironmentID != stage.ID {
		t.Fatalf("stage key environment = %q, want %s", stageKey.EnvironmentID, stage.ID)
	}

	assertConsumers := func(label string, got []state.APIConsumer, err error, want string) {
		t.Helper()
		if err != nil || len(got) != 1 || got[0].ID != want {
			t.Fatalf("%s = %+v, %v; want only %s", label, got, err, want)
		}
	}
	rows, err := store.ListAPIConsumersForApp(ctx, account.ID, app.ID)
	assertConsumers("ListAPIConsumersForApp", rows, err, prod.ID)
	rows, err = store.ListAPIConsumersInEnvironment(ctx, account.ID, app.ID, "")
	assertConsumers("production list", rows, err, prod.ID)
	rows, err = store.ListAPIConsumersInEnvironment(ctx, account.ID, app.ID, "staging")
	assertConsumers("stage list", rows, err, staged.ID)

	keys, err := store.ListConsumerKeysForApp(ctx, account.ID, app.ID)
	if err != nil || len(keys) != 1 || keys[0].ID != prodKey.ID {
		t.Fatalf("ListConsumerKeysForApp = %+v, %v; want production's key only", keys, err)
	}
	keys, err = store.ListConsumerKeysForConsumer(ctx, account.ID, staged.ID)
	if err != nil || len(keys) != 1 || keys[0].ID != stageKey.ID {
		t.Fatalf("ListConsumerKeysForConsumer = %+v, %v; want the stage key", keys, err)
	}

	if _, err := store.ConsumerKeyByAppAndPrefix(ctx, account.ID, app.ID, stageKey.Prefix); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("production host with a stage key = %v, want ErrNotFound", err)
	}
	if k, err := store.ConsumerKeyForEnvironment(ctx, account.ID, app.ID, stage.ID, stageKey.Prefix); err != nil || k.ID != stageKey.ID {
		t.Fatalf("stage host with its key = %+v, %v", k, err)
	}
	if _, err := store.ConsumerKeyForEnvironment(ctx, account.ID, app.ID, stage.ID, prodKey.Prefix); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stage with consumers and a production key = %v, want ErrNotFound", err)
	}
	if k, err := store.ConsumerKeyForEnvironment(ctx, account.ID, app.ID, "", prodKey.Prefix); err != nil || k.ID != prodKey.ID {
		t.Fatalf("no environment = %+v, %v; want production's key", k, err)
	}

	now := time.Now().UTC().Truncate(time.Minute)
	if _, err := store.PutAPIConsumerTrial(ctx, state.APIConsumerTrial{AccountID: account.ID, AppID: app.ID, ConsumerID: staged.ID,
		StartsAt: now, EndsAt: now.Add(24 * time.Hour)}); !errors.Is(err, state.ErrAPIConsumerStageTrial) {
		t.Fatalf("stage consumer trial = %v, want ErrAPIConsumerStageTrial", err)
	}
	if _, err := store.AssignAPIConsumerPlan(ctx, state.APIConsumerPlanAssignment{AccountID: account.ID, AppID: app.ID, ConsumerID: staged.ID,
		EffectiveFrom: now}); err != nil {
		t.Fatalf("stage consumer plan assignment = %v, want it to follow the consumer", err)
	}

	// A stage whose consumers are all revoked answers with production's keys again.
	if _, err := store.RevokeAPIConsumer(ctx, account.ID, staged.ID); err != nil {
		t.Fatalf("RevokeAPIConsumer: %v", err)
	}
	if k, err := store.ConsumerKeyForEnvironment(ctx, account.ID, app.ID, stage.ID, prodKey.Prefix); err != nil || k.ID != prodKey.ID {
		t.Fatalf("stage with only revoked consumers = %+v, %v; want production's key", k, err)
	}
}

func TestMemStoreAPIConsumerEnvironments(t *testing.T) {
	exerciseAPIConsumerEnvironments(t, state.NewMemStore())
}

func TestPgStoreAPIConsumerEnvironments(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAPIConsumerEnvironments(t, state.NewPgStore(pool))
}
