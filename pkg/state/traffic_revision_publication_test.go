// adr: 375
package state

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

func revisionPublicationLegacyRule(account Account, app App, host string) EdgeRule {
	rule := publicationLegacyRule(account, app, host)
	rule.Kind, rule.Action.Kind, rule.Action.Route = EdgeRuleKindHeaders, EdgeRuleKindHeaders, nil
	rule.Action.Headers = &EdgeRuleHeadersAction{ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Revision-Policy", Action: "set", Value: "private"}}}
	return rule
}

func requireRevisionPublicationRefusal(t *testing.T, err error, host string) {
	t.Helper()
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Scope != "host_rule_projection" || aggregate.Host != host || aggregate.Observed <= aggregate.Limit {
		t.Fatalf("new revision hostname accepted an oversized policy: %v", err)
	}
}

func testTrafficRevisionCreation(t *testing.T, store Store, account Account, app App, activity bool, seed func(EdgeRule), intent func() string) {
	t.Helper()
	prior, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	host := fmt.Sprintf("deploy-%d-%s%s", prior.Revision+1, app.Slug, hostidentity.DeployWildcardSuffix)
	legacy := revisionPublicationLegacyRule(account, app, host)
	seed(legacy)
	entry := trafficAppBindingActivity(account)
	entry.OrgID, entry.Kind = uuid.New(), "deploy.requested"
	entry.ResourceID, entry.ResourceLabel = app.ID, app.Slug
	create := func(ctx context.Context) (Deployment, int64, error) {
		candidate := Deployment{AppID: app.ID, Kind: DeploymentKindImage}
		if activity {
			return store.(OrgActivityDeploymentMutationStore).CreateDeploymentWithActivity(ctx, candidate, entry)
		}
		row, err := store.CreateDeployment(ctx, candidate)
		return row, 0, err
	}
	before := intent()
	created, receipt, err := create(t.Context())
	requireRevisionPublicationRefusal(t, err, host)
	if created.ID != "" || receipt != 0 {
		t.Fatal("refused revision creation returned a row or receipt")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := create(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled revision creation: %v", err)
	}
	if before != intent() {
		t.Fatal("refused revision creation changed pending row, policy, cleanup or activity")
	}
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	created, receipt, err = create(t.Context())
	if err != nil || created.Revision != prior.Revision+1 || activity && receipt == 0 {
		t.Fatalf("revision creation after repair: %+v receipt=%d err=%v", created, receipt, err)
	}
	previous, err := store.DeploymentByID(t.Context(), prior.ID)
	if err != nil || previous.Status != DeploySuperseded || previous.TrafficPercent != 0 {
		t.Fatalf("accepted creation did not supersede prior pending row: %+v %v", previous, err)
	}
}

func TestMemTrafficRevisionCreationPublication(t *testing.T) {
	for _, activity := range []bool{false, true} {
		t.Run(fmt.Sprintf("activity=%v", activity), func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			testTrafficRevisionCreation(t, m, account, app, activity, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficAppIntent(t, m) })
		})
	}
}

var trafficRevisionRevivalModes = []string{"pending", "building", "imaging", "snapshotting", "live", "mark-live", "superseded-retry", "unchanged", "dark", "canceled"}

