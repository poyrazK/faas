package apidserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func loadTestManifest(t *testing.T, body string) *gregalemanifest.Manifest {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := gregalemanifest.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	return manifest
}

// ADR-20261106105015 §1: a gregale.yaml database with `create` is planned,
// created at apply and bound; a later deploy binds the database it made.
func TestManifestDatabaseCreateIsPlannedCreatedAndBound(t *testing.T) {
	env := newSourceRefTestServer(t, api.PlanPro, "gateway", 9106)
	ctx := t.Context()
	acct, err := env.store.AccountByID(ctx, env.acctID)
	if err != nil {
		t.Fatal(err)
	}
	attachTestManagedPostgres(t, env.srv, 3)
	app, err := env.store.AppBySlug(ctx, "gateway")
	if err != nil {
		t.Fatal(err)
	}
	manifest := loadTestManifest(t, `databases:
  - database: chronolake-db
    app: gateway
    create:
      postgres_major: 17
  - database: chronolake-db
    app: gateway
    env: MIGRATION_DATABASE_URL
    access: migration
`)

	resolved, problem := env.srv.resolveManifestPostgresBindings(ctx, acct, manifest, []string{"gateway"}, "")
	if problem != nil {
		t.Fatalf("resolve: %+v", problem)
	}
	if len(resolved) != 2 || resolved[0].create == nil || resolved[0].create != resolved[1].create || resolved[0].create.spec.PostgresMajor != 17 {
		t.Fatalf("resolved %+v, want both entries to share one creation on 17", resolved)
	}
	plan := manifestDatabasePlan(resolved)
	if len(plan) != 1 || plan[0].Source != "gregale.yaml" || plan[0].Provision == nil || len(plan[0].Provision.Bindings) != 2 ||
		plan[0].Provision.Bindings[1].EnvKey != "MIGRATION_DATABASE_URL" {
		t.Fatalf("plan %+v", plan)
	}

	if _, problem := env.srv.bindResolvedManagedPostgresBindings(ctx, acct, resolved, []state.App{app}); problem != nil {
		t.Fatalf("bind: %+v", problem)
	}
	databases, err := env.srv.managedPostgres.List(ctx, acct.ID)
	if err != nil || len(databases) != 1 || databases[0].Name != "chronolake-db" || databases[0].Spec.PostgresMajor != 17 {
		t.Fatalf("databases %+v err %v", databases, err)
	}

	again, problem := env.srv.resolveManifestPostgresBindings(ctx, acct, manifest, []string{"gateway"}, "")
	if problem != nil {
		t.Fatalf("second resolve: %+v", problem)
	}
	if again[0].create != nil || again[0].database.ID != databases[0].ID {
		t.Fatalf("second deploy should bind the existing database, got %+v", again[0])
	}
}

func TestManifestDatabaseWithoutCreateNamesTheFix(t *testing.T) {
	env := newSourceRefTestServer(t, api.PlanPro, "gateway", 9107)
	ctx := t.Context()
	acct, err := env.store.AccountByID(ctx, env.acctID)
	if err != nil {
		t.Fatal(err)
	}
	attachTestManagedPostgres(t, env.srv, 3)
	manifest := loadTestManifest(t, "databases:\n  - database: missing-db\n")
	_, problem := env.srv.resolveManifestPostgresBindings(ctx, acct, manifest, []string{"gateway"}, "")
	if problem == nil || problem.Code != "managed_postgres_not_found" || !strings.Contains(problem.Detail, "create: true") {
		t.Fatalf("problem %+v, want not found with a create hint", problem)
	}
}

func TestManifestDatabaseCreateRespectsPlanLimit(t *testing.T) {
	env := newSourceRefTestServer(t, api.PlanPro, "gateway", 9108)
	ctx := t.Context()
	acct, err := env.store.AccountByID(ctx, env.acctID)
	if err != nil {
		t.Fatal(err)
	}
	attachTestManagedPostgres(t, env.srv, 1)
	manifest := loadTestManifest(t, `databases:
  - database: first-db
    app: gateway
    create: true
  - database: second-db
    app: gateway
    env: SECOND_DATABASE_URL
    create: true
`)
	_, problem := env.srv.resolveManifestPostgresBindings(ctx, acct, manifest, []string{"gateway"}, "")
	if problem == nil || problem.Code != "managed_postgres_quota_exceeded" {
		t.Fatalf("problem %+v, want quota before any creation", problem)
	}
	if databases, _ := env.srv.managedPostgres.List(ctx, acct.ID); len(databases) != 0 {
		t.Fatalf("created %d databases during planning", len(databases))
	}
}

func TestManifestBucketPlanShowsCreateAndBind(t *testing.T) {
	plan := manifestBucketPlan([]resolvedManifestBucket{
		{appSlug: "store", bucket: objectManifestBucket("segments", true)},
		{appSlug: "store", bucket: objectManifestBucket("exports", false)},
	})
	if len(plan) != 2 || plan[0].Kind != "bucket" || plan[0].Provision.Existing || !plan[1].Provision.Existing ||
		plan[0].Provision.Bindings[0].Workload != "store" || plan[0].Provision.Bindings[0].EnvKey != "GREGALE_S3_SEGMENTS" {
		t.Fatalf("plan %+v", plan)
	}
}

func objectManifestBucket(name string, create bool) object.ManifestBucket {
	return object.ManifestBucket{Name: name, Scope: api.DefaultEnvScope, Prefix: api.DefaultObjectStorageBindingPrefix(name),
		Permission: api.ObjectBucketPermissionReadWrite, Create: create}
}
