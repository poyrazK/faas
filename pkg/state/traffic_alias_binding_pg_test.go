//go:build !no_pg

// adr: 531
package state

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgTrafficAliasWithdrawal(t *testing.T) {
	for _, mode := range []string{"alias", "account", "purge-app"} {
		t.Run(mode, func(t *testing.T) {
			_, pool, account, app := trafficHostPGFixture(t)
			store := NewPgStore(pool, WithTrafficAppsDomain("apps.example.test"))
			testTrafficAliasWithdrawal(t, store, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAccountIntent(t, pool) })
		})
	}
}

func TestPgTrafficAliasBindingMetadataRetainsReservationsAndBounds(t *testing.T) {
	_, pool, account, app := trafficHostPGFixture(t)
	store := NewPgStore(pool, WithTrafficAppsDomain("apps.example.test"))
	deployment, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetDeploymentAlias(t.Context(), app.ID, "x", deployment.ID); err != nil {
		t.Fatal(err)
	}
	label, _ := api.DeploymentAliasHostLabel(app.ID, "x")
	peerAccount, _ := trafficTenantTransitionPeer(t, store)
	peer, err := store.CreateApp(t.Context(), App{AccountID: peerAccount.ID, Slug: label, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), deployment.ID, DeployFailed, "private-build-failure"); err != nil {
		t.Fatal(err)
	}
	claims, err := readTrafficBindingClaims(t.Context(), pool, store.trafficAppsSuffix)
	want := label + store.trafficAppsSuffix
	if err != nil || len(claims.Aliases) != 1 || claims.Aliases[0] != (trafficNamedHostClaim{Host: want, App: app.ID, Account: account.ID}) || len(claims.Primaries) != 1 || claims.Primaries[0] != (trafficNamedHostClaim{Host: want, App: peer.ID, Account: peer.AccountID}) {
		t.Fatalf("binding discovery lost raw reservation or hidden owner: %+v %v", claims, err)
	}
	for _, params := range []sqlc.ReadTrafficBindingClaimsParams{
		{AppsSuffix: store.trafficAppsSuffix, MaxInputs: 1, MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes},
		{AppsSuffix: store.trafficAppsSuffix, MaxInputs: api.TrafficPolicyMaxAnalysisInputs, MaxBytes: 1},
	} {
		row, err := sqlc.New().ReadTrafficBindingClaims(t.Context(), pool, params)
		if err != nil || len(row.Data) != 0 || row.Inputs < 2 || row.Bytes <= 1 {
			t.Fatalf("oversized alias metadata crossed scalar bounds: %+v %v", row, err)
		}
	}
	row, err := sqlc.New().ReadTrafficBindingClaims(t.Context(), pool, sqlc.ReadTrafficBindingClaimsParams{AppsSuffix: store.trafficAppsSuffix, MaxInputs: api.TrafficPolicyMaxAnalysisInputs, MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes})
	if err != nil || len(row.Data) == 0 || strings.Contains(string(row.Data), "private-build-failure") || strings.Contains(string(row.Data), "DeploymentID") {
		t.Fatalf("binding metadata loaded target or failure body: %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readTrafficBindingClaims(canceled, pool, store.trafficAppsSuffix); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled binding discovery: %v", err)
	}
}
