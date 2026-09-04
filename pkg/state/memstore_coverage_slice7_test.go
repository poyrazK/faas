package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// This slice targets the pure-logic surfaces in pkg/state that carry no
// store state: key helpers, error types, JSON marshaling, scan-source
// ranking, and the heartbeat-gap classifier. No MemStore rows are needed.

func TestStateKeys(t *testing.T) {
	if got := SnapMemKey("dep-1"); got != "snap/dep-1/mem" {
		t.Fatalf("SnapMemKey = %q", got)
	}
	if got := SnapVMStateKey("dep-1"); got != "snap/dep-1/vmstate" {
		t.Fatalf("SnapVMStateKey = %q", got)
	}
}

func TestQuotaErrorMessages(t *testing.T) {
	// Default kind (apps).
	apps := &QuotaError{Limit: 3, Observed: 4}
	if got := apps.Error(); got != "state: deployed-app quota exceeded (limit=3, observed=4)" {
		t.Fatalf("apps error = %q", got)
	}
	// errors.Is matches ErrQuotaExceeded.
	if !errors.Is(apps, ErrQuotaExceeded) {
		t.Fatal("QuotaError should match ErrQuotaExceeded")
	}
	// Cron kind with NotAllowed.
	cronNotAllowed := &QuotaError{Kind: QuotaErrorKindCrons, NotAllowed: true}
	if got := cronNotAllowed.Error(); got != "state: crons not allowed on this plan" {
		t.Fatalf("cron not allowed = %q", got)
	}
	// Cron kind with limit/observed.
	cron := &QuotaError{Kind: QuotaErrorKindCrons, Limit: 5, Observed: 6}
	if got := cron.Error(); got != "state: cron quota exceeded (limit=5, observed=6)" {
		t.Fatalf("cron error = %q", got)
	}
}

func TestCronQuotaError(t *testing.T) {
	e := &CronQuotaError{Scope: CronQuotaScopeApp, Limit: 2, Observed: 2}
	if got := e.Error(); got != "state: cron quota exceeded (scope=app, limit=2, observed=2)" {
		t.Fatalf("cron quota error = %q", got)
	}
	if !errors.Is(e, ErrCronQuotaExceeded) {
		t.Fatal("CronQuotaError should match ErrCronQuotaExceeded")
	}
}

func TestAlertRuleQuotaError(t *testing.T) {
	e := &AlertRuleQuotaError{Scope: AlertRuleQuotaScopeAccount, Limit: 10, Observed: 11}
	if got := e.Error(); got != "state: alert rule quota exceeded (scope=account, limit=10, observed=11)" {
		t.Fatalf("alert quota error = %q", got)
	}
	if !errors.Is(e, ErrAlertRuleQuotaExceeded) {
		t.Fatal("AlertRuleQuotaError should match ErrAlertRuleQuotaExceeded")
	}
}

func TestClampLogLimit(t *testing.T) {
	if got := clampLogLimit(0); got != 50 {
		t.Fatalf("clamp 0 = %d", got)
	}
	if got := clampLogLimit(-5); got != 50 {
		t.Fatalf("clamp -5 = %d", got)
	}
	if got := clampLogLimit(MaxDeploymentLogPage + 100); got != MaxDeploymentLogPage {
		t.Fatalf("clamp over max = %d", got)
	}
	if got := clampLogLimit(10); got != 10 {
		t.Fatalf("clamp in-range = %d", got)
	}
}

