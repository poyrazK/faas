package apidserver_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type gitOpsControlFixture struct {
	Store    state.Store
	Pool     *pgxpool.Pool
	Server   *apidserver.Server
	Notifier *apidserver.CaptureNotifier
	Lease    state.EnvironmentGitOpsLease
	Desired  environmentsync.DesiredState
	App      state.App
}

func requireGitOpsControl(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f gitOpsControlFixture) plan(t *testing.T, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) environmentsync.Plan {
	t.Helper()
	observed, err := f.Store.(state.EnvironmentGitOpsIntentStore).ObserveEnvironmentGitOps(t.Context(), lease, desired)
	requireGitOpsControl(t, err)
	plan, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{
		Manager: lease.Source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA,
		Generation: lease.Source.Generation, Prune: lease.Source.Spec.Prune, Now: time.Now(), Overrides: observed.Overrides})
	requireGitOpsControl(t, err)
	if !plan.CanApply() {
		t.Fatalf("blocked fixture plan: %+v", plan)
	}
	return plan
}

func (f gitOpsControlFixture) backend() environmentgitops.Backend {
	return f.Server.EnvironmentGitOpsBackendForTest(f.Store.(state.EnvironmentGitOpsIntentStore), f.Store.(state.EnvironmentGitOpsEffectStore))
}

func (f gitOpsControlFixture) pause(t *testing.T, paused bool) {
	t.Helper()
	raw, err := json.Marshal(paused)
	requireGitOpsControl(t, err)
	requireGitOpsControl(t, (*f.Server.RuntimeConfig()).Apply(apidserver.RuntimeConfigPlatformReleasePause, raw))
}

func (f gitOpsControlFixture) approve(t *testing.T, desired environmentsync.DesiredState) state.EnvironmentGitSource {
	t.Helper()
	source, err := f.Store.(state.EnvironmentGitOpsStore).EnvironmentGitSource(t.Context(), f.App.AccountID, f.App.ProjectID, "production")
	requireGitOpsControl(t, err)
	source, _, err = f.Store.(state.EnvironmentGitOpsStore).ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{
		AccountID: source.AccountID, SourceID: source.ID, ExpectedGeneration: source.Generation,
		CommitSHA: strings.ReplaceAll(uuid.NewString(), "-", "") + "aaaaaaaa", Desired: desired, ApprovedBy: source.AccountID})
	requireGitOpsControl(t, err)
	return source
}

func newGitOpsControlFixture(t *testing.T, store state.Store) gitOpsControlFixture {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), "gitops-control-"+uuid.NewString()+"@example.test", api.PlanPro)
	requireGitOpsControl(t, err)
	repository := "example/review-" + uuid.NewString()[:8]
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "shop",
		RepoFullName: repository, ProductionBranch: "main", InstallID: 42})
	requireGitOpsControl(t, err)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID,
		Slug: "control-api-" + uuid.NewString()[:8], WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 1})
	requireGitOpsControl(t, err)
	requireGitOpsControl(t, store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "old"))
	_, err = store.(state.EnvironmentGitOpsStore).CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: repository, Ref: "refs/heads/main", ManifestPath: "environments/production.yaml",
		Mode: "enforce", ApprovalPolicy: "manual", Prune: true})
	requireGitOpsControl(t, err)
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug, Variables: map[string]string{"MODE": "new"}}}})
	requireGitOpsControl(t, err)
	notifier := &apidserver.CaptureNotifier{}
	f := gitOpsControlFixture{Store: store, App: app, Desired: desired, Notifier: notifier,
		Server: apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", notifier)}
	source := f.approve(t, desired)
	intent := store.(state.EnvironmentGitOpsIntentStore)
	adoption, err := intent.PreviewEnvironmentGitOpsAdoption(t.Context(), account.ID, source.ID)
	requireGitOpsControl(t, err)
	if !adoption.CanApply() {
		t.Fatal("fixture adoption blocked")
	}
	requireGitOpsControl(t, intent.AdoptEnvironmentGitOps(t.Context(), account.ID, source.ID, adoption.Hash))
	f.Lease, err = store.(state.EnvironmentGitOpsStore).ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
	requireGitOpsControl(t, err)
	_, err = store.(state.EnvironmentGitOpsEffectStore).ApplyEnvironmentGitOpsWithEffects(t.Context(), f.Lease, f.plan(t, f.Lease, desired), nil)
	requireGitOpsControl(t, err)
	return f
}

