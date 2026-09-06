package events

import (
	"testing"
	"time"
)

// TestWakeEvent_AllKindsImplementInterface — compile-time check
// that every payload struct in wake.go implements the WakeEvent
// interface. A new struct that forgets a method fails the
// compilation, which is the cheapest schema validator the package
// can ship.
func TestWakeEvent_AllKindsImplementInterface(t *testing.T) {
	now := time.Now()
	acct := "acct-1"
	_ = acct
	// Touch each struct so the linter + compiler can't drop the
	// interface check; the bind is through the WakeEvent type
	// assertion. If any payload struct drifts from the interface
	// (a missing method, a Kind() string drift), this fails to
	// compile.
	var _ WakeEvent = QueueAccepted{EmitAt: now, WakeID: "w", AppID: "a", RequestID: "r"}
	var _ WakeEvent = Admitted{EmitAt: now, WakeID: "w", AppID: "a", RequestID: "r", AccountID: "acct-1", Plan: "hobby"}
	var _ WakeEvent = BootStarted{EmitAt: now, WakeID: "w", AppID: "a", InstanceID: "i", NodeID: "n", Method: "cold_boot", Trigger: "gateway", QueuedCount: 2, ConcurrencyAtAdmit: 3, AtCapacity: true}
	var _ WakeEvent = RestoreBreakdown{EmitAt: now, WakeID: "w", AppID: "a", InstanceID: "i", TotalMs: 596}
	var _ WakeEvent = BootCompleted{EmitAt: now, WakeID: "w", AppID: "a", InstanceID: "i", NodeID: "n", Method: "cold_boot", Trigger: "gateway", QueuedCount: 2, ConcurrencyAtAdmit: 3}
	var _ WakeEvent = BootFailed{EmitAt: now, WakeID: "w", AppID: "a", InstanceID: "i", NodeID: "n", Method: "cold_boot", Reason: "stub"}
	var _ WakeEvent = Readiness200{EmitAt: now, WakeID: "w", AppID: "a", InstanceID: "i", NodeID: "n", HealthcheckPath: "/healthz", ProbeCount: 1, ElapsedMs: 50}
	var _ WakeEvent = ProxyFirstByte{EmitAt: now, WakeID: "w", AppID: "a", RequestID: "r", InstanceID: "i", NodeID: "n", LatencyMs: 12}
	var _ WakeEvent = PageServed{EmitAt: now, WakeID: "w", AppID: "a", RequestID: "r", ServedAt: now, AccountID: "acct-1"}
	var _ WakeEvent = ParkStarted{EmitAt: now, WakeID: "w", AppID: "a", InstanceID: "i", NodeID: "n"}
	var _ WakeEvent = ParkCompleted{EmitAt: now, WakeID: "w", AppID: "a", InstanceID: "i", NodeID: "n", SnapshotID: "s-1"}
	var _ WakeEvent = Stalled{EmitAt: now, WakeID: "w", AppID: "a", InstanceID: "i", NodeID: "n", Reason: "watchdog"}
	var _ WakeEvent = BuildSucceeded{EmitAt: now, AppID: "a", DeploymentID: "d", ImageDigest: "sha256:abc", DurationMs: 12000}
	var _ WakeEvent = BuildFailed{EmitAt: now, AppID: "a", DeploymentID: "d", ImageDigest: "sha256:abc", Reason: "compile"}
	var _ WakeEvent = DeployFailed{EmitAt: now, AppID: "a", DeploymentID: "d", Reason: "scan"}
	var _ WakeEvent = SidecarInitExit{EmitAt: now, WakeID: "w", AppID: "a", InstanceID: "i", SidecarName: "metrics", Status: "init_ok", ExitCode: 0, DurationMs: 42}
	var _ WakeEvent = SidecarRestart{EmitAt: now, WakeID: "w", AppID: "a", InstanceID: "i", SidecarName: "metrics", Attempt: 1, PreviousExitCode: 137}
}

