package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261029090000: the plan shows a renamed service as an update of its
// previous name's app, matching reconcile's diff, and neither removes nor
// leaves that app unaffected.
func TestComputeAffectedPartition_RenamedServiceUpdatesItsApp(t *testing.T) {
	apps := []state.App{{ID: "app-ledger", Slug: "shop-ledger", WorkloadName: "ledger", ProjectID: projectID, Status: state.AppActive}}
	scanWl := []reposcan.Workload{{Name: "accounts", RenamedFrom: "ledger"}}

	got := apidserver.ComputeAffectedPartition(scanWl, scanWl, apps, nil, projectID)

	if len(got.WillDeploy) != 1 || got.WillDeploy[0].Action != "update" || got.WillDeploy[0].ID != "app-ledger" || got.WillDeploy[0].Slug != "shop-ledger" {
		t.Fatalf("WillDeploy = %+v; want an update of shop-ledger", got.WillDeploy)
	}
	if len(got.Removed) != 0 || len(got.Unaffected) != 0 {
		t.Fatalf("renamed app listed as removed %v or unaffected %+v", got.Removed, got.Unaffected)
	}
}
