package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This slice targets the pure-logic surfaces in pkg/state that carry no
// store state: key helpers, error types, JSON marshaling, scan-source
// ranking, and the heartbeat-gap classifier. No MemStore rows are needed.

func TestStateKeys(t *testing.T) {
	if got := state.SnapMemKey("dep-1"); got != "snap/dep-1/mem" {
		t.Fatalf("SnapMemKey = %q", got)
	}
	if got := state.SnapVMStateKey("dep-1"); got != "snap/dep-1/vmstate" {
		t.Fatalf("SnapVMStateKey = %q", got)
	}
}

func TestQuotaErrorMessages(t *testing.T) {
	// Default kind (apps).
	apps := &state.QuotaError{Limit: 3, Observed: 4}
	if got := apps.Error(); got != "state: deployed-app quota exceeded (limit=3, observed=4)" {
		t.Fatalf("apps error = %q", got)
	}
	// errors.Is matches ErrQuotaExceeded.
	if !errors.Is(apps, state.ErrQuotaExceeded) {
		t.Fatal("QuotaError should match ErrQuotaExceeded")
	}
	// Cron kind with NotAllowed.
	cronNotAllowed := &state.QuotaError{Kind: state.QuotaErrorKindCrons, NotAllowed: true}
	if got := cronNotAllowed.Error(); got != "state: crons not allowed on this plan" {
		t.Fatalf("cron not allowed = %q", got)
	}
	// Cron kind with limit/observed.
	cron := &state.QuotaError{Kind: state.QuotaErrorKindCrons, Limit: 5, Observed: 6}
	if got := cron.Error(); got != "state: cron quota exceeded (limit=5, observed=6)" {
		t.Fatalf("cron error = %q", got)
	}
}

func TestCronQuotaError(t *testing.T) {
	e := &state.CronQuotaError{Scope: state.CronQuotaScopeApp, Limit: 2, Observed: 2}
	if got := e.Error(); got != "state: cron quota exceeded (scope=app, limit=2, observed=2)" {
		t.Fatalf("cron quota error = %q", got)
	}
	if !errors.Is(e, state.ErrCronQuotaExceeded) {
		t.Fatal("CronQuotaError should match ErrCronQuotaExceeded")
	}
}

func TestAlertRuleQuotaError(t *testing.T) {
	e := &state.AlertRuleQuotaError{Scope: state.AlertRuleQuotaScopeAccount, Limit: 10, Observed: 11}
	if got := e.Error(); got != "state: alert rule quota exceeded (scope=account, limit=10, observed=11)" {
		t.Fatalf("alert quota error = %q", got)
	}
	if !errors.Is(e, state.ErrAlertRuleQuotaExceeded) {
		t.Fatal("AlertRuleQuotaError should match ErrAlertRuleQuotaExceeded")
	}
}

func TestClampLogLimit(t *testing.T) {
	if got := state.ClampLogLimit(0); got != 50 {
		t.Fatalf("clamp 0 = %d", got)
	}
	if got := state.ClampLogLimit(-5); got != 50 {
		t.Fatalf("clamp -5 = %d", got)
	}
	if got := state.ClampLogLimit(state.MaxDeploymentLogPage + 100); got != state.MaxDeploymentLogPage {
		t.Fatalf("clamp over max = %d", got)
	}
	if got := state.ClampLogLimit(10); got != 10 {
		t.Fatalf("clamp in-range = %d", got)
	}
}

func TestAppManifestMarshalJSON(t *testing.T) {
	// Zero value → {}.
	if b, err := (state.AppManifest{}).MarshalJSON(); err != nil || string(b) != "{}" {
		t.Fatalf("zero manifest = %s, %v", b, err)
	}
	// Populated → full JSON round-trip.
	m := state.AppManifest{Entrypoint: []string{"node", "server.js"}, Env: map[string]string{"PORT": "8080"}, Port: 8080, Healthz: "/healthz"}
	b, err := m.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded state.AppManifest
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Entrypoint) != 2 || decoded.Port != 8080 || decoded.Healthz != "/healthz" {
		t.Fatalf("round-trip manifest = %+v", decoded)
	}
	// Lifecycle-only manifests must not be collapsed to {}: app settings
	// are persisted before the first deployment creates an entrypoint.
	lifecycle := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 2},
	}
	b, err = lifecycle.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "{}" {
		t.Fatal("lifecycle-only manifest was collapsed to {}")
	}
}

