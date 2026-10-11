// adr: 590
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type invocationEnvironmentTestStore interface {
	state.Store
	state.ProjectReleaseSetStore
	ResolveInvocationPinScope(context.Context, string, string, string) (string, error)
}

type invocationEnvironmentFixture struct {
	account                         state.Account
	project                         state.Project
	app                             state.App
	production, stage               state.Deployment
	productionRelease, stageRelease state.ProjectReleaseSet
}

func TestMemInvocationEnvironmentRoutingContract(t *testing.T) {
	testInvocationEnvironmentRouting(t, state.NewMemStore())
}

func seedInvocationEnvironment(t *testing.T, store invocationEnvironmentTestStore) invocationEnvironmentFixture {
	t.Helper()
	ctx := t.Context()
	f := invocationEnvironmentFixture{}
	var err error
	f.account, err = store.CreateAccount(ctx, "invoke-stage-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.project, err = store.CreateProject(ctx, state.Project{AccountID: f.account.ID, Slug: "invoke-stage"})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"staging", "empty-stage"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: scope}); err != nil {
			t.Fatal(err)
		}
	}
	f.app, err = store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage-api",
		Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 4,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"production", "staging"} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + scope})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		release, err := store.PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, scope, 1800, []state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: dep.ID}})
		if err != nil {
			t.Fatal(err)
		}
		if scope == "production" {
			f.production, f.productionRelease = dep, release
		} else {
			f.stage, f.stageRelease = dep, release
		}
	}
	return f
}

func testInvocationEnvironmentRouting(t *testing.T, store invocationEnvironmentTestStore) invocationEnvironmentFixture {
	t.Helper()
	ctx := t.Context()
	f := seedInvocationEnvironment(t, store)
	request := state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/work", DueAt: time.Now()}
	prepared, version, err := state.ResolveInvocationVersionForEnvironment(ctx, store, request, "staging")
	if err != nil || version.Scope != "staging" || version.DeploymentID != f.stage.ID || version.ReleaseID != f.stageRelease.ID {
		t.Fatalf("stage admission = %+v, %v", version, err)
	}
	var headers map[string]string
	if err := json.Unmarshal(prepared.Headers, &headers); err != nil || headers[api.ReleaseHeader] != f.stageRelease.ID {
		t.Fatalf("durable stage pin = %s, %v", prepared.Headers, err)
	}
	queued, err := store.EnqueueInvocation(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: f.stage.ID}}); err != nil {
		t.Fatal(err)
	}
	stored, err := store.InvocationByID(ctx, queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, delivered, err := state.ResolveInvocationVersion(ctx, store, stored)
	if err != nil || delivered != version {
		t.Fatalf("retained stage delivery = %+v, %v; want %+v", delivered, err, version)
	}
	_, production, err := state.ResolveInvocationVersionForEnvironment(ctx, store, request, "")
	if err != nil || production.Scope != "production" || production.DeploymentID != f.production.ID {
		t.Fatalf("production changed = %+v, %v", production, err)
	}
	for _, pin := range []struct{ header, id string }{{api.ReleaseHeader, f.stageRelease.ID}, {api.RevisionHeader, f.stage.ID}} {
		pinned := request
		pinned.Headers, _ = json.Marshal(map[string]string{pin.header: pin.id})
		_, selected, err := state.ResolveInvocationVersion(ctx, store, pinned)
		if err != nil || selected.Scope != "staging" || selected.DeploymentID != f.stage.ID {
			t.Fatalf("%s stage delivery = %+v, %v", pin.header, selected, err)
		}
		for _, ingress := range []string{"", "default", "production", "empty-stage"} {
			if _, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store, pinned, ingress); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("stage pin crossed ingress %q: %v", ingress, err)
			}
		}
	}
	wrongPin := request
	wrongPin.Headers, _ = json.Marshal(map[string]string{api.ReleaseHeader: f.productionRelease.ID})
	if _, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store, wrongPin, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("production pin entered stage: %v", err)
	}
	for _, scope := range []string{"empty-stage", "missing-stage"} {
		if _, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store, request, scope); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("stage %s fell back to production: %v", scope, err)
		}
	}
	if _, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store, request, "__all__"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid ingress = %v", err)
	}
	foreign := request
	foreign.AccountID = uuid.NewString()
	if _, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store, foreign, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account = %v", err)
	}

	// Admission must fail before keep_latest mutates the existing production lane.
	policy := workpolicy.Policy{Name: "latest", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest}
	productionWork, err := store.EnqueueKeyedInvocation(ctx, request, policy, "s:document")
	if err != nil {
		t.Fatal(err)
	}
	stageWork := prepared
	stageWork.ID = uuid.NewString()
	if _, err := store.EnqueueKeyedInvocation(ctx, stageWork, policy, "s:document"); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
		t.Fatalf("stage keyed admission = %v", err)
	}
	if _, err := store.InvocationByID(ctx, stageWork.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected stage row exists: %v", err)
	}
	retained, err := store.InvocationByID(ctx, productionWork.ID)
	if err != nil || retained.State != state.InvocationPending || retained.WorkSequence != 1 {
		t.Fatalf("stage replaced production lane: %+v, %v", retained, err)
	}
	for _, mutation := range []func(*state.Invocation){
		func(inv *state.Invocation) { inv.Source = state.InvocationQueue },
		func(inv *state.Invocation) { inv.WorkKeyDigest = make([]byte, 32) },
		func(inv *state.Invocation) { inv.OnSuccessDestinationID = uuid.NewString() },
		func(inv *state.Invocation) { inv.OnFailureDestinationID = uuid.NewString() },
	} {
		unsupported := prepared
		unsupported.ID = uuid.NewString()
		mutation(&unsupported)
		if _, err := store.EnqueueInvocation(ctx, unsupported); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
			t.Fatalf("shared resource admission = %v", err)
		}
		if _, _, err := state.ResolveInvocationVersion(ctx, store, unsupported); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
			t.Fatalf("shared resource delivery = %v", err)
		}
	}
	foreignApp, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: "other-invoke", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"staging", "production"} {
		if _, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store, state.Invocation{AppID: foreignApp.ID, AccountID: f.account.ID}, scope); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("standalone named environment fell back: %v", err)
		}
	}
	for _, pin := range []struct{ revision, release string }{{f.stage.ID, ""}, {"", f.stageRelease.ID}} {
		if _, err := store.ResolveInvocationPinScope(ctx, foreignApp.ID, pin.revision, pin.release); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign app pin = %v", err)
		}
	}
	for _, pin := range []struct{ revision, release string }{{"", ""}, {f.stage.ID, f.stageRelease.ID}, {"bad", ""}, {uuid.Nil.String(), ""}} {
		if _, err := store.ResolveInvocationPinScope(ctx, f.app.ID, pin.revision, pin.release); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("malformed pin accepted: %v", err)
		}
	}
	return f
}
