package main

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceDNSChaosResolverKeepsSameNamedWorkloadsInTheirRun(t *testing.T) {
	f := newServiceAddressDNSFixture(t)
	accountApp := f.app("service-dns-chaos@example.com", "service-dns-seed")
	expiry := time.Now().Add(time.Hour)
	preview := func(slug string) state.App {
		t.Helper()
		app, err := f.store.CreateApp(f.ctx, state.App{
			AccountID: accountApp.AccountID, Slug: slug, Type: state.AppTypeApp, Runtime: "node22",
			RAMMB: 128, MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: slug,
			PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry,
		})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	callerA, cacheA := preview("service-dns-caller-a"), preview("service-dns-cache-a")
	callerB, cacheB := preview("service-dns-caller-b"), preview("service-dns-cache-b")
	outsider := preview("service-dns-outsider")
	runA, runB := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, registration := range []struct {
		runID  string
		caller state.App
		cache  state.App
	}{
		{runID: runA, caller: callerA, cache: cacheA},
		{runID: runB, caller: callerB, cache: cacheB},
	} {
		if err := f.store.RegisterScenarioTestMembers(f.ctx, callerA.AccountID, registration.runID, []state.ScenarioTestMember{
			{Workload: "checkout", AppID: registration.caller.ID}, {Workload: "cache", AppID: registration.cache.ID},
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, plan := range []struct {
		runID string
		kind  string
	}{
		{runID: runA, kind: chaos.KindDNSServFail},
		{runID: runB, kind: chaos.KindDNSNXDomain},
	} {
		if _, err := f.store.SetScenarioTestChaosPlan(f.ctx, callerA.AccountID, plan.runID, chaos.Plan{
			DurationMS: 60_000,
			Rules:      []chaos.Rule{{From: "checkout", To: "cache", Kind: plan.kind, Percent: 100}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	f.start(callerA, "10.100.0.1")
	f.start(callerB, "10.100.0.2")
	f.start(outsider, "10.100.0.3")
	resolve := newServiceDNSChaosResolver(f.store, f.node.Name)
	for _, tc := range []struct {
		remote string
		runID  string
		caller string
		want   string
	}{
		{remote: "10.100.0.1:53", runID: runA, caller: callerA.ID, want: chaos.KindDNSServFail},
		{remote: "10.100.0.2:53", runID: runB, caller: callerB.ID, want: chaos.KindDNSNXDomain},
	} {
		policy, err := resolve(f.ctx, tc.remote, "cache")
		if err != nil || policy.RunID != tc.runID || policy.CallerAppID != tc.caller ||
			len(policy.Lease.Rules) != 1 || policy.Lease.Rules[0].Kind != tc.want {
			t.Fatalf("resolve(%s) = (%+v, %v), want run %s kind %s", tc.remote, policy, err, tc.runID, tc.want)
		}
	}
	policy, err := resolve(f.ctx, "10.100.0.3:53", "cache")
	if err != nil || policy.RunID != "" || len(policy.Lease.Rules) != 0 {
		t.Fatalf("unregistered caller policy = (%+v, %v), want no chaos", policy, err)
	}
}

func TestServiceDNSChaosResolverRejectsAmbiguousLiveSourceIP(t *testing.T) {
	f := newServiceAddressDNSFixture(t)
	accountApp := f.app("service-dns-ambiguous@example.com", "service-dns-seed")
	expiry := time.Now().Add(time.Hour)
	preview := func(slug string) state.App {
		t.Helper()
		app, err := f.store.CreateApp(f.ctx, state.App{
			AccountID: accountApp.AccountID, Slug: slug, Type: state.AppTypeApp, Runtime: "node22",
			RAMMB: 128, MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: slug,
			PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry,
		})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	caller, cache, other := preview("service-dns-caller"), preview("service-dns-cache"), preview("service-dns-other")
	runID := "dddddddddddddddddddddddddddddddd"
	if err := f.store.RegisterScenarioTestMembers(f.ctx, caller.AccountID, runID, []state.ScenarioTestMember{
		{Workload: "checkout", AppID: caller.ID}, {Workload: "cache", AppID: cache.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetScenarioTestChaosPlan(f.ctx, caller.AccountID, runID, chaos.Plan{
		DurationMS: 60_000,
		Rules:      []chaos.Rule{{From: "checkout", To: "cache", Kind: chaos.KindDNSNXDomain, Percent: 100}},
	}); err != nil {
		t.Fatal(err)
	}
	f.start(caller, "10.100.0.4")
	f.start(other, "10.100.0.4")
	resolve := newServiceDNSChaosResolver(f.store, f.node.Name)
	if policy, err := resolve(f.ctx, "10.100.0.4:53", "cache"); err != nil || policy.RunID != "" || len(policy.Lease.Rules) != 0 {
		t.Fatalf("ambiguous source address policy = (%+v, %v), want no chaos", policy, err)
	}
}
