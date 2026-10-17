package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/state"
)

type deactivationFixture struct {
	store    state.Store
	account  state.Account
	project  state.Project
	apps     []state.App
	fallback []state.ProjectReleaseMember
	release  state.ProjectReleaseSet
}

func newDeactivationFixture(t *testing.T, store state.Store) deactivationFixture {
	t.Helper()
	ctx := t.Context()
	f := deactivationFixture{store: store}
	var err error
	f.account, err = store.CreateAccount(ctx, uuid.NewString()+"@deactivate.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.project, err = store.CreateProject(ctx, state.Project{AccountID: f.account.ID, Slug: "deactivate-project"})
	if err != nil {
		t.Fatal(err)
	}
	var graph []state.ProjectReleaseMember
	for _, name := range []string{"deactivate-api", "deactivate-worker"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: name, WorkloadName: name, Type: state.AppTypeApp, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}, RAMMB: 128, MaxConcurrency: 5})
		if err != nil {
			t.Fatal(err)
		}
		f.apps = append(f.apps, app)
		for _, dark := range []bool{false, true} {
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:" + uuid.NewString(), TrafficPercentExplicit: dark})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/"+dep.ID, 4096); err != nil {
				t.Fatal(err)
			}
			if dark {
				err = store.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(ctx, dep.ID)
			} else {
				err = store.MarkDeploymentLive(ctx, dep.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			member := state.ProjectReleaseMember{AppID: app.ID, DeploymentID: dep.ID}
			if dark {
				graph = append(graph, member)
			} else {
				f.fallback = append(f.fallback, member)
			}
		}
	}
	f.release, err = store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production", 1800, graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range f.apps {
		zero := int64(0)
		if _, err := store.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, f.account.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f deactivationFixture) fences(ctx context.Context, t *testing.T, selected []state.ProjectReleaseMember) []state.BindingPromotionFence {
	t.Helper()
	var fences []state.BindingPromotionFence
	for _, member := range selected {
		revision, err := f.store.(state.BindingPromotionStore).ReadBindingPromotionRevision(ctx, f.account.ID, member.AppID)
		if err != nil {
			t.Fatal(err)
		}
		policy, err := f.store.(state.BindingReleasePolicyStore).GetBindingReleasePolicy(ctx, f.account.ID, member.AppID, "production")
		if err != nil {
			t.Fatal(err)
		}
		age, err := time.ParseDuration(policy.MaxVerificationAge)
		if err != nil {
			t.Fatal(err)
		}
		fences = append(fences, state.BindingPromotionFence{AccountID: f.account.ID, AppID: member.AppID, DeploymentID: member.DeploymentID, Scope: "production", Revision: revision, PolicyRevision: policy.Revision, MaxVerificationAge: age, ValidUntil: time.Now().Add(time.Minute)})
	}
	return fences
}
func TestCheckedProjectDeactivationMem(t *testing.T) {
	checkedProjectDeactivation(t, state.NewMemStore())
}
func TestCheckedProjectDeactivationPG(t *testing.T) {
	store, _ := pgStore(t)
	checkedProjectDeactivation(t, store)
}
func checkedProjectDeactivation(t *testing.T, store state.Store) {
	t.Helper()
	f := newDeactivationFixture(t, store)
	ctx := t.Context()
	checked := store.(state.CheckedProjectReleaseStore)
	reader := store.(state.ProjectReleaseSetReader)
	if err := store.(state.ProjectReleaseSetPromotionStore).DeactivateProjectReleaseSetIfActive(ctx, f.account.ID, f.project.ID, "production", f.release.ID, f.fallback); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("legacy bypass=%v", err)
	}
	for _, scenario := range []string{"missing fallback", "wrong fallback", "missing fence", "expired proof", "stale active", "policy changed", "wrong account"} {
		t.Run(scenario, func(t *testing.T) {
			members := append([]state.ProjectReleaseMember(nil), f.fallback...)
			fences := f.fences(ctx, t, members)
			active, account := f.release.ID, f.account.ID
			switch scenario {
			case "missing fallback":
				members = members[:1]
				fences = fences[:1]
			case "wrong fallback":
				members[0] = f.release.Members[0]
				fences = f.fences(ctx, t, members)
			case "missing fence":
				fences = fences[:1]
			case "expired proof":
				fences[0].ValidUntil = time.Now().Add(-time.Second)
			case "stale active":
				active = uuid.NewString()
			case "wrong account":
				account = uuid.NewString()
			case "policy changed":
				revision := fences[0].PolicyRevision
				if _, err := store.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, f.account.ID, members[0].AppID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &revision, MaxVerificationAge: "30s"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := checked.DeactivateProjectReleaseWithBindings(ctx, account, f.project.ID, "production", active, members, fences); err == nil {
				t.Fatal("invalid deactivation accepted")
			}
			current, err := reader.ActiveProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production")
			if err != nil || current.ID != f.release.ID || current.Deactivation != nil {
				t.Fatalf("rejected deactivation changed graph: %+v %v", current, err)
			}
		})
	}
	// Fallbacks already serve traffic; no new deployment-retention setting is required.
	for _, app := range f.apps {
		manifest := app.Manifest
		manifest.RevisionPinTTLSeconds = 0
		if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
			t.Fatal(err)
		}
	}
	manifest, problem := bindings.PrepareProjectDeactivation(ctx, store.(bindings.ProjectReleaseCatalog), f.account.ID, f.project, "production")
	if problem != nil || !manifest.Deactivate || manifest.TTLSeconds != 0 || *manifest.ExpectedActiveReleaseID != f.release.ID {
		t.Fatalf("prepare=%+v problem=%+v", manifest, problem)
	}
	if _, problem := bindings.ValidateCheckedProjectRelease(manifest, f.project.Slug, "production", f.apps); problem != nil {
		t.Fatal(problem)
	}
	started := time.Now()
	retired, err := checked.DeactivateProjectReleaseWithBindings(ctx, f.account.ID, f.project.ID, "production", f.release.ID, f.fallback, f.fences(ctx, t, f.fallback))
	if err != nil {
		t.Fatal(err)
	}
	if retired.Active || retired.ExpiresAt == nil || retired.Deactivation == nil || retired.Deactivation.ReleaseID != f.release.ID || len(retired.Deactivation.FallbackMembers) != 2 || retired.ExpiresAt.Before(started.Add(1799*time.Second)) {
		t.Fatalf("retired=%+v", retired)
	}
	if _, err := reader.ActiveProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("graph still active=%v", err)
	}
	persisted, err := reader.ProjectReleaseSetByID(ctx, f.account.ID, f.project.ID, "production", f.release.ID)
	if err != nil || persisted.Deactivation == nil || persisted.Deactivation.ID != retired.Deactivation.ID {
		t.Fatalf("missing durable receipt: %+v %v", persisted, err)
	}
	for _, app := range f.apps {
		id, _, err := store.(state.ProjectReleaseSetStore).ResolveProjectRelease(ctx, app.ID, "production", "")
		if err != nil || id != "" {
			t.Fatalf("default graph route persists: %s %v", id, err)
		}
		if _, _, err := store.(state.ProjectReleaseSetStore).ResolveProjectRelease(ctx, app.ID, "production", f.release.ID); err != nil {
			t.Fatalf("retired sticky graph lost: %v", err)
		}
	}
	if _, err := checked.DeactivateProjectReleaseWithBindings(ctx, f.account.ID, f.project.ID, "production", f.release.ID, f.fallback, f.fences(ctx, t, f.fallback)); err == nil {
		t.Fatal("stale deactivation replay accepted")
	}
	// Returned receipt must not expose mutable MemStore-owned slices.
	retired.Deactivation.FallbackMembers[0].DeploymentID = uuid.NewString()
	again, err := reader.ProjectReleaseSetByID(ctx, f.account.ID, f.project.ID, "production", f.release.ID)
	a, _ := json.Marshal(persisted.Deactivation)
	b, _ := json.Marshal(again.Deactivation)
	if err != nil || string(a) != string(b) {
		t.Fatal("receipt mutated through returned value")
	}
}