func gitOpsControlStores(t *testing.T, run func(*testing.T, gitOpsControlFixture)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { run(t, newGitOpsControlFixture(t, state.NewMemStore())) })
	t.Run("postgres", func(t *testing.T) {
		if os.Getenv("GREGALE_GITOPS_ACCEPTANCE") == "1" && (os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "") {
			t.Fatal("GitOps acceptance requires enabled PostgreSQL tests")
		}
		pool := pgtest.OpenMigrated(t)
		fixture := newGitOpsControlFixture(t, state.NewPgStore(pool))
		fixture.Pool = pool
		run(t, fixture)
	})
}

func TestGitOpsPauseHoldsEmptyRetirementAndAllowsExistingCleanup(t *testing.T) {
	for _, inProgress := range []bool{false, true} {
		name := "first-cutover"
		if inProgress {
			name = "already-started"
		}
		t.Run(name, func(t *testing.T) {
			gitOpsControlStores(t, func(t *testing.T, f gitOpsControlFixture) {
				f.pause(t, true)
				definition := f.Desired.Definition
				definition.Workloads = map[string]api.EnvironmentWorkload{}
				definition.WorkloadPruningPolicy = "retain"
				desired, err := environmentsync.Compile(definition)
				requireGitOpsControl(t, err)
				source := f.approve(t, desired)
				lease, err := f.Store.(state.EnvironmentGitOpsStore).ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
				requireGitOpsControl(t, err)
				backend := f.backend()
				steps, err := backend.Apply(t.Context(), lease, f.plan(t, lease, desired))
				requireGitOpsControl(t, err)
				if len(steps) != 1 || steps[0].Status != "planned" {
					t.Fatalf("retirement reservation: %+v", steps)
				}
				retirement := f.Store.(state.EnvironmentGitOpsWorkloadRetirementStore)
				var releaseID string
				if inProgress {
					f.pause(t, false)
					ready, err := retirement.ActivateEnvironmentGitOpsEmptyRetirement(t.Context(), lease, f.plan(t, lease, desired))
					requireGitOpsControl(t, err)
					if !ready {
						t.Fatal("fixture cutover did not commit")
					}
					release, err := f.Store.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), source.AccountID, source.ProjectID, "production")
					requireGitOpsControl(t, err)
					releaseID = release.ID
					f.pause(t, true)
				}
				for range 2 {
					ready, err := backend.(environmentgitops.RuntimeVerifier).VerifyRuntime(t.Context(), lease, f.plan(t, lease, desired))
					requireGitOpsControl(t, err)
					if ready {
						t.Fatal("paused environment reported convergence")
					}
				}
				rows, err := retirement.ListEnvironmentGitOpsWorkloadRetirements(t.Context(), source.AccountID, source.ID)
				requireGitOpsControl(t, err)
				want := "planned"
				if inProgress {
					want = "retired"
				}
				if len(rows) != 1 || rows[0].Phase != want {
					t.Fatalf("paused retirement phase: %+v, want %s", rows, want)
				}
				blocked, err := f.Store.(state.EnvironmentWorkloadRetirementAdmissionStore).EnvironmentWorkloadAdmissionRetired(t.Context(), f.App.ID, "production")
				requireGitOpsControl(t, err)
				if blocked != inProgress {
					t.Fatal("pause changed admission before first cutover")
				}
				release, err := f.Store.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), source.AccountID, source.ProjectID, "production")
				if !inProgress && !errors.Is(err, state.ErrNotFound) || inProgress && (err != nil || release.ID != releaseID) {
					t.Fatalf("paused verification published a release: %s %v", release.ID, err)
				}
				f.pause(t, false)
				ready, err := backend.(environmentgitops.RuntimeVerifier).VerifyRuntime(t.Context(), lease, f.plan(t, lease, desired))
				requireGitOpsControl(t, err)
				if !ready {
					t.Fatal("retirement did not resume after the pause")
				}
			})
		})
	}
}

