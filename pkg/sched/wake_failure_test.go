// Tests for the schedd-side wake-failure metric emitters (issue
// #1059 / ADR-127 §3.6 — cluster A commit 3 of the
// platform-observability mega-PR). The schedd emits
// schedd_wake_failure_total{box, app, reason} alongside the
// existing events.BootFailed audit-reason emits at
// pkg/sched/engine.go:2123 (vmm_boot_failed) and :2194
// (record_runtime_failed).
//
// This file exercises the metric surface only — the
// Engine.Wake harness integration is covered by the
// `make test-metal` suite (pkg/sched/engine_test.go::TestWake_*)
// where a fakeVMM is wired in. The metric-only pin here is the
// regression guard for "did the operator-facing closed vocab
// union (8 vmmd-side + 2 schedd-side reasons) actually land on
// the schedd-side OpsMetrics registry".

package sched_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestSchedd_WakeFailure_EmittedOnBootFail exercises the metric
// surface for the schedd-side `vmm_boot_failed` reason (issue
// #1059 / ADR-127 §3.6). The Engine's boot-failure branch at
// pkg/sched/engine.go:2127 calls e.ops.WakeFailure("local",
// bootInput.appID, "vmm_boot_failed").Inc() — this test pins
// the metric surface itself: a counter increment on the closed
// "vmm_boot_failed" reason must show as 1 in the registry, the
// closed "record_runtime_failed" sibling must remain at 0
// (separate counter), and the pre-instantiated closed vocab
// surface (labelLocal × labelAppUnknown × 10 reasons) must
// round-trip through the scrape body.
func TestSchedd_WakeFailure_EmittedOnBootFail(t *testing.T) {
	ops := wire.NewOpsMetrics("schedd")
	// Fire the metric the way Engine does at engine.go:2127.
	c := ops.WakeFailure("local", "my-app", "vmm_boot_failed")
	if c == nil {
		t.Fatal("WakeFailure returned nil counter")
	}
	c.Inc()
	if got := testutil.ToFloat64(c); got != 1 {
		t.Errorf("vmm_boot_failed counter = %v, want 1", got)
	}

	// The sibling reason (record_runtime_failed, emitted at
	// engine.go:2199) must remain at 0 — distinct counter, not
	// shared.
	sibling := ops.WakeFailure("local", "my-app", "record_runtime_failed")
	if got := testutil.ToFloat64(sibling); got != 0 {
		t.Errorf("record_runtime_failed counter = %v, want 0 (distinct from vmm_boot_failed)", got)
	}

	// A second increment on the boot-fail counter must surface
	// as 2 — the Prometheus counter is monotonic, not a boolean.
	c.Inc()
	if got := testutil.ToFloat64(c); got != 2 {
		t.Errorf("vmm_boot_failed counter after second Inc = %v, want 2", got)
	}

	// Scrape the registry — the closed (box, app, reason) cartesian
	// is pre-instantiated for {labelLocal, otherBoxLabel} ×
	// {labelAppUnknown, otherAppLabel} × 10 reasons = 40 series on
	// idle fleet, plus the one we just bumped to 2.
	body := sched.GetMetricsBody(t, ops)
	for _, want := range []string{
		// Counter incremented to 2 lands in the scrape body.
		`schedd_wake_failure_total{app="my-app",box="local",reason="vmm_boot_failed"} 2`,
		// Sibling reason pre-instantiated at 0.
		`schedd_wake_failure_total{app="my-app",box="local",reason="record_runtime_failed"} 0`,
		// Reserved labels never count against the cap
		// (maxBoxLabelValues=64, maxAppLabelValues=256) — the
		// __other__ bucket stays at 0 because no real box / app
		// has overflowed yet.
		`schedd_wake_failure_total{app="",box="local",reason="vmm_boot_failed"} 0`,
		`schedd_wake_failure_total{app="__other__",box="__other__",reason="vmm_boot_failed"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

// TestSchedd_WakeFailure_EmittedOnRuntimeFail mirrors the
// boot-fail test for the post-boot SetInstanceRuntime-failure
// branch at pkg/sched/engine.go:2199. Same contract — a counter
// increment on the closed "record_runtime_failed" reason must
// show as 1, the boot-fail sibling must stay at 0.
func TestSchedd_WakeFailure_EmittedOnRuntimeFail(t *testing.T) {
	ops := wire.NewOpsMetrics("schedd")
	c := ops.WakeFailure("local", "my-app", "record_runtime_failed")
	if c == nil {
		t.Fatal("WakeFailure returned nil counter")
	}
	c.Inc()
	if got := testutil.ToFloat64(c); got != 1 {
		t.Errorf("record_runtime_failed counter = %v, want 1", got)
	}

	// Sibling remains at 0.
	sibling := ops.WakeFailure("local", "my-app", "vmm_boot_failed")
	if got := testutil.ToFloat64(sibling); got != 0 {
		t.Errorf("vmm_boot_failed counter = %v, want 0 (distinct from record_runtime_failed)", got)
	}

	// Bumping a DIFFERENT app's runtime-fail counter must NOT
	// bump my-app's counter — the (box, app, reason) tuple is
	// the dedup key.
	other := ops.WakeFailure("local", "other-app", "record_runtime_failed")
	other.Inc()
	if got := testutil.ToFloat64(other); got != 1 {
		t.Errorf("other-app record_runtime_failed counter = %v, want 1 (distinct app dimension)", got)
	}
	if got := testutil.ToFloat64(c); got != 1 {
		t.Errorf("my-app counter after other-app Inc = %v, want 1 (the (box, app, reason) tuple is the dedup key)", got)
	}
}

// TestSchedd_WakeFailure_ClosedVocabUnion pins that the schedd
// registry pre-instantiates BOTH the vmmd-side closed vocab
// (8 reasons) and the schedd-side audit-reason strings (2
// reasons) at boot. The reason union lives at
// pkg/wire/metrics.go::wakeFailureReasons. A regression that
// trimmed the slice to the vmmd-side only would leave the
// schedd's counters for "vmm_boot_failed" and
// "record_runtime_failed" without pre-instantiated series at
// boot, and the §12 "Wake failures by reason (24h)" dashboard
// panel would render "no data" gaps on an idle schedd.
func TestSchedd_WakeFailure_ClosedVocabUnion(t *testing.T) {
	ops := wire.NewOpsMetrics("schedd")
	body := sched.GetMetricsBody(t, ops)
	for _, reason := range []string{
		// vmmd-side closed vocab — schedd never emits these,
		// but the metric pre-instantiation is symmetric so a
		// cross-daemon dashboard legend works.
		"snapshot_stale",
		"disk_full",
		"jailer_fail",
		"netns_fail",
		"cgroup_fail",
		"vsock_fail",
		"snapshot_restore_err",
		"mem_backend_err",
		// schedd-side audit-reason strings (cluster A commit 3).
		"vmm_boot_failed",
		"record_runtime_failed",
	} {
		want := `schedd_wake_failure_total{app="",box="local",reason="` + reason + `"} 0`
		if !strings.Contains(body, want) {
			t.Errorf("missing pre-instantiated closed-vocab series for reason=%q in:\n%s", reason, body)
		}
	}
}

// TestSchedd_WakeFailure_NilSafe pins the nil-safe receiver
// contract — schedd unit tests that don't wire an OpsMetrics
// must keep building. The convention is "nil receiver returns
// nil counter, no panic".
func TestSchedd_WakeFailure_NilSafe(t *testing.T) {
	var ops *wire.OpsMetrics
	if got := ops.WakeFailure("local", "my-app", "vmm_boot_failed"); got != nil {
		t.Errorf("nil.OpsMetrics.WakeFailure = %v, want nil", got)
	}
}

// scrapeOpsMetrics was the original helper name; this file now
// uses pkg/sched/engine_test.go::getMetricsBody (internal helper,
// `package sched`). The function is the same shape —
// `httptest.NewServer(ops.Handler()) + http.Get + io.ReadAll`.
// The import of wire is still needed for the metric calls above.