func testTrafficRevisionRevival(t *testing.T, store Store, account Account, app App, mode string, seed func(EdgeRule), intent func() string) {
	t.Helper()
	candidate := Deployment{AppID: app.ID, Kind: DeploymentKindImage}
	if mode == "dark" {
		candidate.TrafficPercent, candidate.TrafficPercentExplicit = 0, true
	}
	deployment, err := store.CreateDeployment(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "unchanged" && mode != "dark" && mode != "canceled" {
		status := DeployFailed
		if mode == "superseded-retry" {
			status = DeploySuperseded
		}
		if err := store.UpdateDeploymentStatus(t.Context(), deployment.ID, status, "withdrawn"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateCron(t.Context(), app.ID, "*/5 * * * *", "/job", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(CronSuspensionStore).SuspendCronsForApp(t.Context(), app.ID, CronSuspendedNoLiveDeployment); err != nil {
		t.Fatal(err)
	}
	host := hostidentity.BuildDeploymentHost(hostidentity.DeployWildcardSuffix, deployment.Revision, app.Slug)
	legacy := revisionPublicationLegacyRule(account, app, host)
	seed(legacy)
	before := intent()
	apply := func(ctx context.Context) error {
		switch mode {
		case "mark-live":
			return store.MarkDeploymentLive(ctx, deployment.ID)
		case "dark":
			return store.(ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(ctx, deployment.ID)
		case "superseded-retry", "unchanged", "canceled":
			return store.UpdateDeploymentStatus(ctx, deployment.ID, DeployImaging, "retry")
		default:
			return store.UpdateDeploymentStatus(ctx, deployment.ID, DeploymentStatus(mode), "retry")
		}
	}
	if mode == "unchanged" || mode == "dark" {
		if err := apply(t.Context()); err != nil {
			t.Fatalf("unchanged revision lost its legacy repair allowance: %v", err)
		}
		return
	}
	if mode == "canceled" {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := apply(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled revision write: %v", err)
		}
	} else {
		requireRevisionPublicationRefusal(t, apply(t.Context()), host)
	}
	if before != intent() {
		t.Fatal("refused revision revival changed deployment, cron, snapshot, activity or webhook intent")
	}
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := apply(t.Context()); err != nil {
		t.Fatalf("revision revival after repair: %v", err)
	}
	current, err := store.DeploymentByID(t.Context(), deployment.ID)
	if err != nil || !current.DeploymentPreviewActive() {
		t.Fatalf("repaired revision remains ineligible: %+v %v", current, err)
	}
}

func memTrafficRevisionIntent(t *testing.T, m *MemStore) string {
	t.Helper()
	return trafficTenantIntentDigest(t, []any{memTrafficAppIntent(t, m), m.deploymentAliases, m.openAPISnapshots, m.appWebhookEventOutbox, m.appWebhookDeliveries})
}

func TestMemTrafficRevisionRevivalPublication(t *testing.T) {
	registerTrafficRevivalCapture(t)
	for _, mode := range trafficRevisionRevivalModes {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			testTrafficRevisionRevival(t, m, account, app, mode, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficRevisionIntent(t, m) })
		})
	}
}

func testTrafficRevisionAppPublication(t *testing.T, store Store, account Account, app App, mode string, seed func(EdgeRule), intent func() string) {
	t.Helper()
	deployment, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	slug := app.Slug
	switch mode {
	case "restore":
		if _, err := store.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	case "visibility":
		internal := api.AppVisibilityInternal
		if _, err := store.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &internal}); err != nil {
			t.Fatal(err)
		}
	case "rename":
		slug = "revision-renamed-web"
	}
	host := hostidentity.BuildDeploymentHost(hostidentity.DeployWildcardSuffix, deployment.Revision, slug)
	legacy := revisionPublicationLegacyRule(account, app, host)
	seed(legacy)
	apply := func() error {
		switch mode {
		case "restore":
			_, err := store.RestoreApp(t.Context(), app.ID, api.MustLimitsFor(account.Plan))
			return err
		case "visibility":
			public := api.AppVisibilityPublic
			_, err := store.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &public})
			return err
		default:
			_, err := store.RenameApp(t.Context(), account.ID, app.Slug, slug)
			return err
		}
	}
	before := intent()
	requireRevisionPublicationRefusal(t, apply(), host)
	if before != intent() {
		t.Fatal("refused app change published a revision URL or retained side effects")
	}
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := apply(); err != nil {
		t.Fatalf("app publication after revision policy repair: %v", err)
	}
}

func TestMemTrafficRevisionAppPublication(t *testing.T) {
	for _, mode := range []string{"restore", "visibility", "rename"} {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			testTrafficRevisionAppPublication(t, m, account, app, mode, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficRevisionIntent(t, m) })
		})
	}
}

func testTrafficAliasRevivalHiddenPrimary(t *testing.T, store Store, aliases DeploymentAliasStore, account Account, app App, seed func(EdgeRule), intent func() string) {
	t.Helper()
	deployment, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := aliases.SetDeploymentAlias(t.Context(), app.ID, "candidate", deployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), deployment.ID, DeployFailed, "withdrawn"); err != nil {
		t.Fatal(err)
	}
	label, _ := api.DeploymentAliasHostLabel(app.ID, "candidate")
	shadow, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: label, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	host := label + ".apps.example.test"
	legacy := revisionPublicationLegacyRule(account, shadow, host)
	seed(legacy)
	before := intent()
	mapping, err := aliases.ListDeploymentAliases(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireRevisionPublicationRefusal(t, store.UpdateDeploymentStatus(t.Context(), deployment.ID, DeployImaging, "retry"), host)
	after, err := aliases.ListDeploymentAliases(t.Context(), app.ID)
	if err != nil || before != intent() || !reflect.DeepEqual(mapping, after) {
		t.Fatal("refused alias revival changed target or raw reservation")
	}
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), deployment.ID, DeployImaging, "retry"); err != nil {
		t.Fatalf("alias revival after hidden primary policy repair: %v", err)
	}
}

