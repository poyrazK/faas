// adr: 570
package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// Rollback writers make a withdrawn revision URL serve again, and a retry
// takes a new revision. Each is a newly exposed binding with no legacy
// allowance, refused without changing intent until the policy is repaired.
var trafficRollbackRevivalModes = []string{"prepare", "checked", "auto", "retry"}

func testTrafficRollbackRevival(t *testing.T, store Store, account Account, app App, mode string, seed func(EdgeRule), intent func() string) {
	t.Helper()
	live := func() Deployment {
		t.Helper()
		row, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(t.Context(), row.ID); err != nil {
			t.Fatal(err)
		}
		return row
	}
	var target Deployment
	var host string
	var apply func(context.Context) (string, error)
	if mode == "retry" {
		failed, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), failed.ID, DeployFailed, "build failed"); err != nil {
			t.Fatal(err)
		}
		host = hostidentity.BuildDeploymentHost(hostidentity.DeployWildcardSuffix, failed.Revision+1, app.Slug)
		apply = func(ctx context.Context) (string, error) {
			retry, err := store.RetryDeploymentFromStage(ctx, failed.ID, StageSourceDownload)
			return retry.ID, err
		}
	} else {
		target = live()
		current := live()
		if previous, err := store.DeploymentByID(t.Context(), target.ID); err != nil || previous.DeploymentPreviewActive() {
			t.Fatalf("fixture rollback target still serves its revision URL: %+v %v", previous, err)
		}
		host = hostidentity.BuildDeploymentHost(hostidentity.DeployWildcardSuffix, target.Revision, app.Slug)
		apply = func(ctx context.Context) (string, error) {
			switch mode {
			case "prepare":
				prepared, err := store.PrepareDeploymentRollback(ctx, app.ID, target.ID)
				return prepared.ID, err
			case "checked":
				operation, err := store.(CheckedRollbackStore).CreateCheckedRollback(ctx, account.ID, app.ID, target.ID, current.ID, "incident")
				return operation.TargetDeploymentID, err
			default:
				return store.AutoRollbackDeploymentsTx(ctx, app.ID, current.ID)
			}
		}
	}
	legacy := revisionPublicationLegacyRule(account, app, host)
	seed(legacy)
	before := intent()
	_, err := apply(t.Context())
	requireRevisionPublicationRefusal(t, err, host)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := apply(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled %s: %v", mode, err)
	}
	if before != intent() {
		t.Fatalf("refused %s changed deployment, rollback or activity intent", mode)
	}
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	id, err := apply(t.Context())
	if err != nil || id == "" {
		t.Fatalf("%s after repair: id=%q err=%v", mode, id, err)
	}
	revived, err := store.DeploymentByID(t.Context(), id)
	if err != nil || !revived.DeploymentPreviewActive() || hostidentity.BuildDeploymentHost(hostidentity.DeployWildcardSuffix, revived.Revision, app.Slug) != host {
		t.Fatalf("%s after repair does not serve %s: %+v %v", mode, host, revived, err)
	}
}

func memTrafficRollbackIntent(t *testing.T, m *MemStore) string {
	t.Helper()
	return trafficTenantIntentDigest(t, []any{memTrafficRevisionIntent(t, m), m.checkedRollbacks, m.rollbackPrepared,
		m.runtimeUpgradeTargets, m.runtimeUpgradeBaselines, m.deploymentDependencyGates, m.publicBuildURLs})
}

func TestMemTrafficRollbackRevivalPublication(t *testing.T) {
	for _, mode := range trafficRollbackRevivalModes {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			testTrafficRollbackRevival(t, m, account, app, mode, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficRollbackIntent(t, m) })
		})
	}
}
