package state

import (
	"context"
	"encoding/json"
	"errors"
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
	for _, bad := range []string{`[]`, `{"X-Gregale-Revision":"bad"}`, `{"X-Gregale-Revision":"` + old.ID + `","x-gregale-revision":"` + old.ID + `"}`} {
		if _, _, err := ResolveInvocationVersion(ctx, store, Invocation{AppID: app.ID, Headers: json.RawMessage(bad)}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("headers %s: %v", bad, err)
		}
	}
}
