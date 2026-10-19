package conformance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/state"
)

// Namespace cleanup cannot reopen network or webhook delivery for an enrolled
// app, or apply its retained policy to an ordinary app.
func testScenarioActionPolicyReaders(t *testing.T, fx *Fixture) {
	s, ctx := fx.Store, fx.Ctx
	expiry := time.Now().UTC().Add(time.Hour)
	app, err := s.CreateApp(ctx, state.App{
		AccountID: fx.Account.ID, Slug: "policy-" + uuid.NewString(),
		Type: state.AppTypeApp, Runtime: "node22", RAMMB: fx.App.RAMMB,
		PreviewOfSlug: fx.App.Slug, PreviewPrState: state.PreviewPrStateOpen,
		PreviewExpiresAt: &expiry,
	})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := s.CreateAppWebhook(ctx, state.AppWebhook{
		AppID: app.ID, AccountID: fx.Account.ID, TargetURL: "https://example.test/policy",
		SecretSealed: []byte("sealed"), Enabled: true,
		EventFilter: []string{string(state.AppWebhookEventAppDeployed)},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertPolicy := func(enrolled bool) {
		t.Helper()
		want := ""
		if enrolled {
			want = strings.Repeat("e", 32)
		}
		if run, err := s.ScenarioRehearsalNetworkRun(ctx, app.ID); err != nil || run != want {
			t.Fatalf("contained app run = %q, %v; want %q", run, err, want)
		}
		if run, err := s.ScenarioRehearsalNetworkRun(ctx, fx.App.ID); err != nil || run != "" {
			t.Fatalf("ordinary app inherited containment: %q, %v", run, err)
		}
		for _, pair := range [][2]string{{app.ID, uuid.NewString()}, {fx.App.ID, hook.ID}} {
			if blocked, err := s.ScenarioWebhookDeliveryBlocked(ctx, pair[0], pair[1]); err != nil || blocked != enrolled {
				t.Fatalf("app/receiver policy %v = %v, %v; want %v", pair, blocked, err, enrolled)
			}
		}
		if blocked, err := s.ScenarioWebhookDeliveryBlocked(ctx, fx.App.ID, uuid.NewString()); err != nil || blocked {
			t.Fatalf("ordinary delivery inherited capture: %v, %v", blocked, err)
		}
	}
	assertPolicy(false)
	runID := strings.Repeat("e", 32)
	if err := s.RegisterScenarioTestMembers(ctx, fx.Account.ID, runID, []state.ScenarioTestMember{{
		AppID: app.ID, Workload: "primary", WebhookMode: state.ScenarioWebhookCaptureMode,
		NetworkMode: state.ScenarioNetworkContained,
	}}); err != nil {
		t.Fatal(err)
	}
	assertPolicy(true)
	if _, err := s.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteScenarioTestMembers(ctx, fx.Account.ID, runID); err != nil {
		t.Fatal(err)
	}
	assertPolicy(true)
}

// A test service name must never resolve outside its run, and a failed
// teardown must keep the namespace fenced until the apps have been deleted.
func testScenarioTestNamespace(t *testing.T, fx *Fixture) {
	s, ctx, accountID := fx.Store, fx.Ctx, fx.Account.ID
	limits := api.MustLimitsFor(api.PlanPro)
	expiry := time.Now().UTC().Add(time.Hour)
	newPreview := func() state.App {
		t.Helper()
		app, err := s.CreateAppIfUnderQuota(ctx, state.App{
			AccountID: accountID, Slug: "scenario-" + uuid.NewString()[:8],
			Type: state.AppTypeApp, Runtime: "node22", RAMMB: limits.RAMMB,
			MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
			PreviewOfSlug: fx.App.Slug, PreviewPrState: state.PreviewPrStateOpen,
			PreviewExpiresAt: &expiry,
		}, limits)
		if err != nil {
			t.Fatalf("CreateAppIfUnderQuota(preview): %v", err)
		}
		return app
	}
	app := newPreview()
	callerApp := newPreview()
	runID := strings.Repeat("a", 32)
	member := state.ScenarioTestMember{Workload: "worker", AppID: app.ID}
	if err := s.RegisterScenarioTestMembers(ctx, accountID, runID, []state.ScenarioTestMember{{Workload: "primary", AppID: fx.App.ID}}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("register production app = %v, want ErrNotFound", err)
	}
	if err := s.RegisterScenarioTestMembers(ctx, accountID, runID, []state.ScenarioTestMember{
		member, {Workload: "primary", AppID: callerApp.ID},
	}); err != nil {
		t.Fatalf("RegisterScenarioTestMembers: %v", err)
	}
	if got, err := s.ScenarioTestMemberByApp(ctx, app.ID); err != nil || got.AccountID != accountID || got.RunID != runID || got.Workload != "worker" {
		t.Fatalf("ScenarioTestMemberByApp = (%+v, %v)", got, err)
	}
	if got, err := s.ScenarioTestAppByWorkload(ctx, accountID, runID, "worker"); err != nil || got.ID != app.ID {
		t.Fatalf("ScenarioTestAppByWorkload = (%+v, %v)", got, err)
	}
	plan := chaos.Plan{DurationMS: 10_000, Rules: []chaos.Rule{{
		To: "worker", Kind: chaos.KindHTTPStatus, Percent: 100, StatusCode: 503, Seed: 9,
	}}}
	if _, err := s.SetScenarioTestChaosPlan(ctx, accountID, runID, plan); err != nil {
		t.Fatalf("SetScenarioTestChaosPlan: %v", err)
	}
	if _, err := s.SetScenarioTestChaosPlan(ctx, accountID, runID, chaos.Plan{DurationMS: 10_000, Rules: []chaos.Rule{{
		To: "production", Kind: chaos.KindHTTPStatus, Percent: 100, StatusCode: 503,
	}}}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("install plan for unregistered workload = %v, want ErrConflict", err)
	}
	lease, err := s.ScenarioTestChaosForCall(ctx, runID, app.ID, "worker")
	if err != nil || lease.CallerWorkload != "worker" || len(lease.Rules) != 1 || lease.Rules[0].StatusCode != 503 {
		t.Fatalf("ScenarioTestChaosForCall = (%+v, %v)", lease, err)
	}
	strictRule := chaos.Rule{From: "primary", To: "worker", Kind: chaos.KindHTTPStatus, StatusCode: 503, Percent: 100, CorrelationScope: "0123456789abcdef"}
	strictPlan := chaos.Plan{DurationMS: 10_000, Rules: []chaos.Rule{strictRule}}
	strictLease, err := s.SetScenarioTestChaosPlan(ctx, accountID, runID, strictPlan)
	if err != nil {
		t.Fatal(err)
	}
	injections := []chaos.InjectionBatch{}
	for _, id := range []string{chaos.RuleID(strictRule), chaos.CorrelationSupportID(strictRule), chaos.CorrelationCounterID(strictRule, 1)} {
		injections = append(injections, chaos.InjectionBatch{RunID: runID, CallerApp: callerApp.ID, Generation: strictLease.Generation, RuleID: id, Count: 2})
	}
	if err := s.RecordScenarioTestChaosInjections(ctx, injections); err != nil {
		t.Fatal(err)
	}
	evidence, err := s.ScenarioTestChaosMatchEvidence(ctx, accountID, runID)
	if err != nil || evidence.Generation != strictLease.Generation || len(evidence.Matches) != 3 {
		t.Fatalf("strict persisted counters: %+v %v", evidence, err)
	}
	for _, match := range evidence.Matches {
		if match.Count != 2 {
			t.Fatalf("strict counter lost: %+v", match)
		}
	}
	strictRule.CorrelationScope = "fedcba9876543210"
	strictPlan.Rules = []chaos.Rule{strictRule}
	replacement, err := s.SetScenarioTestChaosPlan(ctx, accountID, runID, strictPlan)
	if err != nil || replacement.Generation == strictLease.Generation {
		t.Fatalf("strict replacement: %+v %v", replacement, err)
	}
	if err := s.RecordScenarioTestChaosInjections(ctx, injections); err != nil {
		t.Fatal(err)
	}
	evidence, err = s.ScenarioTestChaosMatchEvidence(ctx, accountID, runID)
	if err != nil || evidence.Generation != replacement.Generation || len(evidence.Matches) != 0 {
		t.Fatalf("stale strict generation escaped: %+v %v", evidence, err)
	}
	if err := s.ClearScenarioTestChaosPlan(ctx, accountID, runID); err != nil {
		t.Fatal(err)
	}
	evidence, err = s.ScenarioTestChaosMatchEvidence(ctx, accountID, runID)
	if err != nil || len(evidence.Matches) != 0 {
		t.Fatalf("strict clear retained counters: %+v %v", evidence, err)
	}
	for _, rule := range []chaos.Rule{
		{Kind: chaos.KindTCPLatency, LatencyMS: 100},
		{Kind: chaos.KindTCPBandwidth, RateKiBPerSecond: 32, Direction: chaos.DirectionDownstream},
		{Kind: chaos.KindTCPTimeout},
		{Kind: chaos.KindTCPReset, ResetAfterMS: 25},
	} {
		rule.To, rule.Port, rule.Percent = "worker", 6379, 100
		if _, err := s.SetScenarioTestChaosPlan(ctx, accountID, runID, chaos.Plan{DurationMS: 1000, Rules: []chaos.Rule{rule}}); err != nil {
			t.Fatalf("TCP plan store: %v", err)
		}
		got, err := s.ScenarioTestChaosForCall(ctx, runID, app.ID, "worker")
		if err != nil || len(got.Rules) != 1 || got.Rules[0] != rule {
			t.Fatalf("TCP rule roundtrip: %+v %v", got, err)
		}
	}
	dnsPlan := chaos.Plan{DurationMS: 10_000, Rules: []chaos.Rule{{
		From: "primary", To: "worker", Kind: chaos.KindDNSNXDomain, Percent: 100,
	}}}
	if _, err := s.SetScenarioTestChaosPlan(ctx, accountID, runID, dnsPlan); err != nil {
		t.Fatalf("SetScenarioTestChaosPlan(DNS): %v", err)
	}
	callerInstance, err := s.CreateInstance(ctx, callerApp.ID, "", string(state.StateColdBooting), 128, fx.Node.ID, "")
	if err != nil {
		t.Fatalf("CreateInstance(DNS caller): %v", err)
	}
	if _, err := s.PublishInstanceRuntime(ctx, callerInstance.ID, string(state.StateColdBooting), "scenario-dns-caller", "10.99.0.77", 20077); err != nil {
		t.Fatalf("PublishInstanceRuntime(DNS caller): %v", err)
	}
	dnsPolicy, err := s.ScenarioTestDNSChaosByHostIP(ctx, fx.Node.Name, "10.99.0.77", "worker")
	if err != nil || dnsPolicy.RunID != runID || dnsPolicy.CallerAppID != callerApp.ID ||
		len(dnsPolicy.Lease.Rules) != 1 || dnsPolicy.Lease.Rules[0].Kind != chaos.KindDNSNXDomain {
		t.Fatalf("ScenarioTestDNSChaosByHostIP = (%+v, %v)", dnsPolicy, err)
	}
	if policy, err := s.ScenarioTestDNSChaosByHostIP(ctx, fx.Node.Name, "10.99.0.77", "production"); err != nil || len(policy.Lease.Rules) != 0 {
		t.Fatalf("DNS lookup escaped the run namespace = (%+v, %v)", policy, err)
	}
	productionInstance, err := s.CreateInstance(ctx, fx.App.ID, fx.Deployment.ID, string(state.StateColdBooting), 128, fx.Node.ID, "")
	if err != nil {
		t.Fatalf("CreateInstance(production caller): %v", err)
	}
	if _, err := s.PublishInstanceRuntime(ctx, productionInstance.ID, string(state.StateColdBooting), "scenario-dns-production", "10.99.0.78", 20078); err != nil {
		t.Fatalf("PublishInstanceRuntime(production caller): %v", err)
	}
	if policy, err := s.ScenarioTestDNSChaosByHostIP(ctx, fx.Node.Name, "10.99.0.78", "worker"); err != nil || len(policy.Lease.Rules) != 0 {
		t.Fatalf("production caller received DNS chaos = (%+v, %v)", policy, err)
	}
	if err := s.ClearScenarioTestChaosPlan(ctx, accountID, runID); err != nil {
		t.Fatalf("ClearScenarioTestChaosPlan: %v", err)
	}
	if got, err := s.ScenarioTestChaosForCall(ctx, runID, app.ID, "worker"); err != nil || len(got.Rules) != 0 {
		t.Fatalf("ScenarioTestChaosForCall after clear = (%+v, %v), want no rules", got, err)
	}
	if err := s.ClearScenarioTestChaosPlan(ctx, accountID, "invalid"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("clear with invalid run ID = %v, want ErrConflict", err)
	}
	if err := s.ClearScenarioTestChaosPlan(ctx, accountID, strings.Repeat("d", 32)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("clear missing namespace = %v, want ErrNotFound", err)
	}
	if lease, err := s.ScenarioTestChaosForCall(ctx, strings.Repeat("b", 32), app.ID, "worker"); err != nil || len(lease.Rules) != 0 {
		t.Fatalf("cross-run chaos lookup = (%+v, %v), want empty", lease, err)
	}
	for _, scope := range [][2]string{{uuid.NewString(), runID}, {accountID, strings.Repeat("b", 32)}} {
		if _, err := s.ScenarioTestAppByWorkload(ctx, scope[0], scope[1], "worker"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cross-scope lookup %v = %v, want ErrNotFound", scope, err)
		}
	}
	if err := s.RegisterScenarioTestMembers(ctx, accountID, runID, []state.ScenarioTestMember{member}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate registration = %v, want ErrConflict", err)
	}
	if err := s.DeleteScenarioTestMembers(ctx, accountID, runID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("delete active namespace = %v, want ErrConflict", err)
	}
	if n, err := s.PruneScenarioTestMembers(ctx, 1); err != nil || n != 0 {
		t.Fatalf("prune active namespace = (%d, %v), want (0, nil)", n, err)
	}
	if err := s.DeleteApp(ctx, app.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if err := s.DeleteApp(ctx, callerApp.ID); err != nil {
		t.Fatalf("DeleteApp(DNS caller): %v", err)
	}
	if _, err := s.ScenarioTestAppByWorkload(ctx, accountID, runID, "worker"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("lookup deleted preview = %v, want ErrNotFound", err)
	}
	if n, err := s.PruneScenarioTestMembers(ctx, 2); err != nil || n != 2 {
		t.Fatalf("prune deleted namespace = (%d, %v), want (2, nil)", n, err)
	}
	if _, err := s.ScenarioTestMemberByApp(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("lookup pruned member = %v, want ErrNotFound", err)
	}
	if lease, err := s.ScenarioTestChaosForCall(ctx, runID, app.ID, "worker"); err != nil || len(lease.Rules) != 0 {
		t.Fatalf("lookup pruned chaos plan = (%+v, %v), want empty", lease, err)
	}

	second := newPreview()
	secondRun := strings.Repeat("c", 32)
	if err := s.RegisterScenarioTestMembers(ctx, accountID, secondRun, []state.ScenarioTestMember{{Workload: "worker", AppID: second.ID}}); err != nil {
		t.Fatalf("register second namespace: %v", err)
	}
	if err := s.DeleteApp(ctx, second.ID); err != nil {
		t.Fatalf("DeleteApp(second): %v", err)
	}
	if err := s.DeleteScenarioTestMembers(ctx, accountID, secondRun); err != nil {
		t.Fatalf("delete drained namespace: %v", err)
	}
	if _, err := s.ScenarioTestMemberByApp(ctx, second.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("lookup deleted member = %v, want ErrNotFound", err)
	}
}