// TestQueueAccepted_Shape — the payload keys are the wire
// contract; the customer-facing JSON surfaces {"wake_id": ...}.
// Catch a typo'd key here before it ships.
func TestQueueAccepted_Shape(t *testing.T) {
	ev := QueueAccepted{EmitAt: time.Unix(0, 0).UTC(), WakeID: "w-1", AppID: "a-1", RequestID: "r-1"}
	if got := ev.Kind(); got != WakeQueueAccepted {
		t.Errorf("Kind = %q, want %q", got, WakeQueueAccepted)
	}
	if got := ev.Payload()["wake_id"]; got != "w-1" {
		t.Errorf("payload.wake_id = %v, want w-1", got)
	}
	if got := ev.Payload()["request_id"]; got != "r-1" {
		t.Errorf("payload.request_id = %v, want r-1", got)
	}
	if _, present := ev.Payload()["queue_wait_ms"]; present {
		t.Errorf("payload.queue_wait_ms must be absent (rejected from schema, see QueueAccepted doc-comment)")
	}
	if got := ev.Subject(); got != nil {
		t.Errorf("Subject = %v, want nil", got)
	}
}

// TestRestoreBreakdown_Shape pins the detailed restore timing contract. The
// timeline endpoint intentionally keeps data generic, so these keys are the
// compatibility surface consumed by the CLI and dashboard.
func TestRestoreBreakdown_Shape(t *testing.T) {
	ev := RestoreBreakdown{
		EmitAt: time.Unix(0, 0).UTC(), WakeID: "w-restore", AppID: "a-restore",
		InstanceID: "i-restore", ChrootMs: 2, MaterializeMemMs: 3,
		MaterializeVMStateMs: 4, ResolveImagesMs: 5, StageDrivesMs: 6,
		StageSnapshotMs: 7, HelperMs: 8, StartJailerMs: 9, BindTunMs: 10,
		LoadSnapshotMs: 400, ResumeHookMs: 11, WaitReadyMs: 131, TotalMs: 596,
	}
	if got := ev.Kind(); got != WakeRestoreBreakdown {
		t.Errorf("Kind = %q, want %q", got, WakeRestoreBreakdown)
	}
	p := ev.Payload()
	for key, want := range map[string]any{
		"wake_id":            "w-restore",
		"materialize_mem_ms": int64(3),
		"load_snapshot_ms":   int64(400),
		"total_ms":           int64(596),
	} {
		if got := p[key]; got != want {
			t.Errorf("payload[%q] = %v, want %v", key, got, want)
		}
	}
}

// TestAdmitted_ShapeOnAccountID — Subject() returns the account_id
// pointer so the audit row's subject column is populated; the
// Empty-string case collapses to nil (system event).
func TestAdmitted_ShapeOnAccountID(t *testing.T) {
	ev := Admitted{EmitAt: time.Unix(0, 0).UTC(), WakeID: "w-1", AppID: "a-1", AccountID: "acct-1", Plan: "hobby"}
	if got := ev.Subject(); got == nil || *got != "acct-1" {
		t.Errorf("Subject = %v, want pointer to acct-1", got)
	}
	empty := Admitted{EmitAt: time.Unix(0, 0).UTC()}
	if got := empty.Subject(); got != nil {
		t.Errorf("Empty AccountID Subject = %v, want nil", got)
	}
}

// TestStalled_WakeIDPreserved — the wake_id is the join key the
// customer-facing endpoint uses (GET
// /v1/apps/{slug}/wakes/{wake_id}/timeline). Catch a payload field
// rename that drops wake_id.
func TestStalled_WakeIDPreserved(t *testing.T) {
	ev := Stalled{WakeID: "w-123", AppID: "a-1", InstanceID: "i-1", NodeID: "n-1", Reason: "watchdog"}
	if got := ev.Payload()["wake_id"]; got != "w-123" {
		t.Errorf("payload.wake_id = %v, want w-123", got)
	}
	if got := ev.Payload()["reason"]; got != "watchdog" {
		t.Errorf("payload.reason = %v, want watchdog", got)
	}
}

