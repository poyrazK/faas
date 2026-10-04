// adr: 570
package state

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func testTrafficAliasPublication(t *testing.T, store Store, aliases DeploymentAliasStore, app App, mode string, seed func(string)) {
	t.Helper()
	first, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	name := "candidate"
	label, ok := api.DeploymentAliasHostLabel(app.ID, name)
	if !ok {
		t.Fatal("fixture alias label invalid")
	}
	host := label + ".apps.example.test"
	pattern := host
	wantRefusal := true
	if mode == "retarget" || mode == "new-url" {
		if _, err := aliases.SetDeploymentAlias(t.Context(), app.ID, name, first.ID); err != nil {
			t.Fatal(err)
		}
		pattern = "*"
		if mode == "new-url" {
			name = "second"
			label, _ := api.DeploymentAliasHostLabel(app.ID, name)
			host = label + ".apps.example.test"
		} else {
			wantRefusal = false
		}
	}
	if mode == "different-domain" || mode == "disabled" {
		pattern = label + ".gregale.dev"
		wantRefusal = false
	}
	second, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	seed(pattern)
	before, err := aliases.ListDeploymentAliases(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = aliases.SetDeploymentAlias(t.Context(), app.ID, name, second.ID)
	if wantRefusal {
		var aggregate *TrafficPolicyAggregateError
		if !errors.As(err, &aggregate) || aggregate.Scope != "host_rule_projection" || aggregate.Host != host {
			t.Fatalf("new alias accepted overload: %v", err)
		}
		after, readErr := aliases.ListDeploymentAliases(t.Context(), app.ID)
		if readErr != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("refused alias changed intent: before=%+v after=%+v err=%v", before, after, readErr)
		}
		if err := store.DeleteEdgeRule(t.Context(), "00000000-0000-0000-0000-000000000375"); err != nil {
			t.Fatal(err)
		}
		if _, err := aliases.SetDeploymentAlias(t.Context(), app.ID, name, second.ID); err != nil {
			t.Fatalf("alias after repair: %v", err)
		}
	} else if err != nil {
		t.Fatalf("unchanged or unserved alias projection refused: %v", err)
	}
	rows, err := aliases.ListDeploymentAliases(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Name == name && row.DeploymentID == second.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("successful alias publication was not saved")
	}
}

func TestMemTrafficAliasPublicationRollbackAndNamespace(t *testing.T) {
	for _, mode := range []string{"new", "retarget", "new-url", "different-domain", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			m.trafficAppsSuffix = ".apps.example.test"
			if mode == "disabled" {
				m.trafficAppsSuffix = ""
			}
			testTrafficAliasPublication(t, m, m, app, mode, func(pattern string) {
				in := memTrafficRule(account, app, pattern, 520)
				id := "00000000-0000-0000-0000-000000000375"
				m.edgeRules[id] = EdgeRule{ID: id, AccountID: account.ID, AppID: app.ID, MatchHost: pattern, MatchPath: "/", Enabled: true, Kind: in.Kind, Action: in.Action}
			})
		})
	}
}

func TestMemTrafficAliasLegacyLabelAndReservation(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	deployment, err := m.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("a", 40)
	legacy := DeploymentAlias{AppID: app.ID, Name: name, DeploymentID: deployment.ID}
	m.deploymentAliases[deploymentAliasKey(app.ID, name)] = legacy
	label := "tag-" + name + "-" + strings.ReplaceAll(app.ID, "-", "")
	if _, err := m.DeploymentAliasByHostLabel(t.Context(), label); err != nil {
		t.Fatalf("legacy SQL label disappeared from the routing mirror: %v", err)
	}
	view, err := m.readMemTrafficHostAnalysisLocked(t.Context(), account.ID, memTrafficPolicyChange{})
	if err != nil || len(view.AliasHosts) != 1 || view.AliasHosts[0] != label+".gregale.dev" {
		t.Fatalf("legacy routing label absent from analyzer: %+v err=%v", view.AliasHosts, err)
	}
	if _, err := m.SetDeploymentAlias(t.Context(), app.ID, name, deployment.ID); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("legacy read widened new allocation: %v", err)
	}
	label, _ = api.DeploymentAliasHostLabel(app.ID, "candidate")
	// A historical slug still reserves its key, matching the SQL collision read.
	m.apps["legacy-tombstone"] = App{ID: "legacy-tombstone", AccountID: account.ID, Slug: label, Status: AppDeleted}
	if _, err := m.SetDeploymentAlias(t.Context(), app.ID, "candidate", deployment.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("tombstone slug reservation was stolen: %v", err)
	}
}
