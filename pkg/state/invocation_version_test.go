package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestResolveInvocationVersionCapturesAndRechecksRelease(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "version-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug, digest string) (state.App, state.Deployment) {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: slug, Status: state.AppActive,
			Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", ImageDigest: digest})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return app, dep
	}
	apiApp, apiV1 := create("api", "sha256:api-v1")
	billing, billingV1 := create("billing", "sha256:billing-v1")
	first, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billing.ID, DeploymentID: billingV1.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	billingV2, err := store.CreateDeployment(ctx, state.Deployment{AppID: billing.ID, Scope: "production", ImageDigest: "sha256:billing-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, billingV2.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billing.ID, DeploymentID: billingV2.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	active, selected, err := state.ResolveInvocationVersion(ctx, store, state.Invocation{AppID: billing.ID})
	if err != nil || selected.ReleaseID != second.ID || selected.DeploymentID != billingV2.ID {
		t.Fatalf("active resolution = %+v, %v", selected, err)
	}
	var headers map[string]string
	if err := json.Unmarshal(active.Headers, &headers); err != nil || headers[api.ReleaseHeader] != second.ID {
		t.Fatalf("stamped headers = %s, %v", active.Headers, err)
	}
	requested := state.Invocation{AppID: billing.ID, Headers: json.RawMessage(`{"X-Gregale-Release":"` + first.ID + `"}`)}
	old, selected, err := state.ResolveInvocationVersion(ctx, store, requested)
	if err != nil || selected.ReleaseID != first.ID || selected.DeploymentID != billingV1.ID {
		t.Fatalf("old resolution = %+v, %v", selected, err)
	}
	(*store.Mu()).Lock()
	expired := (*store.ProjectReleaseSets())[first.ID]
	deadline := time.Now().Add(-time.Second)
	expired.ExpiresAt = &deadline
	(*store.ProjectReleaseSets())[first.ID] = expired
	(*store.Mu()).Unlock()
	if _, _, err := state.ResolveInvocationVersion(ctx, store, old); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired queued release = %v", err)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, store, state.Invocation{AppID: apiApp.ID, Headers: json.RawMessage(`{"X-Gregale-Release":"` + second.ID + `","X-Gregale-Revision":"` + apiV1.ID + `"}`)}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting pins = %v", err)
	}
}

type invocationPoolOnlyReader struct{ *invocationMinimalReader }
type invocationMinimalReader struct{ app state.App }

func (s *invocationMinimalReader) AppByID(context.Context, string) (state.App, error) {
	return s.app, nil
}
func (*invocationPoolOnlyReader) ResolveProjectRelease(context.Context, string, string, string) (string, string, error) {
	panic("independent release pool read")
}
func (*invocationPoolOnlyReader) ResolveRevisionPin(context.Context, string, string, string) (state.Deployment, error) {
	panic("independent revision pool read")
}

func TestResolveInvocationVersionPinRequiresSnapshot(t *testing.T) {
	app := state.App{ID: uuid.NewString(), AccountID: uuid.NewString()}
	reader := &invocationPoolOnlyReader{&invocationMinimalReader{app: app}}
	if _, _, err := state.ResolveInvocationVersion(t.Context(), reader, state.Invocation{AppID: app.ID, Headers: json.RawMessage(`{"X-Gregale-Revision":"` + uuid.NewString() + `"}`)}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pin without snapshot = %v", err)
	}
	if _, _, err := state.ResolveInvocationVersion(t.Context(), reader, state.Invocation{AppID: app.ID}); err != nil {
		t.Fatalf("minimal unpinned adapter = %v", err)
	}
	reader.app.ProjectID = uuid.NewString()
	if _, _, err := state.ResolveInvocationVersion(t.Context(), reader, state.Invocation{AppID: app.ID}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("project without snapshot = %v", err)
	}
}