// TestWakePhaseFromKind — the helper that strips the `wake.`
// prefix to feed the per-phase metric label. Catches typos.
func TestWakePhaseFromKind(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"wake.boot_started", "boot_started"},
		{"wake.readiness_200", "readiness_200"},
		{"wake.proxy_first_byte", "proxy_first_byte"},
		{"legacy", "legacy"}, // bare names pass through unchanged
	}
	for _, c := range cases {
		if got := wakePhaseFromKind(c.in); got != c.want {
			t.Errorf("wakePhaseFromKind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSidecarInitExit_Shape — issue #463 / ADR-069 / PR-B. The
// closed status enum ("init_ok" | "init_failed") is the load-bearing
// field: schedd's "did init fail" decision reads status, not exit_code,
// so the wire shape must stay stable. A typo on the status value
// would silently regress AC #1 (init non-zero exit → user_error).
//
// WakeID is set to "w-1" for the field-typing seam; production
// today emits "" (see struct doc). Downstream readers should
// treat the field as optional on the wake.*.sidecar_* kinds.
func TestSidecarInitExit_Shape(t *testing.T) {
	ev := SidecarInitExit{
		EmitAt: time.Unix(0, 0).UTC(), WakeID: "w-1", AppID: "a-1",
		InstanceID: "i-1", SidecarName: "metrics", Status: "init_failed",
		ExitCode: 1, DurationMs: 80,
	}
	if got := ev.Kind(); got != WakeSidecarInitExit {
		t.Errorf("Kind = %q, want %q", got, WakeSidecarInitExit)
	}
	if got := ev.Payload()["sidecar_name"]; got != "metrics" {
		t.Errorf("payload.sidecar_name = %v, want metrics", got)
	}
	if got := ev.Payload()["status"]; got != "init_failed" {
		t.Errorf("payload.status = %v, want init_failed", got)
	}
	if got := ev.Payload()["exit_code"]; got != 1 {
		t.Errorf("payload.exit_code = %v, want 1", got)
	}
	if got := ev.Payload()["duration_ms"]; got != int64(80) {
		t.Errorf("payload.duration_ms = %v, want 80", got)
	}
}

// TestSidecarRestart_Shape — issue #463 / ADR-069 / PR-B. Attempt
// is 1-indexed; PreviousExitCode lets operators distinguish OOM
// (137) from user_error (1) from signal-driven (-1) without
// joining against the vmmd log. WakeID is "w-1" for the field
// seam; production today emits "" (see struct doc).
func TestSidecarRestart_Shape(t *testing.T) {
	ev := SidecarRestart{
		EmitAt: time.Unix(0, 0).UTC(), WakeID: "w-1", AppID: "a-1",
		InstanceID: "i-1", SidecarName: "metrics", Attempt: 2, PreviousExitCode: 137,
	}
	if got := ev.Kind(); got != WakeSidecarRestart {
		t.Errorf("Kind = %q, want %q", got, WakeSidecarRestart)
	}
	if got := ev.Payload()["attempt"]; got != 2 {
		t.Errorf("payload.attempt = %v, want 2", got)
	}
	if got := ev.Payload()["previous_exit_code"]; got != 137 {
		t.Errorf("payload.previous_exit_code = %v, want 137", got)
	}
}

// TestBootStarted_Shape_IncludesNewFields — ADR-123. The new
// trigger / queued_count / concurrency_at_admit fields must surface
// in the payload so the wake timeline endpoint and dashboard see
// them. A typo on any of the three JSON keys would silently break
// the dashboard JOIN (`e.data->>'trigger'`) — catch it here.
func TestBootStarted_Shape_IncludesNewFields(t *testing.T) {
	ev := BootStarted{
		EmitAt: time.Unix(0, 0).UTC(), WakeID: "w-1", AppID: "a-1",
		InstanceID: "i-1", NodeID: "n-1", Method: "cold_boot",
		Trigger: "gateway", QueuedCount: 8, ConcurrencyAtAdmit: 2,
		AtCapacity: true,
	}
	p := ev.Payload()
	if got := p["trigger"]; got != "gateway" {
		t.Errorf("payload.trigger = %v, want gateway", got)
	}
	if got := p["queued_count"]; got != 8 {
		t.Errorf("payload.queued_count = %v, want 8", got)
	}
	if got := p["concurrency_at_admit"]; got != 2 {
		t.Errorf("payload.concurrency_at_admit = %v, want 2", got)
	}
	if got := p["method"]; got != "cold_boot" {
		t.Errorf("payload.method = %v, want cold_boot (existing field regression)", got)
	}
}

// TestBootStarted_AtCapacity — PR-A. The new `at_capacity` field
// must surface in the payload as a JSON bool (NOT a string "true")
// so the dashboard's `(data->>'at_capacity')::bool` cast gets the
// right type. A future contributor who accidentally stringifies
// the field breaks the recent-instances COALESCE — catch it here.
// Also pins the always-present contract: at_capacity is unconditional
// (unlike Trigger which is conditional), so both true and false
// leaves appear in the jsonb.
func TestBootStarted_AtCapacity(t *testing.T) {
	cases := []struct {
		name string
		in   bool
		want bool
	}{
		{"at-capacity wake", true, true},
		{"non-capacity wake", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := BootStarted{
				EmitAt:     time.Unix(0, 0).UTC(),
				WakeID:     "w",
				AppID:      "a",
				InstanceID: "i",
				NodeID:     "n",
				Method:     "cold_boot",
				AtCapacity: tc.in,
			}
			p := ev.Payload()
			v, present := p["at_capacity"]
			if !present {
				t.Fatalf("payload.at_capacity missing (must be unconditional — see PR-A plan §A)")
			}
			if v != tc.want {
				t.Errorf("payload.at_capacity = %v (%T), want %v", v, v, tc.want)
			}
		})
	}
}

// TestBootStarted_BackCompat_OldLiteralStillEmitsValidPayload —
// ADR-123 backward-compat. A literal constructed without the new
// fields (the Phase-1 fast-path return at engine.go:1119, where
// the trigger is "" because the row is not the customer-facing one)
// must still emit a valid payload: trigger key absent, but
// queued_count + concurrency_at_admit present (as 0, the cold start
// case). Mirrors the `TestQueueAccepted_Shape` rejection-guard
// pattern for `queue_wait_ms`.
func TestBootStarted_BackCompat_OldLiteralStillEmitsValidPayload(t *testing.T) {
	ev := BootStarted{EmitAt: time.Unix(0, 0).UTC(), WakeID: "w-1", AppID: "a-1"}
	p := ev.Payload()
	if _, present := p["trigger"]; present {
		t.Errorf("payload.trigger must be absent when Trigger is \"\" (Phase-1 fast-path)")
	}
	if got, ok := p["queued_count"]; !ok || got != 0 {
		t.Errorf("payload.queued_count = %v (ok=%v), want 0 (zero-value, present)", got, ok)
	}
	if got, ok := p["concurrency_at_admit"]; !ok || got != 0 {
		t.Errorf("payload.concurrency_at_admit = %v (ok=%v), want 0 (zero-value, present)", got, ok)
	}
}

// TestBootCompleted_Shape_IncludesNewFields — ADR-123. Mirror of
// TestBootStarted_Shape_IncludesNewFields; both rows carry the same
// snapshot so the customer timeline can join them on wake_id and
// see identical trigger / queued / concurrency values.
func TestBootCompleted_Shape_IncludesNewFields(t *testing.T) {
	ev := BootCompleted{
		EmitAt: time.Unix(0, 0).UTC(), WakeID: "w-1", AppID: "a-1",
		InstanceID: "i-1", NodeID: "n-1", Method: "cold_boot",
		StartedAt: time.Unix(0, 0).UTC(), CompletedAt: time.Unix(1, 0).UTC(),
		Trigger: "cron.schedule", QueuedCount: 0, ConcurrencyAtAdmit: 1,
	}
	p := ev.Payload()
	if got := p["trigger"]; got != "cron.schedule" {
		t.Errorf("payload.trigger = %v, want cron.schedule", got)
	}
	if got := p["queued_count"]; got != 0 {
		t.Errorf("payload.queued_count = %v, want 0 (cold start case)", got)
	}
	if got := p["concurrency_at_admit"]; got != 1 {
		t.Errorf("payload.concurrency_at_admit = %v, want 1", got)
	}
}
