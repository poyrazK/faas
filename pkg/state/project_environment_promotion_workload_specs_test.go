// adr: 566
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type promotionWorkloadTestStore interface {
	state.Store
	state.ProjectReleaseSetStore
	state.ProjectReleaseSetReader
	state.ProjectEnvironmentPromotionReleaseSetStore
	state.ProjectEnvironmentPromotionWorkloadSpecStore
	state.ProjectEnvironmentWorkloadSpecStore
	state.DeploymentWorkloadSpecReader
	state.ProjectPromotionDeploymentStore
}

func TestMemPromotionActivatesAndRestoresWorkloadSettings(t *testing.T) {
	testPromotionWorkloadSettings(t, state.NewMemStore(), false)
}

func TestMemPromotionRollbackRejectsConcurrentWorkloadEdit(t *testing.T) {
	testPromotionWorkloadSettings(t, state.NewMemStore(), true)
}

func testPromotionWorkloadSettings(t *testing.T, store promotionWorkloadTestStore, editTarget bool) {
	t.Helper()
	ctx := context.Background()
	account, project, releases := testProjectReleaseReadContract(t, store)
	previous := releases[len(releases)-1]
	app, err := store.AppByID(ctx, previous.Members[0].AppID)
	if err != nil {
		t.Fatal(err)
	}
	ram := 256
	productionIP := netip.MustParseAddr("198.51.100.11")
	app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &ram, SetStaticEgressIP: true, StaticEgressIP: &productionIP})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	targetSettings, err := state.MaterializeEnvironmentWorkloadSettings(ctx, store, app, "production")
	if err != nil {
		t.Fatal(err)
	}
	targetHash, err := state.WorkloadSettingsHash(targetSettings)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB, settings.CPUMillicores, settings.StartCommand = 512, 1000, "serve tested"
	settings.AppProtocol, settings.OnlyAllowDeclaredRoutes = "http2", true
	settings.PlatformTenantRequired = true
	settings.DeclaredRoutes = []state.DeclaredRoute{{Path: "/ready", Methods: []string{"GET"}}}
	settings.Manifest.Env = map[string]string{"MODE": "tested"}
	settings.PublicAuthMode, settings.PublicAuthBasicSealed = "basic", []byte("sealed-stage-credential")
	settings.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	settings.EgressPorts = []int{443}
	settings.RetryPolicyJSON = json.RawMessage(`{"max_attempts":2,"backoff_multiplier":1e0}`)
	stagingIP := netip.MustParseAddr("198.51.100.22")
	settings.StaticEgressIP = &stagingIP
	stageSpec, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	sourceRelease, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	promotion, _, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{
		AccountID: account.ID, ProjectID: project.ID, ProjectSlug: project.Slug,
		FromEnvironment: "staging", ToEnvironment: "production", Status: "running",
		PromotionHash: stageSpec.Hash, IdempotencyKey: "workload-promotion", SyncConfig: true,
		ReleaseGraphMode: true, SourceReleaseSetID: sourceRelease.ID, PreviousTargetReleaseSetID: previous.ID, ReleaseTTLSeconds: 1800,
		SourceConfigHash: api.EmptyProjectEnvironmentConfigHash(), PreviousTargetConfigHash: api.EmptyProjectEnvironmentConfigHash(),
		SourceConfigSnapshot: json.RawMessage(`{}`), PreviousTargetConfigSnapshot: json.RawMessage(`{}`),
	}, []state.ProjectEnvironmentPromotionWorkload{{WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName,
		SourceDeploymentID: source.ID, PreviousTargetDeploymentID: previous.Members[0].DeploymentID, Status: "pending"}})
	if err != nil {
		t.Fatal(err)
	}
	input := state.ProjectEnvironmentPromotionWorkloadSpecInput{PromotionID: promotion.ID, SourceDeploymentID: source.ID,
		SourceHash: stageSpec.Hash, PreviousTargetHash: targetHash}
	candidate := state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage,
		TrafficPercent: 0, TrafficPercentExplicit: true}
	bad := input
	bad.PreviousTargetHash = api.EmptyProjectEnvironmentConfigHash()
	if _, err := store.CreateDeploymentForEnvironmentPromotion(ctx, candidate, bad); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale target settings prepared: %v", err)
	}
	prepared, err := store.CreateDeploymentForEnvironmentPromotion(ctx, candidate, input)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := store.CreateDeploymentForEnvironmentPromotion(ctx, candidate, input)
	if err != nil || retried.ID != prepared.ID {
		t.Fatalf("preparation retry created a different deployment: %+v, %v", retried, err)
	}
	if _, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("preparation advanced production desired head: %v", err)
	}
	unchanged, err := store.AppByID(ctx, app.ID)
	if err != nil || unchanged.RAMMB != 256 || unchanged.PlatformTenantRequired {
		t.Fatalf("preparation changed production: RAM=%d, %v", unchanged.RAMMB, err)
	}
	pinned, err := state.AppForDeployment(ctx, store, prepared)
	if err != nil || pinned.RAMMB != 512 || pinned.StartCommand != "serve tested" || !pinned.PlatformTenantRequired || pinned.StaticEgressIP == nil || *pinned.StaticEgressIP != productionIP {
		t.Fatalf("prepared configuration = %+v, %v", pinned, err)
	}
	if err := store.MarkDeploymentLiveDark(ctx, prepared.ID); err != nil {
		t.Fatal(err)
	}
	members := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: prepared.ID}}
	active, err := store.PublishProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.PublishProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, 1800, members)
	if err != nil || again.ID != active.ID {
		t.Fatalf("publication retry = %+v, %v", again, err)
	}
	production, err := store.AppByID(ctx, app.ID)
	if err != nil || production.RAMMB != 512 || production.CPUMillicores != 1000 || production.StartCommand != "serve tested" ||
		production.AppProtocol != "http2" || !production.PlatformTenantRequired || production.Manifest.Env["MODE"] != "tested" || production.PublicAuthMode != "basic" ||
		!reflect.DeepEqual(production.PublicAuthBasicSealed, settings.PublicAuthBasicSealed) || !reflect.DeepEqual(production.EgressPorts, []int{443}) {
		t.Fatalf("production projection = %+v, %v", production, err)
	}
	priorDeployment, err := store.DeploymentByID(ctx, previous.Members[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	priorRuntime, err := state.AppForDeployment(ctx, store, priorDeployment)
	if err != nil || priorRuntime.RAMMB != 256 || priorRuntime.StartCommand == "serve tested" || priorRuntime.PlatformTenantRequired {
		t.Fatalf("retained previous release adopted promoted settings: %+v, %v", priorRuntime, err)
	}
	stage, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID)
	if err != nil || stage.ID != stageSpec.ID {
		t.Fatalf("promotion changed staging: %+v, %v", stage, err)
	}
	if editTarget {
		ram = 1024
		if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
			t.Fatal(err)
		}
		desired, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID)
		if err != nil || desired.Settings.RAMMB != 1024 {
			t.Fatalf("global edit did not advance production head: %+v, %v", desired, err)
		}
		if _, err := store.RollbackProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, 1800, previous.Members); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("rollback overwrote concurrent production edit: %v", err)
		}
		pinned, err = state.AppForDeployment(ctx, store, prepared)
		if err != nil || pinned.RAMMB != 512 {
			t.Fatalf("global edit changed immutable production release: RAM=%d, %v", pinned.RAMMB, err)
		}
		return
	}
	restored, err := store.RollbackProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, 1800, previous.Members)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := store.RollbackProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, 1800, previous.Members); err != nil || retry.ID != restored.ID {
		t.Fatalf("rollback retry = %+v, %v", retry, err)
	}
	production, err = store.AppByID(ctx, app.ID)
	if err != nil || production.RAMMB != 256 || production.StartCommand == "serve tested" || production.PublicAuthMode != app.PublicAuthMode || production.PlatformTenantRequired {
		t.Fatalf("rollback did not restore production config: %+v, %v", production, err)
	}
	if _, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rollback did not restore legacy desired authority: %v", err)
	}
	next, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID, 0, targetSettings)
	if err != nil || next.Revision <= stageSpec.Revision+1 {
		t.Fatalf("revision collided with prepared history after rollback: %+v, %v", next, err)
	}
}
