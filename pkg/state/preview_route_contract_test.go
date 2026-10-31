package state_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewContractStore is the slice of the store these tests use.
type previewContractStore interface {
	previewRenameStore
	CreateAppIfUnderQuota(ctx context.Context, app state.App, limits api.Limits) (state.App, error)
	UpsertAppOpenAPIDoc(ctx context.Context, appID, accountID string, doc []byte, endpointCount int, openapiVersion string) error
	GetRouteContractOpenAPIDoc(ctx context.Context, appID, accountID string) ([]byte, state.AppOpenAPIDocMeta, error)
	RetireProjectPRPreviews(ctx context.Context, accountID, projectID string) (int, error)
}

// A PR preview created through the quota path keeps its production app's
// declared-route contract; the insert used to drop both columns. With no
// OpenAPI document of its own, the preview is held to its production app's.
func exercisePreviewRouteContract(t *testing.T, store previewContractStore) {
	ctx := context.Background()
	acct, production, _ := seedPRPreview(t, store)
	expires := time.Now().Add(time.Hour)
	routes := []state.DeclaredRoute{{Path: "/api/items", Methods: []string{"GET"}}}
	preview, err := store.CreateAppIfUnderQuota(ctx, state.App{AccountID: acct.ID, Slug: "pr-8-" + production.Slug,
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: production.Slug,
		PreviewPrNumber: 8, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires,
		OnlyAllowDeclaredRoutes: true, DeclaredRoutes: routes}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	got, err := store.AppByID(ctx, preview.ID)
	if err != nil || !got.OnlyAllowDeclaredRoutes || len(got.DeclaredRoutes) != 1 || got.DeclaredRoutes[0].Path != "/api/items" {
		t.Fatalf("stored preview contract = (%t, %+v, %v), want it persisted", got.OnlyAllowDeclaredRoutes, got.DeclaredRoutes, err)
	}

	if _, _, err := store.GetRouteContractOpenAPIDoc(ctx, preview.ID, acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("contract document before import = %v, want ErrNotFound", err)
	}
	doc := []byte(`{"openapi":"3.1.0","info":{"title":"t","version":"1"},"paths":{"/api/items":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	if err := store.UpsertAppOpenAPIDoc(ctx, production.ID, acct.ID, doc, 1, "3.1.0"); err != nil {
		t.Fatalf("UpsertAppOpenAPIDoc: %v", err)
	}
	inherited, meta, err := store.GetRouteContractOpenAPIDoc(ctx, preview.ID, acct.ID)
	if err != nil || meta.AppID != production.ID || len(inherited) == 0 {
		t.Fatalf("preview contract document = (%s, %+v, %v), want production's", inherited, meta, err)
	}
	if _, meta, err := store.GetRouteContractOpenAPIDoc(ctx, production.ID, acct.ID); err != nil || meta.AppID != production.ID {
		t.Fatalf("production contract document = (%+v, %v)", meta, err)
	}
}

func TestMemStorePreviewRouteContract(t *testing.T) {
	exercisePreviewRouteContract(t, state.NewMemStore())
}

func TestPgStorePreviewRouteContract(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exercisePreviewRouteContract(t, state.NewPgStore(pool))
}

// Turning previews off retires the project's open PR previews; another
// project's previews are left alone.
func exerciseRetireProjectPRPreviews(t *testing.T, store interface {
	previewContractStore
	CreateProject(ctx context.Context, p state.Project) (state.Project, error)
}) {
	ctx := context.Background()
	acct, production, _ := seedPRPreview(t, store)
	expires := time.Now().Add(time.Hour)
	preview := func(projectID string, pr int) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: projectID, WorkloadName: "web",
			Slug: "pr-" + strconv.Itoa(pr) + "-" + production.Slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1,
			Status: state.AppActive, PreviewOfSlug: production.Slug, PreviewPrNumber: pr,
			PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	project := func(slug string) state.Project {
		t.Helper()
		p, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: slug + "-" + production.Slug})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	off, kept := project("off"), project("kept")
	retired, other := preview(off.ID, 11), preview(kept.ID, 12)

	n, err := store.RetireProjectPRPreviews(ctx, acct.ID, off.ID)
	if err != nil || n != 1 {
		t.Fatalf("RetireProjectPRPreviews = (%d, %v), want 1", n, err)
	}
	if got, err := store.AppByID(ctx, retired.ID); err != nil || got.PreviewPrState != state.PreviewPrStateStale ||
		got.PreviewExpiresAt == nil || got.PreviewExpiresAt.After(time.Now()) {
		t.Fatalf("retired preview = (%+v, %v), want stale with its lease ended", got, err)
	}
	if got, err := store.AppByID(ctx, other.ID); err != nil || got.PreviewPrState != state.PreviewPrStateOpen {
		t.Fatalf("other project's preview = (%q, %v), want open", got.PreviewPrState, err)
	}
	if n, err := store.RetireProjectPRPreviews(ctx, acct.ID, off.ID); err != nil || n != 0 {
		t.Fatalf("second RetireProjectPRPreviews = (%d, %v), want idempotent", n, err)
	}
}

func TestMemStoreRetireProjectPRPreviews(t *testing.T) {
	exerciseRetireProjectPRPreviews(t, state.NewMemStore())
}

func TestPgStoreRetireProjectPRPreviews(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseRetireProjectPRPreviews(t, state.NewPgStore(pool))
}

// Changing the production app's route contract reaches its open previews
// through apps_sync_preview_access_gates, as its access gates do.
func TestPgStoreProductionRouteContractReachesPreviews(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	store := state.NewPgStore(pool)
	ctx := context.Background()
	_, production, preview := seedPRPreview(t, store)
	on := true
	routes := []state.DeclaredRoute{{Path: "/api/items", Methods: []string{"GET"}}}
	if _, err := store.UpdateApp(ctx, production.ID, state.UpdateAppParams{
		OnlyAllowDeclaredRoutes: &on, SetOnlyAllowDeclaredRoutes: true,
		DeclaredRoutes: &routes, SetDeclaredRoutes: true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	got, err := store.AppByID(ctx, preview.ID)
	if err != nil || !got.OnlyAllowDeclaredRoutes || len(got.DeclaredRoutes) != 1 || got.DeclaredRoutes[0].Path != "/api/items" {
		t.Fatalf("preview after production contract change = (%t, %+v, %v), want production's", got.OnlyAllowDeclaredRoutes, got.DeclaredRoutes, err)
	}
}

// A service the PR adds has no production app; its preview follows the
// access gates of the app the PR previews when that app changes them, but
// keeps its own visibility and takes no route contract.
func TestPgStoreNewServicePreviewFollowsBoundAppGates(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	store := state.NewPgStore(pool)
	ctx := context.Background()
	f := newPreviewEnvironmentFixture(t, ctx, store, api.PlanPro)
	bound, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: f.prefix + "-web",
		WorkloadName: "web", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := f.reserve(ctx, 9, 'a', api.MustLimitsFor(api.PlanPro), "web", "worker")
	if err != nil || len(reserved) != 2 {
		t.Fatalf("reserve = (%d, %v)", len(reserved), err)
	}
	if _, err := pool.Exec(ctx, `update apps set require_signed = true, visibility = 'internal', only_declared_routes = true,
		declared_routes = '[{"path":"/api/items","methods":["GET"]}]'::jsonb where id = $1`, bound.ID); err != nil {
		t.Fatalf("change the bound app's gates: %v", err)
	}
	root, err := store.AppByID(ctx, reserved[0].ID)
	if err != nil || !root.RequireSigned || root.Visibility != api.AppVisibilityInternal || !root.OnlyAllowDeclaredRoutes {
		t.Fatalf("bound app's preview = (%+v, %v), want all of its gates", root, err)
	}
	service, err := store.AppByID(ctx, reserved[1].ID)
	if err != nil || !service.RequireSigned || service.Visibility == api.AppVisibilityInternal ||
		service.OnlyAllowDeclaredRoutes || len(service.DeclaredRoutes) != 0 {
		t.Fatalf("new service's preview = signed %t, visibility %q, contract (%t, %v) (%v); want the access gate only",
			service.RequireSigned, service.Visibility, service.OnlyAllowDeclaredRoutes, service.DeclaredRoutes, err)
	}
}
