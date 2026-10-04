// adr: 570
package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var trafficAliasRevivalModes = []string{
	"status-pending", "status-building", "status-imaging", "status-snapshotting", "status-live", "status-superseded",
	"live-stable", "live-manual", "live-service", "live-canary", "unchanged", "cancelled", "dark", "canceled-analysis",
}

func registerTrafficRevivalCapture(t *testing.T) {
	t.Helper()
	RegisterOpenAPICapture(func(_ context.Context, _ sqlc.DBTX, deploymentID, appID, scope string, _ []api.CreateEdgeRuleRequest, _ []byte) (OpenAPISnapshot, error) {
		return OpenAPISnapshot{DeploymentID: deploymentID, AppID: appID, Scope: scope,
			Snapshot: json.RawMessage(`{"openapi":"3.0.3","info":{"title":"revival","version":"1"},"paths":{}}`),
			SHA256:   strings.Repeat("a", 64), SchemaVersion: 1}, nil
	})
	t.Cleanup(func() { RegisterOpenAPICapture(nil) })
}

func testTrafficAliasRevival(t *testing.T, store Store, aliases DeploymentAliasStore, app App, mode string,
	seed func(Deployment, App, string), intent func() string) {
	t.Helper()
	source, err := store.CreateApp(t.Context(), App{AccountID: app.AccountID, Slug: "alias-revival-source", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployLive, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	candidate := Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployBuilding, TrafficPercent: 100}
	switch mode {
	case "live-manual":
		candidate.TrafficPercent, candidate.TrafficPercentExplicit = 10, true
	case "live-service":
		candidate.RolloutState = "rolling_out"
	case "live-canary":
		candidate.TrafficPercent, candidate.CanaryTotalSteps = 10, 3
	case "dark":
		candidate.Status, candidate.TrafficPercent, candidate.TrafficPercentExplicit = DeployPending, 0, true
	}
	candidate, err = store.CreateDeployment(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := aliases.SetDeploymentAlias(t.Context(), app.ID, "candidate", candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCron(t.Context(), app.ID, "*/5 * * * *", "/job", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(CronSuspensionStore).SuspendCronsForApp(t.Context(), app.ID, CronSuspendedNoLiveDeployment); err != nil {
		t.Fatal(err)
	}
	label, _ := api.DeploymentAliasHostLabel(app.ID, "candidate")
	host := label + ".apps.example.test"
	seed(candidate, source, host)
	before := intent()
	apply := func() error {
		if mode == "canceled-analysis" {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return store.UpdateDeploymentStatus(ctx, candidate.ID, DeployImaging, "retry")
		}
		if strings.HasPrefix(mode, "status-") {
			if mode == "status-superseded" {
				return store.MarkDeploymentSuperseded(t.Context(), candidate.ID)
			}
			return store.UpdateDeploymentStatus(t.Context(), candidate.ID, DeploymentStatus(strings.TrimPrefix(mode, "status-")), "retry")
		}
		if mode == "unchanged" || mode == "cancelled" {
			return store.UpdateDeploymentStatus(t.Context(), candidate.ID, DeployImaging, "retry")
		}
		if mode == "dark" {
			return store.(ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(t.Context(), candidate.ID)
		}
		return store.MarkDeploymentLive(t.Context(), candidate.ID)
	}
	err = apply()
	switch mode {
	case "unchanged", "dark":
		if err != nil {
			t.Fatalf("already serving alias lost its repair baseline: %v", err)
		}
		return
	case "cancelled":
		if !errors.Is(err, ErrInvalidStateTransition) {
			t.Fatalf("cancelled deployment revived: %v", err)
		}
	case "canceled-analysis":
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled positive status write persisted: %v", err)
		}
	default:
		var aggregate *TrafficPolicyAggregateError
		if !errors.As(err, &aggregate) || aggregate.Scope != "host_rule_projection" || aggregate.Host != host || aggregate.Observed <= aggregate.Limit {
			t.Fatalf("alias revival accepted overload: %v", err)
		}
	}
	if after := intent(); after != before {
		t.Fatalf("refused revival changed deployment/alias/cron/snapshot/activity/webhook intent\nbefore=%s\nafter=%s", before, after)
	}
	if mode == "cancelled" {
		return
	}
	if mode == "canceled-analysis" {
		if err := store.UpdateDeploymentStatus(t.Context(), candidate.ID, DeployImaging, "retry"); err != nil {
			t.Fatalf("fresh retry after canceled status write: %v", err)
		}
		return
	}
	if err := store.DeleteEdgeRule(t.Context(), "00000000-0000-0000-0000-000000000376"); err != nil {
		t.Fatal(err)
	}
	if err := apply(); err != nil {
		t.Fatalf("alias revival after policy repair: %v", err)
	}
	deployment, err := store.DeploymentByID(t.Context(), candidate.ID)
	if err != nil || !deployment.DeploymentAliasActive() {
		t.Fatalf("repaired target remained ineligible: status=%s err=%v", deployment.Status, err)
	}
}

func TestMemTrafficAliasDeploymentRevivalRollback(t *testing.T) {
	registerTrafficRevivalCapture(t)
	for _, mode := range trafficAliasRevivalModes {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			m.trafficAppsSuffix = ".apps.example.test"
			testTrafficAliasRevival(t, m, m, app, mode, func(candidate Deployment, source App, host string) {
				if mode != "unchanged" && mode != "dark" && mode != "canceled-analysis" {
					candidate.Status = DeployFailed
					if mode == "cancelled" {
						candidate.Status = DeployCancelled
					}
					m.deployments[candidate.ID] = candidate
				}
				in := memTrafficRule(account, source, host, 520)
				id := "00000000-0000-0000-0000-000000000376"
				m.edgeRules[id] = EdgeRule{ID: id, AccountID: account.ID, AppID: source.ID, MatchHost: host, MatchPath: "/", Enabled: true, Kind: in.Kind, Action: in.Action}
			}, func() string {
				encoded, err := json.Marshal(map[string]any{"deployments": m.deployments, "aliases": m.deploymentAliases,
					"crons": m.crons, "snapshots": m.openAPISnapshots, "activity": m.orgActivityOutbox,
					"webhooks": m.appWebhookEventOutbox, "deliveries": m.appWebhookDeliveries})
				if err != nil {
					t.Fatal(err)
				}
				return string(encoded)
			})
		})
	}
}