func TestMemTrafficAliasRevivalHiddenPrimary(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	m.trafficAppsSuffix = ".apps.example.test"
	testTrafficAliasRevivalHiddenPrimary(t, m, m, account, app, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficRevisionIntent(t, m) })
}

func TestMemTrafficRevisionMetadataEligibility(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	m.trafficAppsSuffix = "" // Immutable URLs have their own production suffix.
	deployment, err := m.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	host := hostidentity.BuildDeploymentHost(hostidentity.DeployWildcardSuffix, deployment.Revision, app.Slug)
	for _, status := range []DeploymentStatus{DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting, DeployLive, DeploySuperseded, DeployFailed, DeployCancelled} {
		t.Run(string(status), func(t *testing.T) {
			deployment.Status = status
			m.deployments[deployment.ID] = deployment
			view, err := m.readMemTrafficHostAnalysisLocked(t.Context(), account.ID, memTrafficPolicyChange{})
			if err != nil || slices.Contains(view.RevisionHosts, host) != deployment.DeploymentPreviewActive() || len(view.PrimaryHosts) != 0 {
				t.Fatalf("revision metadata/runtime disagreement: %+v %v", view.RevisionHosts, err)
			}
		})
	}
	deployment.Status = DeployPending
	m.deployments[deployment.ID] = deployment
	for _, mode := range []string{"zero-revision", "deleted-target", "deleted-owner", "internal-owner", "invalid-slug"} {
		t.Run(mode, func(t *testing.T) {
			dep, owner := deployment, app
			now := time.Now()
			switch mode {
			case "zero-revision":
				dep.Revision = 0
			case "deleted-target":
				dep.DeletedAt = &now
			case "deleted-owner":
				owner.DeletedAt = &now
			case "internal-owner":
				owner.Visibility = api.AppVisibilityInternal
			case "invalid-slug":
				owner.Slug = "legacy.invalid"
			}
			view, err := m.readMemTrafficHostAnalysisLocked(t.Context(), account.ID, memTrafficPolicyChange{Apps: map[string]App{app.ID: owner}, Deployments: map[string]Deployment{dep.ID: dep}})
			if err != nil || len(view.RevisionHosts) != 0 {
				t.Fatalf("unservable revision retained an allowance: %+v %v", view.RevisionHosts, err)
			}
		})
	}
}

func TestTrafficRevisionOrdinaryCompilerAndPublicationAllowance(t *testing.T) {
	host := "deploy-42-web" + hostidentity.DeployWildcardSuffix
	view := trafficHostAnalysis{RevisionHosts: []string{host}, Groups: []trafficHostGroup{
		{App: "sibling", Pattern: host, Kind: string(EdgeRuleKindCORSA), Preset: "shared", Rows: 1, Canonical: 10, Compiled: 10},
		{App: "target", Pattern: host, Kind: string(EdgeRuleKindCORSA), Preset: "shared", Rows: 1, Canonical: 10, Compiled: 10},
	}, Assets: []trafficHostAsset{{ID: "shared", Compiled: api.TrafficPolicyMaxHostBytes - 100}}}
	if err := checkTrafficHostAnalysis(t.Context(), trafficHostAnalysis{}, view); err != nil {
		t.Fatalf("shared preset was charged more than once: %v", err)
	}
	view.Assets[0].Compiled = api.TrafficPolicyMaxHostBytes
	before := view
	before.RevisionHosts = nil
	var aggregate *TrafficPolicyAggregateError
	if err := checkTrafficHostAnalysis(t.Context(), before, view); !errors.As(err, &aggregate) || aggregate.Scope != "host_compiled_projection_estimate" || aggregate.Host != host {
		t.Fatalf("new revision inherited unserved selector allowance or excluded sibling policy: %v", err)
	}
	if err := checkTrafficHostAnalysis(t.Context(), view, view); err != nil {
		t.Fatalf("unchanged revision lost repair allowance: %v", err)
	}
}
