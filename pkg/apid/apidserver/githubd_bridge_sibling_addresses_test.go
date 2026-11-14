// adr: 20261105104500
package apidserver_test

import (
	"context"
	"maps"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A push's sibling addresses are set on the app when the scope has no value
// for them; a value already set wins, and anything that is not a Gregale
// service address is dropped.
func TestPushSiblingAddressesSetOnlyUnsetAddresses(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "sibling@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "web", WorkloadName: "web", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, state.DefaultEnvScope, "AUTH_URL", "https://auth.example.com"); err != nil {
		t.Fatal(err)
	}
	written, err := apidserver.ImportPushSiblingAddresses(ctx, store, acct, app, "", map[string]string{
		"BILLING_URL": "http://billing.svc.gregale:10081/v1",
		"AUTH_URL":    "http://auth.svc.gregale:10081",
		"EVIL_URL":    "http://attacker.example.com",
		"SECURE_URL":  "https://orders.internal/rpc",
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListAppEnvInScope(ctx, acct.ID, app.ID, state.DefaultEnvScope)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, row := range rows {
		got[row.Key] = row.Value
	}
	want := map[string]string{
		"AUTH_URL":    "https://auth.example.com",
		"BILLING_URL": "http://billing.svc.gregale:10081/v1",
		"SECURE_URL":  "https://orders.internal/rpc",
	}
	if !maps.Equal(got, want) || len(written) != 2 {
		t.Fatalf("env = %v (written %v), want %v", got, written, want)
	}
}
