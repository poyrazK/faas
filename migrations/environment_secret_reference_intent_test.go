//go:build !no_pg

package migrations_test

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func environmentGitOpsReplayVersions() []int64 {
	return []int64{
		20260930183000001, 20260930183000002, 20260930183000003, 20260930183000004, 20260930183000005, 20260930183000006,
		20260930193000001, 20260930220000001,
		20261001010000001, 20261001020000001, 20261001020000002, 20261001030000001, 20261001040000001, 20261001050000001,
		20261001060000001, 20261001070000001, 20261001070000002, 20261001080000001, 20261001080000002, 20261001080000003, 20261001081007501,
		20261001094704872, 20261001110831601, 20261001120000001, 20261001142049282, 20261001143949543,
		20261001150000001, 20261001160000001, 20261001164005579, 20261001181539580, 20261001193719615, 20261001214705000, 20261001225012000, 20261001234301000, 20261002020803000, 20261002061753000, 20261002074620000,
	}
}

func TestEnvironmentSecretReferenceIntentGuardsAndPopulatedReplay(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "secret-reference-replay@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "reference-shop", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "reference-api", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"production", "staging"} {
		for _, name := range []string{"DATABASE_A", "DATABASE_B"} {
			if err := store.UpsertAppSecretInScope(ctx, account.ID, app.ID, scope, name, []byte(scope+"-sealed-"+name)); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.PutAppEnvironmentSecretReference(ctx, account.ID, app.ID, scope, "DATABASE_URL", "secret:DATABASE_A"); err != nil {
			t.Fatal(err)
		}
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug, SecretRefs: map[string]string{"DATABASE_URL": "secret:DATABASE_B"}}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, state.ApproveEnvironmentRevision{AccountID: account.ID, SourceID: source.ID, ExpectedGeneration: source.Generation, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewEnvironmentGitOpsAdoption(ctx, account.ID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(ctx, account.ID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(ctx, "reference-replay-worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.ObserveEnvironmentGitOps(ctx, lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: source.Generation, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOpsWithEffects(ctx, lease, plan, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppEnvironmentSecretReference(ctx, account.ID, app.ID, "production", "REMOVED"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`update app_environment_secret_refs set secret_name='DATABASE_A' where app_id=$1 and scope='production'`,
		`delete from app_environment_secret_refs where app_id=$1 and scope='production'`,
		`update app_environment_secret_refs set key='ANOTHER' where app_id=$1 and scope='staging'`,
		`update app_environment_secret_refs set scope='production' where app_id=$1 and scope='staging'`,
		`insert into app_envs(account_id,app_id,scope,key,value) select account_id,id,'production','DATABASE_URL','shadow' from apps where id=$1`,
		`insert into app_environment_secret_ref_suppressions(account_id,project_id,environment_id,app_id,scope,key) select r.account_id,r.project_id,r.environment_id,r.app_id,r.scope,r.key from app_environment_secret_refs r where r.app_id=$1 and r.scope='production'`,
		`insert into app_environment_secret_ref_suppressions(account_id,project_id,environment_id,app_id,scope,key) select r.account_id,r.project_id,r.environment_id,r.app_id,r.scope,r.key from app_environment_secret_refs r where r.app_id=$1 and r.scope='staging'`,
		`update app_environment_secret_ref_suppressions set key='MOVED' where app_id=$1 and scope='production'`,
	} {
		if _, err := pool.Exec(ctx, statement, app.ID); err == nil {
			t.Fatalf("raw SQL bypassed reference guard: %s", statement)
		}
	}
	other, err := store.CreateAccount(ctx, "reference-other@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_environment_secret_refs(account_id,project_id,environment_id,app_id,scope,key,secret_name) values($1,$2,$3,$4,'staging','CROSS_ACCOUNT','DATABASE_B')`, other.ID, project.ID, staging.ID, app.ID); err == nil {
		t.Fatal("cross-account reference tuple accepted")
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64), Status: state.DeployLive, Scope: "production", OverrideEnvSecrets: json.RawMessage(`{"DATABASE_URL":"secret:DATABASE_B"}`)})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "reference-replay-node", Active: true, TargetURL: "tcp://127.0.0.1:50051", AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 512, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListAppSecretsInScope(ctx, account.ID, app.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]int64{}
	for _, row := range rows {
		if row.Key == "DATABASE_B" {
			versions["production/"+row.Key] = row.DeliveryVersion
		}
	}
	inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC(), Variables: map[string]string{}, SecretVersions: versions, SecretRefs: map[string]string{"DATABASE_URL": "secret:DATABASE_B"}}
	if err := store.RecordInstanceRuntimeConfigReceipt(ctx, instance.ID, instance.WakeID, inputs); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.PublishSnapshotIfRuntimeFresh(ctx, state.Snapshot{DeploymentID: deployment.ID, FCVersion: "1.13.0", StorageKey: state.SnapMemKey(deployment.ID)}, instance.ID, instance.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	// Replay the complete additive set over actual intent, ownership, receipts,
	// runtime effects and an active controller lease, not just empty DDL.
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=any($1::bigint[])`, environmentGitOpsReplayVersions()); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	refs, err := store.AppEnvironmentSecretReferences(ctx, account.ID, app.ID, "production")
	if err != nil || !maps.Equal(refs, inputs.SecretRefs) {
		t.Fatalf("replay changed reference intent: %+v %v", refs, err)
	}
	intent, err := store.AppEnvironmentSecretIntent(ctx, account.ID, app.ID, "production")
	if err != nil || !slices.Equal(intent.SuppressedKeys, []string{"REMOVED"}) {
		t.Fatalf("replay erased suppression: %+v %v", intent, err)
	}
	for _, read := range []func() (state.RuntimeConfigInputs, bool, error){
		func() (state.RuntimeConfigInputs, bool, error) {
			return store.InstanceRuntimeConfigReceipt(ctx, instance.ID)
		},
		func() (state.RuntimeConfigInputs, bool, error) {
			return store.SnapshotRuntimeConfigReceipt(ctx, snapshot.ID)
		},
	} {
		receipt, exists, err := read()
		if err != nil || !exists || !maps.Equal(receipt.SecretRefs, inputs.SecretRefs) {
			t.Fatalf("replay erased mapping evidence: %+v %v %v", receipt, exists, err)
		}
		if fresh, err := store.RuntimeConfigInputsFresh(ctx, app.ID, receipt); err != nil || !fresh {
			t.Fatalf("replay invalidated current receipt: %v %v", fresh, err)
		}
	}
	if err := store.PutAppEnvironmentSecretReference(ctx, account.ID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_A"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
		t.Fatalf("replay released ownership: %v", err)
	}
	if err := store.RenewEnvironmentGitOps(ctx, lease, time.Now(), time.Minute); err != nil {
		t.Fatalf("replay replaced controller lease: %v", err)
	}
	var qualified bool
	if err := pool.QueryRow(ctx, `select environment_runtime_inputs_fresh($1,'production',clock_timestamp(),'{}',$2,false)`, app.ID, []byte(`{"production/DATABASE_A":1,"production/DATABASE_B":1}`)).Scan(&qualified); err != nil || qualified {
		t.Fatalf("old receipt reader qualified managed references: %v %v", qualified, err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	refs, err = store.AppEnvironmentSecretReferences(ctx, account.ID, app.ID, "staging")
	if err != nil || len(refs) != 0 {
		t.Fatalf("catalog replacement adopted old references: %+v %v", refs, err)
	}
}