func TestMemInvocationVersionSnapshotProjectionAndCancellation(t *testing.T) {
	store, ctx, _, app, _ := state.MemDeploymentFixture(t)
	manifest := app.Manifest
	manifest.Env = map[string]string{"SECRET": "not-version-input"}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	err := store.WithInvocationVersionSnapshot(ctx, func(reader state.InvocationVersionReader) error {
		projected, err := reader.AppByID(ctx, app.ID)
		want := state.App{ID: app.ID, AccountID: app.AccountID, ProjectID: app.ProjectID, PreviewOfSlug: app.PreviewOfSlug, Status: app.Status}
		if err != nil || !reflect.DeepEqual(projected, want) {
			t.Fatalf("projection contains unrelated app metadata: %+v %v", projected, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	if err := store.WithInvocationVersionSnapshot(ctx, func(state.InvocationVersionReader) error { cancel(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("callback cancellation = %v", err)
	}
	if err := store.WithInvocationVersionSnapshot(ctx, func(state.InvocationVersionReader) error { t.Fatal("cancelled snapshot executed"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("entry cancellation = %v", err)
	}
	if err := store.WithInvocationVersionSnapshot(t.Context(), nil); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("nil callback = %v", err)
	}
	if _, err := store.AppByID(t.Context(), app.ID); err != nil {
		t.Fatalf("snapshot leaked its mutex: %v", err)
	}
}

func TestResolveInvocationVersionExactRevisionAndMalformedHeaders(t *testing.T) {
	ctx := context.Background()
	store, _, _, app, old := state.MemDeploymentFixture(t)
	(*store.Mu()).Lock()
	configured := (*store.Apps())[app.ID]
	configured.Manifest.RevisionPinTTLSeconds = 60
	(*store.Apps())[app.ID] = configured
	(*store.Mu()).Unlock()
	if err := store.MarkDeploymentLive(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	request := state.Invocation{AppID: app.ID, Headers: json.RawMessage(`{"x-gregale-revision":"` + old.ID + `"}`)}
	forwarded, selected, err := state.ResolveInvocationVersion(ctx, store, request)
	if err != nil || selected.DeploymentID != old.ID || selected.ReleaseID != "" {
		t.Fatalf("revision resolution = %+v, %v", selected, err)
	}
	var headers map[string]string
	if err := json.Unmarshal(forwarded.Headers, &headers); err != nil || headers[api.RevisionHeader] != old.ID || len(headers) != 1 {
		t.Fatalf("revision headers = %s, %v", forwarded.Headers, err)
	}
	canonical := uuid.MustParse(old.ID).String()
	for _, pin := range []string{canonical, strings.ToUpper(canonical), "urn:uuid:" + canonical, "{" + canonical + "}"} {
		request.Headers = json.RawMessage(`{"X-Gregale-Revision":"` + pin + `"}`)
		if _, selected, err := state.ResolveInvocationVersion(ctx, store, request); err != nil || selected.DeploymentID != old.ID {
			t.Fatalf("revision spelling %q: %+v %v", pin, selected, err)
		}
	}
	for _, bad := range []string{`[]`, `{"X-Gregale-Revision":"bad"}`, `{"X-Gregale-Revision":"` + old.ID + `","x-gregale-revision":"` + old.ID + `"}`} {
		if _, _, err := state.ResolveInvocationVersion(ctx, store, state.Invocation{AppID: app.ID, Headers: json.RawMessage(bad)}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("headers %s: %v", bad, err)
		}
	}
}

func TestResolveInvocationVersionRejectsRetiredOrForeignOwner(t *testing.T) {
	for _, kind := range []string{"foreign account", "deleted status", "deleted timestamp"} {
		t.Run(kind, func(t *testing.T) {
			store, ctx, account, app, _ := state.MemDeploymentFixture(t)
			inv := state.Invocation{AppID: app.ID, AccountID: account.ID}
			(*store.Mu()).Lock()
			current := (*store.Apps())[app.ID]
			switch kind {
			case "foreign account":
				inv.AccountID = uuid.NewString()
			case "deleted status":
				current.Status = state.AppDeleted
			case "deleted timestamp":
				at := time.Now().UTC()
				current.DeletedAt = &at
			}
			(*store.Apps())[app.ID] = current
			(*store.Mu()).Unlock()
			if _, _, err := state.ResolveInvocationVersion(ctx, store, inv); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unsafe invocation accepted: %v", err)
			}
		})
	}
}

func TestResolveInvocationVersionUsesCapturedProjectScope(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "captured-version@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "captured-version"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "captured-api",
		Status: state.AppActive, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	deployments := map[string]state.Deployment{}
	releases := map[string]state.ProjectReleaseSet{}
	for _, scope := range []string{"production", "staging"} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, ImageDigest: "sha256:" + scope})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		release, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, scope, 1800,
			[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}})
		if err != nil {
			t.Fatal(err)
		}
		deployments[scope], releases[scope] = dep, release
	}
	for _, scope := range []string{"production", "staging"} {
		inv, selected, err := state.ResolveInvocationVersion(ctx, store, state.Invocation{AppID: app.ID, DeploymentScope: scope})
		if err != nil || inv.DeploymentScope != scope || selected.Scope != scope || selected.DeploymentID != deployments[scope].ID || selected.ReleaseID != releases[scope].ID {
			t.Fatalf("resolution %s = %+v, %v", scope, selected, err)
		}
	}
	for _, header := range []string{api.ReleaseHeader, api.RevisionHeader} {
		pin := releases["production"].ID
		if header == api.RevisionHeader {
			pin = deployments["production"].ID
		}
		headers, _ := json.Marshal(map[string]string{header: pin})
		if _, _, err := state.ResolveInvocationVersion(ctx, store, state.Invocation{AppID: app.ID, DeploymentScope: "staging", Headers: headers}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cross-scope pin accepted for %s: %v", header, err)
		}
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, store, state.Invocation{AppID: app.ID, DeploymentScope: "__all__"}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid captured scope accepted: %v", err)
	}
}

// A project app without a release graph dispatches production work to the
// lane the gateway routes as production: the legacy default scope until a
// named production deployment carries traffic.
func TestResolveInvocationVersionProjectProductionLane(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []string
		want   string
	}{
		{name: "legacy default lane only", scopes: []string{state.DefaultEnvScope}, want: state.DefaultEnvScope},
		{name: "named production lane only", scopes: []string{"production"}, want: "production"},
		{name: "named production lane takes precedence", scopes: []string{state.DefaultEnvScope, "production"}, want: "production"},
		{name: "no live deployment", want: "production"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "lane-"+uuid.NewString()+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "lane"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "lane-api", Status: state.AppActive,
				Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
			if err != nil {
				t.Fatal(err)
			}
			for _, scope := range tc.scopes {
				dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, ImageDigest: "sha256:" + scope})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
					t.Fatal(err)
				}
			}
			for _, ingress := range []bool{false, true} {
				request := state.Invocation{AppID: app.ID}
				var inv state.Invocation
				var selected state.InvocationVersion
				if ingress {
					inv, selected, err = state.ResolveInvocationVersionForEnvironment(ctx, store, request, "")
				} else {
					inv, selected, err = state.ResolveInvocationVersion(ctx, store, request)
				}
				if err != nil || selected.Scope != tc.want || selected.DeploymentID != "" || inv.DeploymentScope != "production" {
					t.Fatalf("ingress=%v: version = %+v, captured scope %q, %v; want dispatch scope %q", ingress, selected, inv.DeploymentScope, err, tc.want)
				}
				if len(tc.scopes) > 0 {
					if _, err := store.LiveDeploymentForScope(ctx, app.ID, selected.Scope); err != nil {
						t.Fatalf("ingress=%v: dispatch scope %q has no live deployment to wake: %v", ingress, selected.Scope, err)
					}
				}
			}
		})
	}
}
