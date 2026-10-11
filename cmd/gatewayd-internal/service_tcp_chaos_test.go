package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceTCPChaosResolverRevalidatesRunMembership(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tcp-chaos@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour)
	create := func(slug string, preview bool) state.App {
		t.Helper()
		app := state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 128}
		if preview {
			app.PreviewOfSlug = slug
			app.PreviewPrState = state.PreviewPrStateOpen
			app.PreviewExpiresAt = &expiry
		}
		out, err := store.CreateApp(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	caller := create("dev-tcp-caller", true)
	target := create("dev-tcp-cache", true)
	other := create("dev-tcp-other", true)
	production := create("production-cache", false)
	const runID = "0123456789abcdef0123456789abcdef"
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, runID, []state.ScenarioTestMember{{Workload: "checkout", AppID: caller.ID}, {Workload: "cache", AppID: target.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []state.ScenarioTestMember{{Workload: "cache", AppID: other.ID}}); err != nil {
		t.Fatal(err)
	}
	rule := chaos.Rule{From: "checkout", To: "cache", Kind: chaos.KindTCPTimeout, Port: 6379, Percent: 100}
	if _, err := store.SetScenarioTestChaosPlan(ctx, account.ID, runID, chaos.Plan{DurationMS: 30000, Rules: []chaos.Rule{rule}}); err != nil {
		t.Fatal(err)
	}
	resolve := newServiceTCPChaosResolver(store)
	lease, err := resolve(ctx, caller.ID, target.ID)
	if err != nil || len(lease.Rules) != 1 || lease.Rules[0] != rule {
		t.Fatalf("lease = %+v, %v", lease, err)
	}
	for _, pair := range [][2]string{{caller.ID, other.ID}, {caller.ID, production.ID}, {production.ID, target.ID}} {
		if _, err := resolve(ctx, pair[0], pair[1]); !errors.Is(err, gateway.ErrServiceProxyDenied) {
			t.Fatalf("escaped test namespace: %v", err)
		}
	}
	index, err := store.AppServiceAddressIndex(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	address, ok := api.ServiceAddressForIndex(index)
	if !ok {
		t.Fatal("missing service address")
	}
	tcpTarget, routable, err := newServiceTCPTargetResolver(store)(ctx, caller.ID, address)
	if err != nil || !routable || tcpTarget.ScenarioTestRunID != runID || tcpTarget.ScenarioWorkload != "cache" {
		t.Fatalf("TCP scenario target = %+v, %v, %v", tcpTarget, routable, err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(ctx, caller.ID, target.ID); err == nil {
		t.Fatal("deleted test target retained its policy")
	}
}
