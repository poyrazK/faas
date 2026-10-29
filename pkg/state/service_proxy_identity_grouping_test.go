package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// The cheap path must group instance rows exactly as the managed-Job query
// does (Hunt 14 H14-33): at most two app owners, and a deployment only when
// every row of an owner names the same one.
func TestLiveServiceProxyIdentitiesGroupsLikeTheQuery(t *testing.T) {
	row := func(app, deployment string) sqlc.LiveServiceProxyInstanceRowsByHostIPRow {
		return sqlc.LiveServiceProxyInstanceRowsByHostIPRow{AppID: app, DeploymentID: deployment}
	}
	for _, tc := range []struct {
		name string
		rows []sqlc.LiveServiceProxyInstanceRowsByHostIPRow
		want []state.Instance
	}{
		{"one owner", []sqlc.LiveServiceProxyInstanceRowsByHostIPRow{row("a", "d1")}, []state.Instance{{AppID: "a", DeploymentID: "d1"}}},
		{"one owner, one deployment twice", []sqlc.LiveServiceProxyInstanceRowsByHostIPRow{row("a", "d1"), row("a", "d1")}, []state.Instance{{AppID: "a", DeploymentID: "d1"}}},
		{"overlapping deployments hide the deployment", []sqlc.LiveServiceProxyInstanceRowsByHostIPRow{row("a", "d1"), row("a", "d2")}, []state.Instance{{AppID: "a"}}},
		{"a row without a deployment", []sqlc.LiveServiceProxyInstanceRowsByHostIPRow{row("a", "d1"), row("a", "")}, []state.Instance{{AppID: "a"}}},
		{"two deployments cannot hide a second owner", []sqlc.LiveServiceProxyInstanceRowsByHostIPRow{row("a", "d1"), row("a", "d2"), row("b", "d3")}, []state.Instance{{AppID: "a"}, {AppID: "b", DeploymentID: "d3"}}},
		{"at most two owners", []sqlc.LiveServiceProxyInstanceRowsByHostIPRow{row("a", "d1"), row("b", "d2"), row("c", "d3")}, []state.Instance{{AppID: "a", DeploymentID: "d1"}, {AppID: "b", DeploymentID: "d2"}}},
		{"no live instance", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := state.LiveServiceProxyIdentities(tc.rows, "10.100.0.5", "node-1")
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i].AppID != tc.want[i].AppID || got[i].DeploymentID != tc.want[i].DeploymentID || got[i].HostIP != "10.100.0.5" || got[i].NodeID != "node-1" {
					t.Fatalf("row %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
