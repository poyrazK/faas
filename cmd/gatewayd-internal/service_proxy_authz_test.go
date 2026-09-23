// adr: 168
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceProxyAuthorizer(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	// seedApp creates a fresh account per call, so caller and target are
	// built explicitly here to share one — otherwise the "same account"
	// case would be testing the cross-account path by accident.
	acct, err := store.CreateAccount(ctx, "authz@local", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	newApp := func(accountID, slug string, manifests ...state.AppManifest) state.App {
		t.Helper()
		var manifest state.AppManifest
		if len(manifests) > 0 {
			manifest = manifests[0]
		}
		app, err := store.CreateApp(ctx, state.App{
			AccountID: accountID, Slug: slug,
			Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive, Manifest: manifest,
		})
		if err != nil {
			t.Fatalf("CreateApp %q: %v", slug, err)
		}
		return app
	}
	caller := newApp(acct.ID, "authzcaller")
	target := newApp(acct.ID, "authztarget")
	strictAllowed := newApp(acct.ID, "strictallowed", state.AppManifest{
		ServiceBindingPolicy: api.ServiceBindingPolicyDeclared,
		ServiceBindings: []api.AppServiceBinding{{
			Binding: "GREGALE_SERVICE_AUTHZTARGET_URL",
			Service: "authztarget",
		}},
	})
	strictUndeclared := newApp(acct.ID, "strictundeclared", state.AppManifest{
		ServiceBindingPolicy: api.ServiceBindingPolicyDeclared,
		ServiceBindings: []api.AppServiceBinding{{
			Binding: "GREGALE_SERVICE_OTHER_URL",
			Service: "other",
		}},
	})
	strictEmpty := newApp(acct.ID, "strictempty", state.AppManifest{
		ServiceBindingPolicy: api.ServiceBindingPolicyDeclared,
	})
	unknownPolicy := newApp(acct.ID, "unknownpolicy", state.AppManifest{
		ServiceBindingPolicy: api.ServiceBindingPolicy("future-policy"),
	})
	denyingTarget := newApp(acct.ID, "denyingtarget", state.AppManifest{
		PreviewServiceCallsPolicy: api.PreviewServiceCallsDeny,
	})
	unknownTargetPolicy := newApp(acct.ID, "unknowntargetpolicy", state.AppManifest{
		PreviewServiceCallsPolicy: api.PreviewServiceCallsPolicy("future-policy"),
	})
	preview, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "pr-42-authzcaller", Type: state.AppTypeApp,
		RAMMB: 128, Status: state.AppActive, PreviewOfSlug: caller.Slug,
	})
	if err != nil {
		t.Fatalf("CreateApp preview: %v", err)
	}
	previewTarget, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "pr-42-denyingtarget", Type: state.AppTypeApp,
		RAMMB: 128, Status: state.AppActive, PreviewOfSlug: denyingTarget.Slug,
		Manifest: state.AppManifest{PreviewServiceCallsPolicy: api.PreviewServiceCallsDeny},
	})
	if err != nil {
		t.Fatalf("CreateApp preview target: %v", err)
	}

	// A second account, to stand in for a cross-tenant caller.
	otherAcct, err := store.CreateAccount(ctx, "other@local", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	outsider := newApp(otherAcct.ID, "authzoutsider")

	// A well-formed id that names nothing.
	const absentUUID = "00000000-0000-4000-8000-000000000000"

	tests := []struct {
		name    string
		caller  string
		target  string
		wantErr error
	}{
		{"same account is allowed", caller.ID, target.ID, nil},
		{"declared binding is allowed", strictAllowed.ID, target.ID, nil},
		{"undeclared target is binding denied", strictUndeclared.ID, target.ID, gateway.ErrServiceProxyBindingDenied},
		{"empty strict binding set denies all services", strictEmpty.ID, target.ID, gateway.ErrServiceProxyBindingDenied},
		{"unknown persisted policy fails closed", unknownPolicy.ID, target.ID, gateway.ErrServiceProxyBindingDenied},
		{"preview reaches legacy production target", preview.ID, target.ID, nil},
		{"production reaches guarded target", caller.ID, denyingTarget.ID, nil},
		{"preview cannot reach guarded production target", preview.ID, denyingTarget.ID, gateway.ErrServiceProxyPreviewDenied},
		{"unknown target policy fails closed for preview", preview.ID, unknownTargetPolicy.ID, gateway.ErrServiceProxyPreviewDenied},
		{"unknown target policy does not block production", caller.ID, unknownTargetPolicy.ID, nil},
		{"guard applies only to production target", preview.ID, previewTarget.ID, nil},
		{"cross-account is denied", outsider.ID, target.ID, gateway.ErrServiceProxyDenied},
		{"absent caller is denied", absentUUID, target.ID, gateway.ErrServiceProxyDenied},
		{"absent target is denied", caller.ID, absentUUID, gateway.ErrServiceProxyDenied},
		// A malformed id can never name a row in a uuid column. Passing it
		// through makes Postgres raise 22P02, which the proxy could only
		// report as 503 "authorization unavailable" — blaming the platform
		// for a caller error, and paying a round-trip guaranteed to fail.
		{"malformed caller is denied, not a platform fault", "app-does-not-exist", target.ID, gateway.ErrServiceProxyDenied},
		{"malformed target is denied", caller.ID, "not-a-uuid", gateway.ErrServiceProxyDenied},
		{"empty caller is denied", "", target.ID, gateway.ErrServiceProxyDenied},
	}

	authorize := newServiceProxyAuthorizer(store)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := authorize(context.Background(), tc.caller, tc.target)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("authorize = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("authorize = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// A genuine store failure must stay a 503 — it is the one case that really is
// a platform fault, and collapsing it into "denied" would hide an outage
// behind what looks like an authorization decision.
func TestServiceProxyAuthorizerSurfacesStoreFailure(t *testing.T) {
	boom := errors.New("connection refused")
	authorize := newServiceProxyAuthorizer(failingAppStore{Store: state.NewMemStore(), err: boom})

	_, err := authorize(context.Background(), "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002")
	if errors.Is(err, gateway.ErrServiceProxyDenied) {
		t.Fatal("store failure was reported as a denial; an outage would look like an authz decision")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("authorize = %v, want it to wrap %v", err, boom)
	}
}

type failingAppStore struct {
	state.Store
	err error
}

func (f failingAppStore) AppByID(context.Context, string) (state.App, error) {
	return state.App{}, f.err
}

// The caller row is already loaded for the tenant check, so its preview
// identity must come back with it — the hop needs it to mark a
// preview-to-production call, and a third store read for a fact already in
// hand would be pure waste on the request path.
func TestServiceProxyAuthorizerCarriesPreviewIdentity(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "preview-authz@local", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	mk := func(slug, previewOf string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{
			AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp,
			RAMMB: 128, Status: state.AppActive, PreviewOfSlug: previewOf,
		})
		if err != nil {
			t.Fatalf("CreateApp %q: %v", slug, err)
		}
		return app
	}
	target := mk("previewtarget", "")
	prod := mk("prodcaller", "")
	preview := mk("pr-42-prodcaller", "prodcaller")

	authorize := newServiceProxyAuthorizer(store)

	got, err := authorize(ctx, prod.ID, target.ID)
	if err != nil {
		t.Fatalf("authorize production caller: %v", err)
	}
	if got.PreviewOfSlug != "" {
		t.Errorf("production caller PreviewOfSlug = %q, want empty", got.PreviewOfSlug)
	}

	got, err = authorize(ctx, preview.ID, target.ID)
	if err != nil {
		t.Fatalf("authorize preview caller: %v", err)
	}
	if got.PreviewOfSlug != "prodcaller" {
		t.Errorf("preview caller PreviewOfSlug = %q, want prodcaller", got.PreviewOfSlug)
	}
	if got.AppID != preview.ID {
		t.Errorf("caller AppID = %q, want %q", got.AppID, preview.ID)
	}
}

func TestServiceProxyAuthorizerEnforcesPreviewServicePolicy(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "preview-policy-authz@local", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "preview-policy"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	mk := func(slug, projectID, previewOf string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{
			AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp,
			RAMMB: 128, Status: state.AppActive, ProjectID: projectID,
			PreviewOfSlug: previewOf,
		})
		if err != nil {
			t.Fatalf("CreateApp %q: %v", slug, err)
		}
		return app
	}
	parent := mk("policy-parent", project.ID, "")
	preview := mk("pr-42-policy-parent", project.ID, parent.Slug)
	target := mk("policy-target", project.ID, "")
	authorize := newServiceProxyAuthorizer(store)

	// No persisted row means a newly created project and therefore deny.
	if _, err := authorize(ctx, preview.ID, target.ID); !errors.Is(err, gateway.ErrServiceProxyPreviewProductionDenied) {
		t.Fatalf("default preview authorization = %v, want preview-production denial", err)
	}

	policy := state.DefaultGitHubDeployPolicy(project.ID, acct.ID)
	policy.PreviewServicePolicy = state.PreviewServicePolicyAllowMarked
	if _, err := store.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatalf("allow preview services: %v", err)
	}
	got, err := authorize(ctx, preview.ID, target.ID)
	if err != nil {
		t.Fatalf("authorize explicitly allowed preview: %v", err)
	}
	if got.PreviewOfSlug != parent.Slug {
		t.Errorf("allowed preview identity = %q, want %q", got.PreviewOfSlug, parent.Slug)
	}
	if _, err := authorize(ctx, parent.ID, target.ID); err != nil {
		t.Fatalf("production caller was affected by preview policy: %v", err)
	}

	// Pre-policy preview rows did not carry project_id. Resolve their parent so
	// changing the policy also protects previews that are already alive.
	policy.PreviewServicePolicy = state.PreviewServicePolicyDeny
	if _, err := store.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatalf("deny preview services: %v", err)
	}
	legacyPreview := mk("pr-43-policy-parent", "", parent.Slug)
	if _, err := authorize(ctx, legacyPreview.ID, target.ID); !errors.Is(err, gateway.ErrServiceProxyPreviewProductionDenied) {
		t.Fatalf("legacy preview authorization = %v, want preview-production denial", err)
	}

	// Standalone apps have no project-owned policy surface. Preserve their
	// existing marked-call behaviour until environment-scoped dependencies
	// exist for them too.
	standaloneParent := mk("standalone-parent", "", "")
	standalonePreview := mk("pr-44-standalone-parent", "", standaloneParent.Slug)
	if _, err := authorize(ctx, standalonePreview.ID, target.ID); err != nil {
		t.Fatalf("standalone preview authorization = %v, want legacy allow", err)
	}
	danglingPreview := mk("pr-45-missing-parent", "", "missing-parent")
	if _, err := authorize(ctx, danglingPreview.ID, target.ID); !errors.Is(err, gateway.ErrServiceProxyDenied) {
		t.Fatalf("dangling preview authorization = %v, want fail-closed denial", err)
	}
}