func TestGitOpsReportFinalizesFreshEffectsBeforePlanningAndDetach(t *testing.T) {
	for _, scenario := range []string{"matching", "changed", "removed", "stale-resident"} {
		t.Run(scenario, func(t *testing.T) {
			gitOpsControlStores(t, func(t *testing.T, f gitOpsControlFixture) {
				var instance state.Instance
				if scenario == "stale-resident" {
					node, err := f.Store.CreateComputeNode(t.Context(), state.ComputeNode{Name: uuid.NewString(), Active: true,
						TargetURL: "tcp://127.0.0.1:50051", AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5})
					requireGitOpsControl(t, err)
					deployment, err := f.Store.CreateDeployment(t.Context(), state.Deployment{AppID: f.App.ID, Scope: "production",
						Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("c", 64), Status: state.DeployLive})
					requireGitOpsControl(t, err)
					instance, err = f.Store.CreateInstanceWithMode(t.Context(), f.App.ID, deployment.ID, string(state.StateWaking), f.App.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
					requireGitOpsControl(t, err)
					requireGitOpsControl(t, f.Store.UpdateInstanceState(t.Context(), instance.ID, string(state.StateRunning)))
				} else if scenario != "matching" {
					definition := f.Desired.Definition
					if scenario == "changed" {
						definition.Workloads["api"] = api.EnvironmentWorkload{App: f.App.Slug, Variables: map[string]string{"MODE": "not-applied"}}
					} else {
						definition.Workloads = map[string]api.EnvironmentWorkload{}
						definition.WorkloadPruningPolicy = "retain"
					}
					desired, err := environmentsync.Compile(definition)
					requireGitOpsControl(t, err)
					f.approve(t, desired)
				}
				runtime := f.Store.(state.EnvironmentGitOpsRuntimeStore)
				source, err := f.Store.(state.EnvironmentGitOpsStore).EnvironmentGitSource(t.Context(), f.App.AccountID, f.App.ProjectID, "production")
				requireGitOpsControl(t, err)
				source, err = f.Store.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID,
					state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: "report"})
				requireGitOpsControl(t, err)
				worker := environmentgitops.Worker{Store: f.Store.(state.EnvironmentGitOpsStore), Backend: f.backend(), Mode: "report",
					LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
				worked, err := worker.RunOnce(t.Context())
				requireGitOpsControl(t, err)
				if !worked {
					t.Fatal("report worker did not claim its source")
				}
				runs, err := f.Store.(state.EnvironmentGitOpsStore).ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
				requireGitOpsControl(t, err)
				want := "drifted"
				if scenario == "matching" {
					want = "converged"
				}
				if len(runs) != 1 || runs[0].Status != want {
					t.Fatalf("report status: %+v, want %s", runs, want)
				}
				values, err := f.Store.ListAppEnvInScope(t.Context(), f.App.AccountID, f.App.ID, "production")
				requireGitOpsControl(t, err)
				if len(values) != 1 || values[0].Value != "new" {
					t.Fatal("report changed customer intent", values)
				}
				lifecycle := f.Store.(state.EnvironmentGitOpsLifecycleStore)
				detachErr := lifecycle.DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation)
				if scenario == "stale-resident" {
					if !errors.Is(detachErr, state.ErrConflict) {
						t.Fatal("stale runtime lost its detach fence", detachErr)
					}
					lease, err := f.Store.(state.EnvironmentGitOpsStore).ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now().Add(2*time.Minute), time.Minute)
					requireGitOpsControl(t, err)
					before, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
					requireGitOpsControl(t, err)
					if len(before) != 1 || before[0].RequestedAt != nil {
						t.Fatal("report refreshed a stale resident", before)
					}
					observer := f.backend().(environmentgitops.RuntimeEffectObserver)
					requireGitOpsControl(t, observer.ObserveRuntimeEffects(t.Context(), lease))
					after, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
					requireGitOpsControl(t, err)
					if !reflect.DeepEqual(before, after) {
						t.Fatal("repeated report observation changed pending work")
					}
					requireGitOpsControl(t, f.Store.UpdateInstanceStateToTerminal(t.Context(), instance.ID, string(state.StateStopped), time.Now()))
					requireGitOpsControl(t, observer.ObserveRuntimeEffects(t.Context(), lease))
					detachErr = lifecycle.DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation)
				}
				requireGitOpsControl(t, detachErr)
				if len(f.Notifier.ByChannel(db.NotifyRuntimeConfigRestart)) != 0 {
					t.Fatal("report requested a runtime refresh")
				}
				if f.Pool != nil {
					var notifications int
					requireGitOpsControl(t, f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM notification_outbox WHERE channel=$1`, db.NotifyRuntimeConfigRestart).Scan(&notifications))
					if notifications != 0 {
						t.Fatal("report enqueued a durable runtime refresh")
					}
				}
			})
		})
	}
}
