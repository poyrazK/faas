package state

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
)

func TestResolveInvocationVersionCapturesAndRechecksRelease(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "version-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug, digest string) (App, Deployment) {
		t.Helper()
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: slug, Status: AppActive,
			Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: digest})
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
	first, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billing.ID, DeploymentID: billingV1.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	billingV2, err := store.CreateDeployment(ctx, Deployment{AppID: billing.ID, Scope: "production", ImageDigest: "sha256:billing-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, billingV2.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billing.ID, DeploymentID: billingV2.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	active, selected, err := ResolveInvocationVersion(ctx, store, Invocation{AppID: billing.ID})
	if err != nil || selected.ReleaseID != second.ID || selected.DeploymentID != billingV2.ID {
		t.Fatalf("active resolution = %+v, %v", selected, err)
	}
	var headers map[string]string
	if err := json.Unmarshal(active.Headers, &headers); err != nil || headers[api.ReleaseHeader] != second.ID {
		t.Fatalf("stamped headers = %s, %v", active.Headers, err)
	}
	requested := Invocation{AppID: billing.ID, Headers: json.RawMessage(`{"X-Gregale-Release":"` + first.ID + `"}`)}
	old, selected, err := ResolveInvocationVersion(ctx, store, requested)
	if err != nil || selected.ReleaseID != first.ID || selected.DeploymentID != billingV1.ID {
		t.Fatalf("old resolution = %+v, %v", selected, err)
	}
	store.mu.Lock()
	expired := store.projectReleaseSets[first.ID]
	deadline := time.Now().Add(-time.Second)
	expired.ExpiresAt = &deadline
	store.projectReleaseSets[first.ID] = expired
	store.mu.Unlock()
	if _, _, err := ResolveInvocationVersion(ctx, store, old); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired queued release = %v", err)
	}
	if _, _, err := ResolveInvocationVersion(ctx, store, Invocation{AppID: apiApp.ID, Headers: json.RawMessage(`{"X-Gregale-Release":"` + second.ID + `","X-Gregale-Revision":"` + apiV1.ID + `"}`)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting pins = %v", err)
	}
}

type invocationSnapshotFailure struct {
	*MemStore
	failure error
	after   bool
}

func (s invocationSnapshotFailure) AppByID(context.Context, string) (App, error) {
	panic("snapshot refusal fell back to a pool read")
}

func (s invocationSnapshotFailure) WithInvocationVersionSnapshot(ctx context.Context, read func(InvocationVersionReader) error) error {
	if s.after {
		if err := s.MemStore.WithInvocationVersionSnapshot(ctx, read); err != nil {
			return err
		}
	}
	return s.failure
}

func TestResolveInvocationVersionSnapshotFailurePublishesNoSelection(t *testing.T) {
	store, ctx, account, app, dep := memDeploymentFixture(t)
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 60
	if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	inv := Invocation{AppID: app.ID, AccountID: account.ID, Headers: json.RawMessage(`{"x-gregale-revision":"` + dep.ID + `"}`)}
	failure := errors.New("snapshot unavailable")
	for _, after := range []bool{false, true} {
		out, version, err := ResolveInvocationVersion(ctx, invocationSnapshotFailure{MemStore: store, failure: failure, after: after}, inv)
		if !errors.Is(err, failure) || version != (InvocationVersion{}) || !reflect.DeepEqual(out, inv) {
			t.Fatalf("partial selection after=%v: %+v %+v %v", after, out, version, err)
		}
		out, version, owner, err := ResolveInvocationDispatch(ctx, invocationSnapshotFailure{MemStore: store, failure: failure, after: after}, inv, nil)
		if !errors.Is(err, failure) || version != (InvocationVersion{}) || owner != "" || !reflect.DeepEqual(out, inv) {
			t.Fatalf("partial dispatch after=%v: %+v %+v %q %v", after, out, version, owner, err)
		}
	}
}

type invocationPoolOnlyReader struct{ *invocationMinimalReader }
type invocationMinimalReader struct{ app App }

func (s *invocationMinimalReader) AppByID(context.Context, string) (App, error) {
	return s.app, nil
}
func (*invocationPoolOnlyReader) ResolveProjectRelease(context.Context, string, string, string) (string, string, error) {
	panic("independent release pool read")
}
func (*invocationPoolOnlyReader) ResolveRevisionPin(context.Context, string, string, string) (Deployment, error) {
	panic("independent revision pool read")
}

func TestResolveInvocationVersionPinRequiresSnapshot(t *testing.T) {
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString()}
	reader := &invocationPoolOnlyReader{&invocationMinimalReader{app: app}}
	if _, _, err := ResolveInvocationVersion(t.Context(), reader, Invocation{AppID: app.ID, Headers: json.RawMessage(`{"X-Gregale-Revision":"` + uuid.NewString() + `"}`)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("pin without snapshot = %v", err)
	}
	if _, _, err := ResolveInvocationVersion(t.Context(), reader, Invocation{AppID: app.ID}); err != nil {
		t.Fatalf("minimal unpinned adapter = %v", err)
	}
	reader.app.ProjectID = uuid.NewString()
	if _, _, err := ResolveInvocationVersion(t.Context(), reader, Invocation{AppID: app.ID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("project without snapshot = %v", err)
	}
}

func TestMemInvocationVersionSnapshotProjectionAndCancellation(t *testing.T) {
	store, ctx, _, app, _ := memDeploymentFixture(t)
	manifest := app.Manifest
	manifest.Env = map[string]string{"SECRET": "not-version-input"}
	if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	err := store.WithInvocationVersionSnapshot(ctx, func(reader InvocationVersionReader) error {
		projected, err := reader.AppByID(ctx, app.ID)
		want := App{ID: app.ID, AccountID: app.AccountID, ProjectID: app.ProjectID, PreviewOfSlug: app.PreviewOfSlug, Status: app.Status}
		if err != nil || !reflect.DeepEqual(projected, want) {
			t.Fatalf("projection contains unrelated app metadata: %+v %v", projected, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	if err := store.WithInvocationVersionSnapshot(ctx, func(InvocationVersionReader) error { cancel(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("callback cancellation = %v", err)
	}
	if err := store.WithInvocationVersionSnapshot(ctx, func(InvocationVersionReader) error { t.Fatal("cancelled snapshot executed"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("entry cancellation = %v", err)
	}
	if err := store.WithInvocationVersionSnapshot(t.Context(), nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil callback = %v", err)
	}
	if _, err := store.AppByID(t.Context(), app.ID); err != nil {
		t.Fatalf("snapshot leaked its mutex: %v", err)
	}
}

func TestResolveInvocationVersionExactRevisionAndMalformedHeaders(t *testing.T) {
	ctx := context.Background()
	store, _, _, app, old := memDeploymentFixture(t)
	store.mu.Lock()
	configured := store.apps[app.ID]
	configured.Manifest.RevisionPinTTLSeconds = 60
	store.apps[app.ID] = configured
	store.mu.Unlock()
	if err := store.MarkDeploymentLive(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	request := Invocation{AppID: app.ID, Headers: json.RawMessage(`{"x-gregale-revision":"` + old.ID + `"}`)}
	forwarded, selected, err := ResolveInvocationVersion(ctx, store, request)
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
		if _, selected, err := ResolveInvocationVersion(ctx, store, request); err != nil || selected.DeploymentID != old.ID {
			t.Fatalf("revision spelling %q: %+v %v", pin, selected, err)
		}
	}
	for _, bad := range []string{`[]`, `{"X-Gregale-Revision":"bad"}`, `{"X-Gregale-Revision":"` + old.ID + `","x-gregale-revision":"` + old.ID + `"}`} {
		if _, _, err := ResolveInvocationVersion(ctx, store, Invocation{AppID: app.ID, Headers: json.RawMessage(bad)}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("headers %s: %v", bad, err)
		}
	}
}

func TestResolveInvocationVersionRejectsRetiredOrForeignOwner(t *testing.T) {
	for _, kind := range []string{"foreign account", "deleted status", "deleted timestamp"} {
		t.Run(kind, func(t *testing.T) {
			store, ctx, account, app, _ := memDeploymentFixture(t)
			inv := Invocation{AppID: app.ID, AccountID: account.ID}
			store.mu.Lock()
			current := store.apps[app.ID]
			switch kind {
			case "foreign account":
				inv.AccountID = uuid.NewString()
			case "deleted status":
				current.Status = AppDeleted
			case "deleted timestamp":
				at := time.Now().UTC()
				current.DeletedAt = &at
			}
			store.apps[app.ID] = current
			store.mu.Unlock()
			if _, _, err := ResolveInvocationVersion(ctx, store, inv); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unsafe invocation accepted: %v", err)
			}
		})
	}
}