func TestAppManifestMarshalJSON(t *testing.T) {
	// Zero value → {}.
	if b, err := (AppManifest{}).MarshalJSON(); err != nil || string(b) != "{}" {
		t.Fatalf("zero manifest = %s, %v", b, err)
	}
	// Populated → full JSON round-trip.
	m := AppManifest{Entrypoint: []string{"node", "server.js"}, Env: map[string]string{"PORT": "8080"}, Port: 8080, Healthz: "/healthz"}
	b, err := m.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded AppManifest
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Entrypoint) != 2 || decoded.Port != 8080 || decoded.Healthz != "/healthz" {
		t.Fatalf("round-trip manifest = %+v", decoded)
	}
	// Lifecycle-only manifests must not be collapsed to {}: app settings
	// are persisted before the first deployment creates an entrypoint.
	lifecycle := AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &ServiceReplicas{Min: 1, Max: 3, Desired: 2},
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
	p := ScalingPolicy{MinInstances: 1, MaxInstances: 4, Target: &ScalingTarget{Metric: "rps", Value: 10}, ScaleOutCooldownS: 60}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got ScalingPolicy
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.MinInstances != 1 || got.MaxInstances != 4 || got.Target == nil || got.Target.Metric != "rps" || got.ScaleOutCooldownS != 60 {
		t.Fatalf("round-trip scaling policy = %+v", got)
	}
	// UnmarshalJSON on malformed input → error.
	if err := json.Unmarshal([]byte(`not-json`), &got); err == nil {
		t.Fatal("malformed policy should error")
	}
	// ScalingPolicyOrDefault.
	if got := ScalingPolicyOrDefault(nil); got.MinInstances != 0 {
		t.Fatalf("nil policy default = %+v", got)
	}
	if got := ScalingPolicyOrDefault(&p); got.MaxInstances != 4 {
		t.Fatalf("non-nil policy = %+v", got)
	}
}

func TestAccountHelpers(t *testing.T) {
	if !(Account{Status: AccountActive}).Active() {
		t.Fatal("active account should be Active")
	}
	if !(Account{Status: AccountPastDue}).Active() {
		t.Fatal("past-due account should be Active")
	}
	if (Account{Status: AccountSuspended}).Active() {
		t.Fatal("suspended account should not be Active")
	}
	if (Account{}).MFAEnrolled() {
		t.Fatal("no enrollment should be false")
	}
	now := time.Now()
	if !(Account{MFAEnrolledAt: &now}).MFAEnrolled() {
		t.Fatal("enrolled account should be MFAEnrolled")
	}
}

func TestProjectIsZero(t *testing.T) {
	if !(Project{}).IsZero() {
		t.Fatal("zero project should be IsZero")
	}
	if (Project{ID: "x"}).IsZero() {
		t.Fatal("non-zero project should not be IsZero")
	}
}

func TestTierRank(t *testing.T) {
	cases := []struct {
		src  ProjectScanSource
		want scanSourceRank
	}{
		{ProjectScanSourceCompose, scanSourceRankCompose},
		{ProjectScanSourceK8s, scanSourceRankK8s},
		{ProjectScanSourceRender, scanSourceRankRender},
		{ProjectScanSourceFly, scanSourceRankFly},
		{ProjectScanSourceServerless, scanSourceRankServerless},
		{ProjectScanSourceProcfile, scanSourceRankProcfile},
		{ProjectScanSourceWorkspace, scanSourceRankWorkspace},
		{ProjectScanSourceConvention, scanSourceRankConvention},
		{ProjectScanSourceSingle, scanSourceRankSingle},
		{ProjectScanSourceUnknown, scanSourceRankUnknown},
		{"", scanSourceRankUnknown},
	}
	for _, c := range cases {
		if got := tierRank(c.src); got != c.want {
			t.Fatalf("tierRank(%q) = %d, want %d", c.src, got, c.want)
		}
	}
}

func TestClassifyHeartbeatGap(t *testing.T) {
	interval := DefaultHeartbeatInterval
	staleness := DefaultHeartbeatStaleness
	base := time.Now()
	cases := []struct {
		name string
		prev time.Time
		curr time.Time
		want HeartbeatGapSummary
	}{
		// Zero prev: the gap arithmetic overflows (curr.Sub(zero) is
		// huge), but the classifier's zero-prev guard means neither
		// Missed nor Stale is set. Assert the flags only.
		{"zero prev", time.Time{}, base, HeartbeatGapSummary{}},
		{"healthy tick", base, base.Add(interval / 2), HeartbeatGapSummary{Gap: interval / 2}},
		{"one missed tick", base, base.Add(interval), HeartbeatGapSummary{Gap: interval, Missed: true}},
		{"stale", base, base.Add(2 * staleness), HeartbeatGapSummary{Gap: 2 * staleness, Missed: true, Stale: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyHeartbeatGap(c.prev, c.curr, interval, staleness)
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
