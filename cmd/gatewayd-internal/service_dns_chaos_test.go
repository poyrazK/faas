package main

import (
	"context"
	"errors"
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

type dnsChaosPolicyStoreFunc func(context.Context, string, string, string) (state.ScenarioTestDNSChaosPolicy, error)

func (f dnsChaosPolicyStoreFunc) ScenarioTestDNSChaosByHostIP(ctx context.Context, nodeName, hostIP, target string) (state.ScenarioTestDNSChaosPolicy, error) {
	return f(ctx, nodeName, hostIP, target)
}

// A private name that cannot be a workload has no policy: it must not count as
// an unavailable store, which would log and pause chaos for the whole node.
func TestServiceDNSChaosResolverTreatsUnaddressableTargetsAsNoPolicy(t *testing.T) {
	for _, tc := range []struct {
		err     error
		wantErr bool
	}{
		{err: state.ErrInvalidArgument},
		{err: errors.New("store unavailable"), wantErr: true},
	} {
		resolve := newServiceDNSChaosResolver(dnsChaosPolicyStoreFunc(func(context.Context, string, string, string) (state.ScenarioTestDNSChaosPolicy, error) {
			return state.ScenarioTestDNSChaosPolicy{}, tc.err
		}), "node-a")
		policy, err := resolve(context.Background(), "10.100.0.5:53", "x")
		if (err != nil) != tc.wantErr || policy.RunID != "" {
			t.Fatalf("store error %v: policy=%+v err=%v, want error=%v", tc.err, policy, err, tc.wantErr)
		}
	}
}
