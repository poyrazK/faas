// adr: 375
package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var trafficAppWithdrawalModes = []string{"visibility", "status", "activity-visibility", "activity-status", "status-cas", "schedule", "activity-schedule", "soft-delete", "delete"}

func trafficAppBindingActivity(account Account) OrgActivity {
	actor := uuid.MustParse(account.ID)
	return OrgActivity{ActorAccountID: &actor, ActorType: OrgActivityActorUser, ActorLabel: account.Email,
		Kind: "app.updated", ResourceType: "app", SourceType: "traffic-app-binding", SourceID: uuid.NewString(), Data: json.RawMessage(`{"phase":"app-binding"}`)}
}

func seedTrafficAppCleanup(t *testing.T, store Store, account Account, app App) {
	t.Helper()
	ctx := t.Context()
	if _, err := store.EnqueueInvocation(ctx, Invocation{AppID: app.ID, AccountID: account.ID, Source: InvocationDelayedTask, Method: "POST", Path: "/work", Payload: json.RawMessage(`{}`), Headers: json.RawMessage(`{}`), DueAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	reserved, err := store.EnqueueInvocation(ctx, Invocation{AppID: app.ID, AccountID: account.ID, Source: InvocationDelayedTask, Method: "POST", Path: "/reserved", Payload: json.RawMessage(`{}`), Headers: json.RawMessage(`{}`), DueAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, reserved.ID, "", 60, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCron(ctx, app.ID, "*/5 * * * *", "/cleanup", true); err != nil {
		t.Fatal(err)
	}
	pending, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindTarball, ImageDigest: "sha256:traffic-app-cleanup"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, pending.ID, DeployBuilding, ""); err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuild(ctx, pending.ID, DeploymentKindTarball, 128, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueuedBuild(ctx, build.ID); err != nil {
		t.Fatal(err)
	}
	live, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployLive, ImageDigest: "sha256:traffic-app-live", RootfsPath: "/traffic-app/rootfs.ext4", RootfsKey: "traffic-app/rootfs.ext4"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, live.ID, "/traffic-app/rootfs.ext4", "traffic-app/rootfs.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(AppTaskStore).CreateAppTask(ctx, CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: live.ID, Kind: AppTaskKindManual, Command: []string{"true"}}); err != nil {
		t.Fatal(err)
	}
}

func testTrafficAppWithdrawal(t *testing.T, store Store, account Account, app App, mode string, seed func(EdgeRule), intent func() string) {
	t.Helper()
	ctx := t.Context()
	surface, host := newTrafficTenantClaim(t, store, account, app, "app-withdrawal.example.test")
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTenantHostnameVerified(ctx, host.Hostname); err != nil {
		t.Fatal(err)
	}
	peerAccount, peer := trafficTenantTransitionPeer(t, store)
	if _, err := store.CreateCustomDomain(ctx, "*.example.test", peer.ID, "private-peer-token"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "*.example.test"); err != nil {
		t.Fatal(err)
	}
	seedTrafficAppCleanup(t, store, account, app)
	legacy := publicationLegacyRule(peerAccount, peer, host.Hostname)
	seed(legacy)
	before := intent()
	original, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(c context.Context) error {
		visibility, status := api.AppVisibilityInternal, AppDeleted
		params := UpdateAppParams{SetVisibility: true, Visibility: &visibility}
		if mode == "status" || mode == "activity-status" {
			params = UpdateAppParams{Status: &status}
		}
		switch mode {
		case "activity-visibility", "activity-status":
			_, receipt, err := store.(OrgActivityAppConfigMutationStore).UpdateAppWithActivity(c, app.ID, params, trafficAppBindingActivity(account), func(App, App) (json.RawMessage, bool, error) { return json.RawMessage(`{"changed":true}`), true, nil })
			if err != nil && receipt != 0 {
				t.Fatal("refusal returned activity receipt")
			}
			return err
		case "status-cas":
			changed, err := store.(interface {
				CompareAndSetAppStatus(context.Context, string, AppStatus, AppStatus) (bool, error)
			}).CompareAndSetAppStatus(c, app.ID, AppActive, AppDeleted)
			if err != nil && changed {
				t.Fatal("refused CAS returned changed")
			}
			return err
		case "schedule":
			_, err := store.ScheduleAppDeletion(c, app.ID, time.Now().Add(time.Hour))
			return err
		case "activity-schedule":
			_, receipt, err := store.(OrgActivityAppLifecycleMutationStore).ScheduleAppDeletionWithActivity(c, app.ID, time.Now().Add(time.Hour), trafficAppBindingActivity(account))
			if err != nil && receipt != 0 {
				t.Fatal("refusal returned deletion receipt")
			}
			return err
		case "soft-delete":
			_, err := store.SoftDeleteAppCascade(c, app.ID)
			return err
		case "delete":
			return store.DeleteApp(c, app.ID)
		default:
			_, err := store.UpdateApp(c, app.ID, params)
			return err
		}
	}
	requireTrafficTenantRefusal(t, mutate(ctx))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := mutate(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled app withdrawal: %v", err)
	}
	current, err := store.AppByID(ctx, app.ID)
	if err != nil || !reflect.DeepEqual(original, current) || before != intent() {
		t.Fatal("refusal changed app, jobs, builds, cleanup, reservations, or activity")
	}
	if err := store.DeleteEdgeRule(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := mutate(ctx); err != nil {
		t.Fatalf("app withdrawal after repair: %v", err)
	}
	if mode == "schedule" || mode == "activity-schedule" || mode == "soft-delete" || mode == "delete" {
		pending, err := store.ListInvocationsForApp(ctx, app.ID, InvocationPending, InvocationDispatching)
		if err != nil || len(pending) != 0 {
			t.Fatal("accepted deletion left invocations runnable")
		}
		_, current, err := store.GetAccountAsyncQuota(ctx, account.ID)
		if err != nil || current != 0 {
			t.Fatalf("accepted deletion did not release reserved quota: current=%d err=%v", current, err)
		}
	}
}

func memTrafficAppIntent(t *testing.T, m *MemStore) string {
	return trafficTenantIntentDigest(t, []any{m.apps, m.crons, m.appTasks, m.deployments, m.builds, m.builderVMCleanup, m.snapshots,
		m.invocations, m.accountAsyncQuota, m.appDeletionClaims, m.domains, m.defaultDomains, m.tenantSurfaces, m.tenantHostnames, m.platformTenantBySurface, m.orgActivityOutbox, m.orgActivityOutboxByKey})
}

func TestMemTrafficAppBindingWithdrawals(t *testing.T) {
	for _, mode := range trafficAppWithdrawalModes {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			testTrafficAppWithdrawal(t, m, account, app, mode, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficAppIntent(t, m) })
		})
	}
}

func testTrafficAppPurgeFallback(t *testing.T, store Store, account Account, app App, seed func(EdgeRule), intent func() string) {
	t.Helper()
	ctx := t.Context()
	surface, host := newTrafficTenantClaim(t, store, account, app, "purged-app.example.test")
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTenantHostnameVerified(ctx, host.Hostname); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.(PlatformTenantStore).CreatePlatformTenant(ctx, account.ID, "purged-app", "Purged app", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(PlatformTenantStore).LinkPlatformTenantSurface(ctx, account.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(PlatformTenantStore).SetPlatformTenantStatus(ctx, account.ID, tenant.ID, PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	peerAccount, peer := trafficTenantTransitionPeer(t, store)
	if _, err := store.CreateCustomDomain(ctx, "*.example.test", peer.ID, "private-peer-token"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "*.example.test"); err != nil {
		t.Fatal(err)
	}
	seedTrafficAppCleanup(t, store, account, app)
	if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimAppDeletion(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	legacy := publicationLegacyRule(peerAccount, peer, host.Hostname)
	seed(legacy)
	before := intent()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.DeleteAppPermanently(canceled, app.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled app purge: %v", err)
	}
	requireTrafficTenantRefusal(t, store.DeleteAppPermanently(ctx, app.ID))
	if before != intent() {
		t.Fatal("refused purge changed app, reservations, cleanup, or claim")
	}
	if err := store.DeleteEdgeRule(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppPermanently(ctx, app.ID); err != nil {
		t.Fatalf("repaired purge: %v", err)
	}
	if _, err := store.AppByID(ctx, app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purge kept app: %v", err)
	}
	if _, err := store.GetTenantHostnameByName(ctx, host.Hostname); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purge kept hostname reservation: %v", err)
	}
}

func TestMemTrafficAppPurgeFallback(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	testTrafficAppPurgeFallback(t, m, account, app, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficAppIntent(t, m) })
}

// Purge also removes a legacy redirect row owned by another app. Discovery
// must include that row before finding its newly exposed wildcard owner.
func testTrafficAppPurgeForeignRedirect(t *testing.T, store Store, account Account, app App, redirect func(string, string), seed func(EdgeRule), intent func() string) {
	t.Helper()
	ctx := t.Context()
	_, exactApp := trafficTenantTransitionPeer(t, store)
	fallbackAccount, err := store.CreateAccount(ctx, "app-purge-fallback@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	fallbackApp, err := store.CreateApp(ctx, App{AccountID: fallbackAccount.ID, Slug: "app-purge-fallback", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	const exact = "redirect-purge.example.test"
	if _, err := store.CreateCustomDomain(ctx, exact, exactApp.ID, "private-exact"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, exact); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "*.example.test", fallbackApp.ID, "private-fallback"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "*.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := store.(interface {
		SetDefaultCustomDomain(context.Context, string, string) error
	}).SetDefaultCustomDomain(ctx, exactApp.ID, exact); err != nil {
		t.Fatal(err)
	}
	redirect(exact, app.ID)
	if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimAppDeletion(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	legacy := publicationLegacyRule(fallbackAccount, fallbackApp, exact)
	seed(legacy)
	before := intent()
	requireTrafficTenantRefusal(t, store.DeleteAppPermanently(ctx, app.ID))
	if before != intent() {
		t.Fatal("refused purge changed foreign redirect claim or lifecycle intent")
	}
	if err := store.DeleteEdgeRule(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppPermanently(ctx, app.ID); err != nil {
		t.Fatalf("redirect purge after repair: %v", err)
	}
	if _, err := store.DomainByName(ctx, exact); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purge retained redirect claim: %v", err)
	}
	if _, err := store.AppByID(ctx, exactApp.ID); err != nil {
		t.Fatalf("purge removed redirect owner app: %v", err)
	}
	if _, err := store.(DefaultCustomDomainStore).DefaultCustomDomain(ctx, exactApp.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purge retained default pointer: %v", err)
	}
	if _, err := store.CreateCustomDomain(ctx, exact, fallbackApp.ID, "private-reclaimed"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, exact); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(DefaultCustomDomainStore).DefaultCustomDomain(ctx, exactApp.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("default pointer inherited a foreign reclaimed hostname: %v", err)
	}
}

func TestMemTrafficAppPurgeForeignRedirect(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	testTrafficAppPurgeForeignRedirect(t, m, account, app, func(host, id string) { d := m.domains[host]; d.RedirectAppID = id; m.domains[host] = d }, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficAppIntent(t, m) })
}

func testTrafficAppPurgeGlobalReservation(t *testing.T, store Store, account Account, app App, kind string, seed func(EdgeRule), intent func() string) {
	t.Helper()
	ctx := t.Context()
	const host = "global-app-purge.example.test"
	if kind == "domain" {
		if _, err := store.CreateCustomDomain(ctx, host, app.ID, "private-pending"); err != nil {
			t.Fatal(err)
		}
	} else {
		surface, _ := newTrafficTenantClaim(t, store, account, app, host)
		if err := store.DeleteTenantSurface(ctx, surface.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimAppDeletion(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	peerAccount, peer := trafficTenantTransitionPeer(t, store)
	legacy := publicationLegacyRule(peerAccount, peer, host)
	seed(legacy)
	before := intent()
	requireTrafficTenantRefusal(t, store.DeleteAppPermanently(ctx, app.ID))
	if before != intent() {
		t.Fatal("refused purge released inactive routing reservation")
	}
	if err := store.DeleteEdgeRule(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppPermanently(ctx, app.ID); err != nil {
		t.Fatalf("global reservation purge after repair: %v", err)
	}
}

func TestMemTrafficAppPurgeGlobalReservations(t *testing.T) {
	for _, kind := range []string{"domain", "deleted-tenant"} {
		t.Run(kind, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			testTrafficAppPurgeGlobalReservation(t, m, account, app, kind, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficAppIntent(t, m) })
		})
	}
}

func testTrafficAppRenamePublication(t *testing.T, store Store, account Account, app App, seed func(EdgeRule), intent func() string) {
	t.Helper()
	legacy := publicationLegacyRule(account, app, "renamed-traffic.apps.example.test")
	seed(legacy)
	before := intent()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.RenameApp(canceled, account.ID, app.Slug, "renamed-traffic"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled rename: %v", err)
	}
	_, err := store.RenameApp(t.Context(), account.ID, app.Slug, "renamed-traffic")
	requireTrafficTenantRefusal(t, err)
	if before != intent() {
		t.Fatal("refused namespace publication changed app intent")
	}
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	renamed, err := store.RenameApp(t.Context(), account.ID, app.Slug, "renamed-traffic")
	if err != nil || renamed.ID != app.ID || renamed.Slug != "renamed-traffic" {
		t.Fatalf("rename after repair: %v", err)
	}
}

func TestMemTrafficAppRenamePublication(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	m.trafficAppsSuffix = ".apps.example.test"
	testTrafficAppRenamePublication(t, m, account, app, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficAppIntent(t, m) })
}
