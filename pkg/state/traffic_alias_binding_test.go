// adr: 531
package state

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func testTrafficAliasWithdrawal(t *testing.T, store Store, aliases DeploymentAliasStore, account Account, app App, mode string, seed func(EdgeRule), intent func() string) {
	t.Helper()
	ctx := t.Context()
	deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := aliases.SetDeploymentAlias(ctx, app.ID, "x", deployment.ID); err != nil {
		t.Fatal(err)
	}
	label, ok := api.DeploymentAliasHostLabel(app.ID, "x")
	if !ok {
		t.Fatal("invalid alias fixture")
	}
	peerAccount, _ := trafficTenantTransitionPeer(t, store)
	peer, err := store.CreateApp(ctx, App{AccountID: peerAccount.ID, Slug: label, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	legacy := publicationLegacyRule(peerAccount, peer, label+".apps.example.test")
	legacy.Kind, legacy.Action.Kind, legacy.Action.Route = EdgeRuleKindHeaders, EdgeRuleKindHeaders, nil
	legacy.Action.Headers = &EdgeRuleHeadersAction{ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Alias-Peer", Action: "set", Value: "private"}}}
	seed(legacy)
	apply := func(ctx context.Context) error {
		switch mode {
		case "account":
			return store.DeleteAccount(ctx, account.ID)
		case "purge-app":
			return store.DeleteAppPermanently(ctx, app.ID)
		}
		return aliases.DeleteDeploymentAlias(ctx, app.ID, "x")
	}
	if mode == "account" {
		if err := store.MarkAccountDeletionPending(ctx, account.ID); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "purge-app" {
		if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := store.ClaimAppDeletion(ctx, app.ID); err != nil {
			t.Fatal(err)
		}
	}
	before := intent()
	beforeAliases, err := aliases.ListDeploymentAliases(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = apply(ctx)
	requireTrafficTenantRefusal(t, err)
	var binding *TrafficPolicyBindingError
	if !errors.As(err, &binding) {
		t.Fatalf("alias refusal lacks foreign-owner privacy marker: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := apply(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled alias withdrawal: %v", err)
	}
	afterAliases, readErr := aliases.ListDeploymentAliases(ctx, app.ID)
	if readErr != nil || !reflect.DeepEqual(beforeAliases, afterAliases) {
		t.Fatalf("refused alias withdrawal changed its mapping: %v", readErr)
	}
	if before != intent() {
		t.Fatal("refused alias withdrawal changed alias, owner or cleanup intent")
	}
	if err := store.DeleteEdgeRule(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := apply(ctx); err != nil {
		t.Fatalf("alias withdrawal after policy repair: %v", err)
	}
	rows, err := aliases.ListDeploymentAliases(ctx, app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("accepted alias withdrawal retained its reservation: %+v %v", rows, err)
	}
	if _, err := store.AppByID(ctx, peer.ID); err != nil {
		t.Fatalf("foreign primary owner was removed: %v", err)
	}
}

func TestMemTrafficAliasWithdrawal(t *testing.T) {
	for _, mode := range []string{"alias", "account", "purge-app"} {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			m.trafficAppsSuffix = ".apps.example.test"
			testTrafficAliasWithdrawal(t, m, m, account, app, mode, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficAccountIntent(t, m) })
		})
	}
}
