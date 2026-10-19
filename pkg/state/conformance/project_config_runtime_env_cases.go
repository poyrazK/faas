package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectConfigFixture is a project with a staging environment and one
// member app (ADR-1136).
type projectConfigFixture struct {
	project state.Project
	app     state.App
}

func newProjectConfigFixture(t *testing.T, fx *Fixture) projectConfigFixture {
	t.Helper()
	project, err := fx.Store.CreateProject(fx.Ctx, state.Project{
		AccountID: fx.Account.ID, Slug: "config-" + uuid.NewString()[:8],
		ProductionBranch: "main", ScanSource: state.ProjectScanSourceConvention,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A named scope resolves to an environment created before the deployment.
	if _, err := fx.Store.CreateProjectEnvironment(fx.Ctx, state.ProjectEnvironment{
		AccountID: fx.Account.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{
		AccountID: fx.Account.ID, ProjectID: project.ID,
		Slug: "config-api-" + uuid.NewString()[:8], WorkloadName: "api", Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return projectConfigFixture{project: project, app: app}
}

func (p projectConfigFixture) setConfig(t *testing.T, fx *Fixture, environment, document string) {
	t.Helper()
	values, hash, err := api.NormalizeProjectEnvironmentConfig([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.CreateProjectEnvironmentConfigVersion(fx.Ctx, state.ProjectEnvironmentConfig{
		AccountID: fx.Account.ID, ProjectID: p.project.ID, EnvironmentSlug: environment, ConfigHash: hash, Values: values,
	}); err != nil {
		t.Fatalf("CreateProjectEnvironmentConfigVersion(%s): %v", environment, err)
	}
}

func (p projectConfigFixture) deploy(t *testing.T, fx *Fixture, scope string) state.Deployment {
	t.Helper()
	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: p.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + scope + uuid.NewString()[:8], Scope: scope,
	})
	if err != nil {
		t.Fatal(err)
	}
	return dep
}

func inheritedValues(t *testing.T, fx *Fixture, appID, deploymentID string) map[string]string {
	t.Helper()
	snapshot, err := fx.Store.RuntimeAppValuesForDeployment(fx.Ctx, fx.Account.ID, appID, deploymentID)
	if err != nil {
		t.Fatalf("RuntimeAppValuesForDeployment: %v", err)
	}
	got := map[string]string{}
	for i, row := range snapshot.Inherited {
		if i > 0 && snapshot.Inherited[i-1].Key >= row.Key {
			t.Fatalf("inherited rows are not sorted: %+v", snapshot.Inherited)
		}
		if row.AppID != appID || row.Scope != snapshot.Scope {
			t.Fatalf("inherited row owner = %+v, snapshot scope %q", row, snapshot.Scope)
		}
		got[row.Key] = row.Value
	}
	return got
}

func sameStrings(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

// ADR-1136: a deployment inherits the env-shaped scalar keys of its project
// environment's latest configuration, the default scope reads production, and
// the app's own variable in the same scope wins.
func testProjectConfigReachesRuntimeValues(t *testing.T, fx *Fixture) {
	p := newProjectConfigFixture(t, fx)
	if err := fx.Store.UpsertAppEnvInScope(fx.Ctx, fx.Account.ID, p.app.ID, "staging", "LOG_LEVEL", "debug"); err != nil {
		t.Fatal(err)
	}
	defaultDep, productionDep, stagingDep := p.deploy(t, fx, "default"), p.deploy(t, fx, "production"), p.deploy(t, fx, "staging")
	if got := inheritedValues(t, fx, p.app.ID, defaultDep.ID); len(got) != 0 {
		t.Fatalf("inherited before any configuration = %v", got)
	}
	p.setConfig(t, fx, "production", `{"LOG_LEVEL":"info","SHARED":"p","WORKERS":4,"region":"eu","LIMITS":{"cpu":2}}`)
	p.setConfig(t, fx, "staging", `{"LOG_LEVEL":"warn","SHARED":"s"}`)
	production := map[string]string{"LOG_LEVEL": "info", "SHARED": "p", "WORKERS": "4"}
	if got := inheritedValues(t, fx, p.app.ID, defaultDep.ID); !sameStrings(got, production) {
		t.Fatalf("default scope inherited %v, want production's %v", got, production)
	}
	if got := inheritedValues(t, fx, p.app.ID, productionDep.ID); !sameStrings(got, production) {
		t.Fatalf("production inherited %v, want %v", got, production)
	}
	staging := map[string]string{"SHARED": "s"}
	if got := inheritedValues(t, fx, p.app.ID, stagingDep.ID); !sameStrings(got, staging) {
		t.Fatalf("staging inherited %v, want %v (the app's own LOG_LEVEL wins)", got, staging)
	}
	if got := inheritedValues(t, fx, fx.App.ID, fx.Deployment.ID); len(got) != 0 {
		t.Fatalf("an app outside any project inherited %v", got)
	}

	fence := func() string {
		t.Helper()
		snapshot, err := fx.Store.RuntimeAppValuesForDeployment(fx.Ctx, fx.Account.ID, p.app.ID, stagingDep.ID)
		if err != nil {
			t.Fatal(err)
		}
		value, err := state.NewRuntimeAppConfigFence(snapshot)
		if err != nil {
			t.Fatalf("NewRuntimeAppConfigFence: %v", err)
		}
		return value.Fingerprint
	}
	before := fence()
	p.setConfig(t, fx, "staging", `{"LOG_LEVEL":"warn","SHARED":"s","note":"document only"}`)
	if after := fence(); after != before {
		t.Fatal("a document-only change moved the configuration fence")
	}
	p.setConfig(t, fx, "staging", `{"LOG_LEVEL":"warn","SHARED":"s2"}`)
	if after := fence(); after == before {
		t.Fatal("a delivered change kept the configuration fence")
	}
}

// ADR-1136: a configuration version that changes delivered variables stamps the
// member apps in the affected scopes and marks their older snapshots stale. A
// production change also covers the default scope; staging is untouched, and a
// document-only change invalidates nothing.
func testProjectConfigChangeInvalidatesMemberSnapshots(t *testing.T, fx *Fixture) {
	p := newProjectConfigFixture(t, fx)
	type capture struct {
		deployment state.Deployment
		instance   state.Instance
		snapshot   state.Snapshot
	}
	capturePublished := func(scope string) capture {
		t.Helper()
		dep := p.deploy(t, fx, scope)
		ins, err := fx.Store.CreateInstance(fx.Ctx, p.app.ID, dep.ID, string(state.StateRunning), 256, fx.Node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		snap, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, state.Snapshot{
			DeploymentID: dep.ID, Tier: state.SnapshotTierInit, FCVersion: "1.10.0",
			StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "initial"),
		}, ins.ID, ins.StartedAt)
		if err != nil {
			t.Fatal(err)
		}
		return capture{dep, ins, snap}
	}
	usable := func(c capture) bool {
		t.Helper()
		got, err := fx.Store.LatestSnapshot(fx.Ctx, c.deployment.ID)
		if errors.Is(err, state.ErrNotFound) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		return got.ID == c.snapshot.ID
	}
	defaultScope, staging := capturePublished("default"), capturePublished("staging")
	time.Sleep(2 * time.Millisecond)

	p.setConfig(t, fx, "production", `{"region":"eu"}`)
	if !usable(defaultScope) || !usable(staging) {
		t.Fatal("a document-only configuration invalidated snapshots")
	}
	p.setConfig(t, fx, "production", `{"region":"eu","LOG_LEVEL":"info"}`)
	if usable(defaultScope) {
		t.Fatal("a production change left the default-scope snapshot usable")
	}
	if !usable(staging) {
		t.Fatal("a production change invalidated the staging snapshot")
	}
	if _, stamped, err := state.RuntimeConfigChangedAtForScope(fx.Ctx, fx.Store, p.app.ID, "default"); err != nil || !stamped {
		t.Fatalf("default-scope boundary after a production change: stamped=%v err=%v", stamped, err)
	}
	// The stamp keeps an instance that booted with the old values from
	// publishing a fresh capture of them.
	if _, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, state.Snapshot{
		DeploymentID: defaultScope.deployment.ID, Tier: state.SnapshotTierWarm, FCVersion: "1.10.0",
		StorageKey: state.SnapshotCaptureMemKey(defaultScope.deployment.ID, state.SnapshotTierWarm, "stale"),
	}, defaultScope.instance.ID, defaultScope.instance.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
		t.Fatalf("capture from before the change = %v, want ErrSnapshotRuntimeStale", err)
	}

	time.Sleep(2 * time.Millisecond)
	p.setConfig(t, fx, "staging", `{"LOG_LEVEL":"warn"}`)
	if usable(staging) {
		t.Fatal("a staging change left the staging snapshot usable")
	}
}
