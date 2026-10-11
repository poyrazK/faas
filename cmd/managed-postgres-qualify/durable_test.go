package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDurableCatalogGuardIgnoresSessionSetting(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := context.Background()
	config := durableQualificationConfig{options: managedpostgres.DurableLifecycleOptions{RunID: uuid.NewString()}}
	if _, err := pool.Exec(ctx, "SELECT set_config('faas.qualification_run', $1, false)", config.options.RunID); err != nil {
		t.Fatal(err)
	}
	if err := config.checkCatalog(ctx, pool); err == nil {
		t.Fatal("session setting bypassed disposable database guard")
	}
	// This is test-only fixture DDL on the database created by pgtest.
	if _, err := pool.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{pool.Config().ConnConfig.Database}.Sanitize()+" SET faas.qualification_run TO '"+config.options.RunID+"'"); err != nil {
		t.Fatal(err)
	}
	if err := config.checkCatalog(ctx, pool); err != nil {
		t.Fatal(err)
	}
	config.options.RunID = uuid.NewString()
	if err := config.checkCatalog(ctx, pool); err == nil {
		t.Fatal("another run adopted this disposable catalog")
	}
}

func TestDurableQualificationPreflightRejectsForeignAppAndExistingSecret(t *testing.T) {
	t.Setenv("FAAS_PGTEST_TEMPLATE_DATABASE", "1")
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@durable-preflight.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "preflight-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	identityPath := filepath.Join(root, "identity.age")
	hmacPath := filepath.Join(root, "hmac.key")
	if err := os.WriteFile(identityPath, []byte("# standard age-keygen comment\n"+identity.String()+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hmacPath, []byte(strings.Repeat("h", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if _, err := pool.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{pool.Config().ConnConfig.Database}.Sanitize()+" SET faas.qualification_run TO '"+runID+"'"); err != nil {
		t.Fatal(err)
	}
	// pgtest rewrites Config.Database after parsing; ConnString retains the
	// original base URL. Explicitly target the owned cloned database here.
	catalogURL, err := url.Parse(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	catalogURL.Path = "/" + pool.Config().ConnConfig.Database
	catalogURL.RawPath = ""
	values := map[string]string{
		"FAAS_ENVIRONMENT": "staging", "FAAS_MANAGED_POSTGRES_QUALIFY_LIVE": "true", "FAAS_MANAGED_POSTGRES_QUALIFY_LIFECYCLE": "true",
		"FAAS_MANAGED_POSTGRES_QUALIFY_RUN_ID": runID, "FAAS_MANAGED_POSTGRES_QUALIFY_ACCOUNT_ID": account.ID, "FAAS_MANAGED_POSTGRES_QUALIFY_APP_ID": app.ID,
		"FAAS_MANAGED_POSTGRES_QUALIFY_AGE_IDENTITY_FILE": identityPath, "FAAS_MANAGED_POSTGRES_QUALIFY_HMAC_KEY_FILE": hmacPath, "FAAS_MANAGED_POSTGRES_QUALIFY_CATALOG_URL": catalogURL.String(),
	}
	getenv := func(key string) string { return values[key] }
	spec := managedpostgres.Spec{Region: "us-east-1", PostgresMajor: 16, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30}
	if _, err := loadDurableQualification(ctx, getenv, spec, time.Minute); err != nil {
		t.Fatalf("fresh owned fixture: %v", err)
	}
	foreign, err := store.CreateAccount(ctx, uuid.NewString()+"@foreign.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	values["FAAS_MANAGED_POSTGRES_QUALIFY_ACCOUNT_ID"] = foreign.ID
	if _, err := loadDurableQualification(ctx, getenv, spec, time.Minute); err == nil {
		t.Fatal("accepted foreign app")
	}
	values["FAAS_MANAGED_POSTGRES_QUALIFY_ACCOUNT_ID"] = account.ID
	if err := store.UpsertAppSecret(ctx, account.ID, app.ID, "DATABASE_URL", []byte("fixture ciphertext")); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDurableQualification(ctx, getenv, spec, time.Minute); err == nil {
		t.Fatal("adopted existing app secret")
	}
}

func TestDurableQualificationPrivateFiles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "key")
	if err := os.WriteFile(file, []byte("private-key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readQualificationSecret(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readQualificationSecret(file); err == nil {
		t.Fatal("accepted world-readable key")
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readQualificationSecret(link); err == nil {
		t.Fatal("accepted symlink key")
	}
}