type deactivationObservationCatalog struct {
	*state.MemStore
	mode string
}

func (c deactivationObservationCatalog) LiveDeployments(ctx context.Context, appID string) ([]state.Deployment, error) {
	deps, err := c.MemStore.LiveDeployments(ctx, appID)
	if err != nil {
		return nil, err
	}
	for i := range deps {
		switch c.mode {
		case "missing":
			deps[i].TrafficPercent = 0
		case "split":
			deps[i].TrafficPercent = 50
		}
	}
	return deps, nil
}
func TestPrepareProjectDeactivationRejectsMissingAndSplitFallback(t *testing.T) {
	store := state.NewMemStore()
	f := newDeactivationFixture(t, store)
	for _, mode := range []string{"missing", "split"} {
		t.Run(mode, func(t *testing.T) {
			catalog := deactivationObservationCatalog{MemStore: store, mode: mode}
			if _, problem := bindings.PrepareProjectDeactivation(t.Context(), catalog, f.account.ID, f.project, "production"); problem == nil {
				t.Fatal("ambiguous fallback accepted")
			}
		})
	}
}
func TestCheckedDeactivationServiceReadinessMem(t *testing.T) {
	checkedDeactivationServiceReadiness(t, state.NewMemStore(), state.DefaultLocalNodeName)
}
func TestCheckedDeactivationServiceReadinessPG(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	checkedDeactivationServiceReadiness(t, store, resolveDefaultLocal(t, ctx, store))
}
func checkedDeactivationServiceReadiness(t *testing.T, store state.Store, node string) {
	t.Helper()
	f := newDeactivationFixture(t, store)
	ctx := t.Context()
	app := f.apps[0]
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Desired: 2, Max: 2}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	write := func() error {
		_, err := store.(state.CheckedProjectReleaseStore).DeactivateProjectReleaseWithBindings(ctx, f.account.ID, f.project.ID, "production", f.release.ID, f.fallback, f.fences(ctx, t, f.fallback))
		return err
	}
	for replicas := 0; replicas < 2; replicas++ {
		if err := write(); err == nil {
			t.Fatal("partially ready fallback accepted")
		}
		if _, err := store.CreateInstanceWithMode(ctx, app.ID, f.fallback[0].DeploymentID, "running", 128, node, uuid.NewString(), "service"); err != nil {
			t.Fatal(err)
		}
	}
	if err := write(); err != nil {
		t.Fatalf("ready fallback blocked: %v", err)
	}
}