func TestScalingPolicyJSON(t *testing.T) {
	// MarshalJSON with a populated target.
	p := state.ScalingPolicy{MinInstances: 1, MaxInstances: 4, Target: &state.ScalingTarget{Metric: "rps", Value: 10}, ScaleOutCooldownS: 60, ConcurrencyOverflow: "drop", MaxQueueWaitMS: 1250, MaxQueueDepth: 17, WakeMaxQueueDepth: 32, WakeMaxQueueWaitSeconds: 20}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got state.ScalingPolicy
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.MinInstances != 1 || got.MaxInstances != 4 || got.Target == nil || got.Target.Metric != "rps" || got.ScaleOutCooldownS != 60 || got.ConcurrencyOverflow != "drop" || got.MaxQueueWaitMS != 1250 || got.MaxQueueDepth != 17 || got.WakeMaxQueueDepth != 32 || got.WakeMaxQueueWaitSeconds != 20 {
		t.Fatalf("round-trip scaling policy = %+v", got)
	}
	// UnmarshalJSON on malformed input → error.
	if err := json.Unmarshal([]byte(`not-json`), &got); err == nil {
		t.Fatal("malformed policy should error")
	}
	// ScalingPolicyOrDefault.
	if got := state.ScalingPolicyOrDefault(nil); got.MinInstances != 0 {
		t.Fatalf("nil policy default = %+v", got)
	}
	if got := state.ScalingPolicyOrDefault(&p); got.MaxInstances != 4 {
		t.Fatalf("non-nil policy = %+v", got)
	}
}

func TestAccountHelpers(t *testing.T) {
	if !(state.Account{Status: state.AccountActive}).Active() {
		t.Fatal("active account should be Active")
	}
	if !(state.Account{Status: state.AccountPastDue}).Active() {
		t.Fatal("past-due account should be Active")
	}
	if (state.Account{Status: state.AccountSuspended}).Active() {
		t.Fatal("suspended account should not be Active")
	}
	if (state.Account{}).MFAEnrolled() {
		t.Fatal("no enrollment should be false")
	}
	now := time.Now()
	if !(state.Account{MFAEnrolledAt: &now}).MFAEnrolled() {
		t.Fatal("enrolled account should be MFAEnrolled")
	}
}

func TestProjectIsZero(t *testing.T) {
	if !(state.Project{}).IsZero() {
		t.Fatal("zero project should be IsZero")
	}
	if (state.Project{ID: "x"}).IsZero() {
		t.Fatal("non-zero project should not be IsZero")
	}
}

func TestTierRank(t *testing.T) {
	cases := []struct {
		src  state.ProjectScanSource
		want state.ScanSourceRank
	}{
		{state.ProjectScanSourceCompose, state.ScanSourceRankCompose},
		{state.ProjectScanSourceK8s, state.ScanSourceRankK8s},
		{state.ProjectScanSourceRender, state.ScanSourceRankRender},
		{state.ProjectScanSourceFly, state.ScanSourceRankFly},
		{state.ProjectScanSourceServerless, state.ScanSourceRankServerless},
		{state.ProjectScanSourceProcfile, state.ScanSourceRankProcfile},
		{state.ProjectScanSourceWorkspace, state.ScanSourceRankWorkspace},
		{state.ProjectScanSourceConvention, state.ScanSourceRankConvention},
		{state.ProjectScanSourceSingle, state.ScanSourceRankSingle},
		{state.ProjectScanSourceUnknown, state.ScanSourceRankUnknown},
		{"", state.ScanSourceRankUnknown},
	}
	for _, c := range cases {
		if got := state.TierRank(c.src); got != c.want {
			t.Fatalf("tierRank(%q) = %d, want %d", c.src, got, c.want)
		}
	}
}

func TestClassifyHeartbeatGap(t *testing.T) {
	interval := state.DefaultHeartbeatInterval
	staleness := state.DefaultHeartbeatStaleness
	base := time.Now()
	cases := []struct {
		name string
		prev time.Time
		curr time.Time
		want state.HeartbeatGapSummary
	}{
		// Zero prev: the gap arithmetic overflows (curr.Sub(zero) is
		// huge), but the classifier's zero-prev guard means neither
		// Missed nor Stale is set. Assert the flags only.
		{"zero prev", time.Time{}, base, state.HeartbeatGapSummary{}},
		{"healthy tick", base, base.Add(interval / 2), state.HeartbeatGapSummary{Gap: interval / 2}},
		{"one missed tick", base, base.Add(interval), state.HeartbeatGapSummary{Gap: interval, Missed: true}},
		{"stale", base, base.Add(2 * staleness), state.HeartbeatGapSummary{Gap: 2 * staleness, Missed: true, Stale: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := state.ClassifyHeartbeatGap(c.prev, c.curr, interval, staleness)
			if got.Missed != c.want.Missed || got.Stale != c.want.Stale {
				t.Fatalf("ClassifyHeartbeatGap = %+v, want %+v", got, c.want)
			}
			if c.prev.IsZero() {
				return
			}
			if got.Gap != c.want.Gap {
				t.Fatalf("ClassifyHeartbeatGap gap = %v, want %v", got.Gap, c.want.Gap)
			}
		})
	}
}
