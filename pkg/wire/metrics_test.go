// Tests for the OpsMetrics helper and the /metrics handler. We exercise:
//   - counter incremented per Observe call, labelled by op + code
//   - histogram observes per Observe call, labelled by op
//   - code label is "ok" on success and "err" on non-nil error
//   - the HTTP handler emits both series in the Prometheus text format

package wire_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestOpsMetrics_ObserveCounter(t *testing.T) {
	m := wire.NewOpsMetrics("vmmd")
	m.Observe("CreateFromSnapshot", 12*time.Millisecond, nil)
	m.Observe("CreateFromSnapshot", 10*time.Millisecond, nil)
	m.Observe("Stats", 200*time.Microsecond, errors.New("boom"))

	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	body := string(buf)

	for _, want := range []string{
		`vmmd_ops_total{code="ok",op="CreateFromSnapshot"} 2`,
		`vmmd_ops_total{code="err",op="Stats"} 1`,
		`vmmd_op_duration_seconds_count{op="CreateFromSnapshot"} 2`,
		`vmmd_op_duration_seconds_count{op="Stats"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
}

func TestOpsMetrics_IndependentRegistries(t *testing.T) {
	// Two daemons must not collide if they construct in the same process —
	// that's the point of per-daemon Registry over the global default.
	a := wire.NewOpsMetrics("vmmd")
	b := wire.NewOpsMetrics("builderd")
	a.Observe("ColdBoot", time.Millisecond, nil)
	b.Observe("Build", 50*time.Millisecond, nil)

	// vmmd's endpoint must NOT mention builderd series, and vice versa.
	bodyA := render(t, a)
	bodyB := render(t, b)

	if !strings.Contains(bodyA, `vmmd_ops_total{code="ok",op="ColdBoot"} 1`) {
		t.Errorf("vmmd endpoint missing vmmd series:\n%s", bodyA)
	}
	if strings.Contains(bodyA, "builderd_") {
		t.Errorf("vmmd endpoint leaked builderd:\n%s", bodyA)
	}
	if !strings.Contains(bodyB, `builderd_ops_total{code="ok",op="Build"} 1`) {
		t.Errorf("builderd endpoint missing builderd series:\n%s", bodyB)
	}
	if strings.Contains(bodyB, "vmmd_") {
		t.Errorf("builderd endpoint leaked vmmd:\n%s", bodyB)
	}
}

func render(t *testing.T, m *wire.OpsMetrics) string {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	body, err := readAll(t, srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return body
}

func TestOpsMetrics_ObserveBuild(t *testing.T) {
	m := wire.NewOpsMetrics("builderd")
	m.ObserveBuildCount("ok")
	m.ObserveBuildCount("ok")
	m.ObserveBuildCount("cache_hit")
	m.ObserveBuildCount("user_error")
	m.ObserveBuildDuration("ok", 42*time.Second)
	m.ObserveBuildDuration("cache_hit", 200*time.Millisecond)
	m.ObserveBuildQueueWait(3 * time.Second)

	body := render(t, m)
	for _, want := range []string{
		`builderd_ops_total{code="ok",op="build"} 2`,
		`builderd_ops_total{code="cache_hit",op="build"} 1`,
		`builderd_ops_total{code="user_error",op="build"} 1`,
		`builderd_build_duration_seconds_count{outcome="ok"} 1`,
		`builderd_build_duration_seconds_count{outcome="cache_hit"} 1`,
		`builderd_build_duration_seconds_count{outcome="failed"} 0`,
		`builderd_build_queue_wait_seconds_count 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
}

func TestOpsMetrics_ObserveBuildNilSafe(t *testing.T) {
	// builderd unit tests construct the orchestrator without metrics; the
	// observers must be no-ops on a nil receiver rather than panicking.
	var m *wire.OpsMetrics
	m.ObserveBuildCount("ok")
	m.ObserveBuildDuration("ok", time.Second)
	m.ObserveBuildQueueWait(time.Second)
}

func TestOpsMetrics_ObserveImagedOCIPull(t *testing.T) {
	// Same shape as the vmm-proxy test: real observations on a subset of
	// (op, result) tuples + zero-valued pre-instantiated buckets for the
	// rest of the closed set.
	m := wire.NewOpsMetrics("imaged")
	m.ObserveImagedOCIPull("manifest", "ok", 200*time.Millisecond)
	m.ObserveImagedOCIPull("blob", "ok", 5*time.Second)
	m.ObserveImagedOCIPull("blob", "err", 60*time.Second)
	m.ObserveImagedOCIPull("above_base", "ok", 800*time.Millisecond)

	body := render(t, m)
	for _, want := range []string{
		// Real observations.
		`imaged_oci_pull_duration_seconds_count{op="manifest",result="ok"} 1`,
		`imaged_oci_pull_duration_seconds_count{op="blob",result="ok"} 1`,
		`imaged_oci_pull_duration_seconds_count{op="blob",result="err"} 1`,
		`imaged_oci_pull_duration_seconds_count{op="above_base",result="ok"} 1`,
		// Pre-instantiated tuples we never observed: still zero-valued.
		`imaged_oci_pull_duration_seconds_count{op="config",result="ok"} 0`,
		`imaged_oci_pull_duration_seconds_count{op="manifest",result="err"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
}

func TestOpsMetrics_NewObserversNilSafe(t *testing.T) {
	// imaged unit tests construct the orchestrator without metrics;
	// the new observer must be a no-op on a nil receiver.
	var m *wire.OpsMetrics
	m.ObserveImagedOCIPull("blob", "ok", time.Second)
}

// TestOpsMetrics_PgBackupLastPushed (issue #250) — the
// pg_backup_last_pushed_seconds gauge must:
//   - be registered under the per-daemon prefix,
//   - surface in /metrics from boot pre-instantiated to 0,
//   - accept Set() calls without panicking,
//   - return nil from the accessor on a nil receiver (nil-safe).
//
// The PgBackupStale alert rule (deploy/ansible/roles/prometheus/files/pg_backup.rules.yml)
// queries `time() - pg_backup_last_pushed_seconds > 86400`; without
// the gauge series from boot, a freshly-booted box looks identical
// to one with no basebackup root — both return NaN to the alert,
// and the alert is silently skipped. The pre-instantiated-to-0
// pattern (mirror of alertEvaluatorEnabled, line ~771) closes the
// gap.
func TestOpsMetrics_PgBackupLastPushed(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	body := render(t, m)
	want := `apid_pg_backup_last_pushed_seconds 0`
	if !strings.Contains(body, want) {
		t.Errorf("missing line %q in:\n%s", want, body)
	}
	// Set must not panic; the gauge accepts arbitrary float64 values
	// (apid's sampler writes time.Since(newest).Seconds()).
	m.PgBackupLastPushed().Set(3600)
	body = render(t, m)
	if !strings.Contains(body, `apid_pg_backup_last_pushed_seconds 3600`) {
		t.Errorf("gauge did not surface Set value:\n%s", body)
	}
}

// TestOpsMetrics_PgBackupLastPushedNilSafe — nil-receiver accessor.
func TestOpsMetrics_PgBackupLastPushedNilSafe(t *testing.T) {
	var m *wire.OpsMetrics
	if g := m.PgBackupLastPushed(); g != nil {
		t.Errorf("nil receiver returned non-nil gauge: %v", g)
	}
}

// TestOpsMetrics_EgressDenyRegistryPreinstantiated (PR-E) — every
// catalog (cidr, family) tuple must surface in /metrics from boot
// with value 0, mirroring the OCI-pull and build histogram
// pre-instantiation pattern. The wire test pins both the vmmd-side
// (prefix "vmmd") collector and the imaged-side (prefix "imaged")
// OCI mirror collector. Operators rely on the panel existing at
// day 1 — an idle box would otherwise render the panel as "no
// data" until at least one drop had been observed.
func TestOpsMetrics_EgressDenyRegistryPreinstantiated(t *testing.T) {
	cases := []struct {
		prefix      string
		wantCatalog []string
		wantOCIOnly []string // only on "imaged"
	}{
		{
			prefix: "vmmd",
			// Catalog pre-instantiation covers the firewall-side counter.
			wantCatalog: []string{
				`vmmd_egress_deny_total{cidr="drop_v4_10_0_0_0_8",family="ip"} 0`,
				`vmmd_egress_deny_total{cidr="drop_v6_fe80___10",family="ip6"} 0`,
				`vmmd_egress_deny_total{cidr="drop_v6_2002___16",family="ip6"} 0`,
			},
		},
		{
			prefix: "imaged",
			// Imaged registry also gets the OCI-mirror collector with
			// the catalog portion pre-instantiated (OCI-only extras are
			// pre-instantiated from cmd/imaged/main.go so wire doesn't
			// import pkg/oci).
			wantCatalog: []string{
				`imaged_egress_deny_total{cidr="drop_v4_10_0_0_0_8",family="ip"} 0`,
				`imaged_oci_egress_deny_total{cidr="drop_v4_10_0_0_0_8",family="ip"} 0`,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.prefix, func(t *testing.T) {
			m := wire.NewOpsMetrics(c.prefix)
			body := render(t, m)
			for _, want := range c.wantCatalog {
				if !strings.Contains(body, want) {
					t.Errorf("missing line %q in:\n%s", want, body)
				}
			}
		})
	}
}

// TestOpsMetrics_EgressDenyIncrement (PR-E) — the public
// EgressDeny accessor increments the per-(cidr, family) counter
// and the value surfaces in /metrics. Asserts the wire path
// end-to-end (the cmd/vmmd poller + cmd/imaged hook both rely on
// this).
func TestOpsMetrics_EgressDenyIncrement(t *testing.T) {
	m := wire.NewOpsMetrics("vmmd")
	m.EgressDeny("drop_v4_10_0_0_0_8", "ip").Add(7)
	body := render(t, m)
	want := `vmmd_egress_deny_total{cidr="drop_v4_10_0_0_0_8",family="ip"} 7`
	if !strings.Contains(body, want) {
		t.Errorf("missing line %q in:\n%s", want, body)
	}
}

// TestOpsMetrics_OCIEgressDenyIncrement (PR-E) — the imaged-side
// mirror. Only registered when prefix == "imaged"; nil-safe
// accessor on every other prefix.
func TestOpsMetrics_OCIEgressDenyIncrement(t *testing.T) {
	m := wire.NewOpsMetrics("imaged")
	m.OCIEgressDeny("drop_v4_10_0_0_0_8", "ip").Inc()
	body := render(t, m)
	want := `imaged_oci_egress_deny_total{cidr="drop_v4_10_0_0_0_8",family="ip"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("missing line %q in:\n%s", want, body)
	}

	// Non-imaged registries: the accessor returns nil (a no-op counter).
	// The wire contract — cmd/vmmd only ever calls EgressDeny, cmd/imaged
	// only ever calls OCIEgressDeny — is enforced by the prefix check in
	// NewOpsMetrics, not by the accessor. Verify the accessor is safe
	// on a non-imaged registry (no panic, no /metrics line).
	vmmd := wire.NewOpsMetrics("vmmd")
	if c := vmmd.OCIEgressDeny("drop_v4_10_0_0_0_8", "ip"); c != nil {
		t.Errorf("vmmd.OCIEgressDeny = %v, want nil", c)
	}
	vmmdBody := render(t, vmmd)
	// The vmmd registry must NOT have a counter NAMED oci_egress_deny_total
	// (only the firewall-side vmmd_egress_deny_total). The HELP text for
	// vmmd_egress_deny_total mentions "oci_egress_deny_total" in its
	// description; substring match would falsely trip on that. Anchor
	// the check on the metric-name declaration line.
	if strings.Contains(vmmdBody, "# TYPE vmmd_oci_egress_deny_total counter") {
		t.Errorf("vmmd registry should not contain vmmd_oci_egress_deny_total, got:\n%s", vmmdBody)
	}
}

// TestOpsMetrics_EgressDenyNilSafe (PR-E) — the accessor must be
// no-op on a nil receiver so vmmd / imaged unit tests without
// metrics keep working (same nil-safe posture as Observe*).
func TestOpsMetrics_EgressDenyNilSafe(t *testing.T) {
	var m *wire.OpsMetrics
	if got := m.EgressDeny("drop_v4_10_0_0_0_8", "ip"); got != nil {
		t.Errorf("nil.EgressDeny = %v, want nil", got)
	}
	if got := m.OCIEgressDeny("drop_v4_10_0_0_0_8", "ip"); got != nil {
		t.Errorf("nil.OCIEgressDeny = %v, want nil", got)
	}
}

// TestOpsMetrics_ObserveScaleDown (issue #171) — the aggressive-
// reaper observer increments the per-(app, outcome) counter and the
// value surfaces in /metrics. Pins the wire path end-to-end. Two
// `park` observations on `app="a1"` + one `keep` + pre-instantiated
// empty-app placeholders. Mirrors TestOpsMetrics_ObserveBuild.
func TestOpsMetrics_ObserveScaleDown(t *testing.T) {
	m := wire.NewOpsMetrics("schedd")
	m.ObserveScaleDown("a1", "park")
	m.ObserveScaleDown("a1", "park")
	m.ObserveScaleDown("a1", "keep")

	body := render(t, m)
	for _, want := range []string{
		// Real observations.
		`schedd_scale_down_decisions_total{app="a1",outcome="park"} 2`,
		`schedd_scale_down_decisions_total{app="a1",outcome="keep"} 1`,
		// Pre-instantiated empty-app placeholder: zero-valued, must
		// surface in /metrics from boot so the panel exists at day 1.
		// min_floor_already (PR-C, issue #462) is pre-instantiated
		// alongside park / keep so the closed outcome label set
		// is fully surfaced from boot.
		`schedd_scale_down_decisions_total{app="",outcome="park"} 0`,
		`schedd_scale_down_decisions_total{app="",outcome="keep"} 0`,
		`schedd_scale_down_decisions_total{app="",outcome="min_floor_already"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
}

// TestOpsMetrics_ObserveScaleDownNilSafe — schedd unit tests
// construct the engine without metrics; the observer must be a
// no-op on a nil receiver rather than panicking.
func TestOpsMetrics_ObserveScaleDownNilSafe(t *testing.T) {
	var m *wire.OpsMetrics
	m.ObserveScaleDown("a1", "park")
}

// TestOpsMetrics_ObserveScaleUpClosedSet (PR-C, issue #462) —
// pins the scale-up outcome label set: pre-instantiated rows
// for the closed set {admit, reject_at_cap, no_signal,
// cooldown_held} must surface in /metrics from boot with the
// empty-app placeholder. Mirrors TestOpsMetrics_ObserveScaleDown
// for the scale-up side. The cooldown_held outcome is the new
// wake-gate path emission added by PR-C.
func TestOpsMetrics_ObserveScaleUpClosedSet(t *testing.T) {
	m := wire.NewOpsMetrics("schedd")
	m.ObserveScaleUp("a1", "admit")
	m.ObserveScaleUp("a1", "cooldown_held")

	body := render(t, m)
	for _, want := range []string{
		// Real observations.
		`schedd_scale_up_decisions_total{app="a1",outcome="admit"} 1`,
		`schedd_scale_up_decisions_total{app="a1",outcome="cooldown_held"} 1`,
		// Pre-instantiated empty-app placeholder for the closed set.
		`schedd_scale_up_decisions_total{app="",outcome="admit"} 0`,
		`schedd_scale_up_decisions_total{app="",outcome="reject_at_cap"} 0`,
		`schedd_scale_up_decisions_total{app="",outcome="no_signal"} 0`,
		`schedd_scale_up_decisions_total{app="",outcome="cooldown_held"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
}

// TestOpsMetrics_ObserveLogEmitted (issue #254, Move 4) — the per-app
// SSE log frame counter increments on each ObserveLogEmitted call and
// the value surfaces in /metrics under apid_logs_emitted_total{app}.
// Pins the wire path end-to-end so an accidental rename in the metric
// name or the label set trips the test before the dashboard panel
// goes dark.
//
// The series is registered on every daemon (single-registry pattern,
// per memory wire-opsmetrics-single-registry); the test constructs an
// apid-flavored OpsMetrics so the absolute metric name matches the
// production path.
func TestOpsMetrics_ObserveLogEmitted(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	m.ObserveLogEmitted("app-1")
	m.ObserveLogEmitted("app-1")
	m.ObserveLogEmitted("app-2")

	body := render(t, m)
	for _, want := range []string{
		`apid_logs_emitted_total{app="app-1"} 2`,
		`apid_logs_emitted_total{app="app-2"} 1`,
		// The metric is registered on every daemon (including apid),
		// so the HELP/TYPE must surface in /metrics from boot — even
		// before any frame has been emitted. Verifies the
		// commonCollectors append was applied.
		`# HELP apid_logs_emitted_total`,
		`# TYPE apid_logs_emitted_total counter`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
}

// TestOpsMetrics_ObserveLogEmittedNilSafe — handlers without a
// metrics registry (unit tests, throwaway scripts) must not panic
// when the SSE handler renders its first frame. Same nil-receiver
// contract as the other Observers.
func TestOpsMetrics_ObserveLogEmittedNilSafe(t *testing.T) {
	var m *wire.OpsMetrics
	m.ObserveLogEmitted("app-1")
}

// TestOpsMetrics_LogEmittedAcrossPrefixes pins the prefix-on-every-
// daemon contract (per memory wire-opsmetrics-single-registry):
// every OpsMetrics instance — regardless of prefix — has the
// _logs_emitted_total CounterVec pre-instantiated. Only apid's
// production path increments via ObserveLogEmitted; the others sit
// at zero. A regression that scopes the collector to a single
// prefix trips this test before the operator dashboard panel
// goes dark on a non-apid box.
func TestOpsMetrics_LogEmittedAcrossPrefixes(t *testing.T) {
	for _, prefix := range []string{"apid", "vmmd", "schedd", "imaged", "meterd", "builderd"} {
		m := wire.NewOpsMetrics(prefix)
		m.ObserveLogEmitted("any-app")
		body := render(t, m)
		// Metric name is "<prefix>_logs_emitted_total" — confirm
		// the literal string is present, not just a substring.
		want := prefix + `_logs_emitted_total{app="any-app"} 1`
		if !strings.Contains(body, want) {
			t.Errorf("prefix=%s missing %q in:\n%s", prefix, want, body)
		}
	}
}

func TestRenderSeconds(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{time.Millisecond, "0.001"},
		{500 * time.Microsecond, "0.0005"},
		{2 * time.Second, "2"},
	} {
		if got := wire.RenderSeconds(tc.in); got != tc.want {
			t.Errorf("RenderSeconds(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOpsMetrics_RegistryAccess(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	if m.Registry() == nil {
		t.Fatal("Registry() returned nil")
	}
	// Observe something so the CounterVec has a series to gather.
	m.Observe("whoami", time.Millisecond, nil)
	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(mfs) == 0 {
		t.Error("expected at least one metric family after construction")
	}
}

func TestOpsMetrics_HandlerStandalone(t *testing.T) {
	// Handler() must be usable without an httptest server wrapper — that's
	// the form daemons actually mount onto their main mux.
	m := wire.NewOpsMetrics("meterd")
	m.Observe("tick", time.Millisecond, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `meterd_ops_total{code="ok",op="tick"} 1`) {
		t.Errorf("body missing tick series:\n%s", rec.Body.String())
	}
}

func readAll(t *testing.T, url string) (string, error) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return string(buf), nil
		}
	}
}

// TestOpsMetrics_SnapshotDiskDriftRegistered — the
// snapshot_disk_drift_total counter is registered on every daemon's
// OpsMetrics (single-registry pattern, memory
// wire.NewOpsMetrics single-registry pattern). It only produces
// samples when schedd's DiskDrift.Tick observes a discrepancy, but
// the collector must exist on every daemon's registry so a unified
// scrape never fails with "unknown metric."
func TestOpsMetrics_SnapshotDiskDriftRegistered(t *testing.T) {
	m := wire.NewOpsMetrics("schedd")
	c := m.SnapshotDiskDrift()
	if c == nil {
		t.Fatal("SnapshotDiskDrift() = nil on non-nil receiver")
	}
	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var found bool
	for _, fam := range mfs {
		if fam.GetName() == "schedd_snapshot_disk_drift_total" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("schedd_snapshot_disk_drift_total not present in registry gather")
	}

	// Increment and re-gather: counter should now read 1.
	c.Inc()
	mfs, err = m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather after Inc: %v", err)
	}
	for _, fam := range mfs {
		if fam.GetName() == "schedd_snapshot_disk_drift_total" {
			for _, mt := range fam.GetMetric() {
				if got := mt.GetCounter().GetValue(); got != 1 {
					t.Errorf("counter = %v, want 1 after one Inc", got)
				}
			}
			return
		}
	}
	t.Fatal("counter disappeared after Inc")
}

// TestOpsMetrics_SnapshotDiskDriftNilSafe — DiskDrift.Tick calls
// SnapshotDiskDrift() without guarding the receiver; the accessor
// itself must short-circuit on a nil receiver so a partially wired
// DiskDrift (or a test that constructs the struct directly) doesn't
// panic on every Tick.
func TestOpsMetrics_SnapshotDiskDriftNilSafe(t *testing.T) {
	var m *wire.OpsMetrics
	if got := m.SnapshotDiskDrift(); got != nil {
		t.Errorf("SnapshotDiskDrift on nil receiver = %v, want nil", got)
	}
}

// TestOpsMetrics_ObserveOAuthDisabled (issue #419 / ADR-046) — the
// sign-in OAuth consent handlers increment
// `apid_oauth_disabled_total{provider}` on every 503
// `oauth_provider_unavailable` response. The accessor must:
//   - increment by 1 for the closed set ("google", "github"),
//   - leave the metric untouched for unknown providers so a
//     future caller can't widen the label set by accident,
//   - be no-op on a nil receiver so apid unit tests that don't
//     wire metrics keep working (parity with ObserveLogEmitted
//     above).
func TestOpsMetrics_ObserveOAuthDisabled(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	m.ObserveOAuthDisabled("google")
	m.ObserveOAuthDisabled("google")
	m.ObserveOAuthDisabled("github")
	// Unknown provider: accessor must not create a label series.
	m.ObserveOAuthDisabled("facebook")

	body := render(t, m)
	wantGoogle := `apid_oauth_disabled_total{provider="google"} 2`
	if !strings.Contains(body, wantGoogle) {
		t.Errorf("missing line %q in:\n%s", wantGoogle, body)
	}
	wantGitHub := `apid_oauth_disabled_total{provider="github"} 1`
	if !strings.Contains(body, wantGitHub) {
		t.Errorf("missing line %q in:\n%s", wantGitHub, body)
	}
	if strings.Contains(body, `provider="facebook"`) {
		t.Errorf("facebook label series must not be created, got:\n%s", body)
	}

	// Nil-receiver parity with the other Observe* accessors.
	var nilM *wire.OpsMetrics
	nilM.ObserveOAuthDisabled("google") // must not panic
}

// TestOpsMetrics_ObserveAdvisoryBatchResult (Mega-PR B) — the
// stateless-advisory forward outcome counter increments per
// closed-set result value (ok / dial_failed / rejected /
// unavailable_after_retry) and refuses to widen the label set
// for unknown values, mirroring the OAuth-counter pattern above.
// Pair-counter with stateless_advisory_events_total: a healthy
// box has rate(apid_..{severity="high"})[5m] ≈
// rate(vmmd_..{result="ok"})[5m].
func TestOpsMetrics_ObserveAdvisoryBatchResult(t *testing.T) {
	m := wire.NewOpsMetrics("vmmd")
	m.ObserveAdvisoryBatchResult("ok")
	m.ObserveAdvisoryBatchResult("ok")
	m.ObserveAdvisoryBatchResult("dial_failed")
	m.ObserveAdvisoryBatchResult("rejected")
	m.ObserveAdvisoryBatchResult("unavailable_after_retry")
	// Unknown result: accessor must NOT create a label series.
	m.ObserveAdvisoryBatchResult("mystery")

	body := render(t, m)
	wantLines := []string{
		`vmmd_stateless_advisory_batches_emitted_total{result="ok"} 2`,
		`vmmd_stateless_advisory_batches_emitted_total{result="dial_failed"} 1`,
		`vmmd_stateless_advisory_batches_emitted_total{result="rejected"} 1`,
		`vmmd_stateless_advisory_batches_emitted_total{result="unavailable_after_retry"} 1`,
	}
	for _, want := range wantLines {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, `result="mystery"`) {
		t.Errorf("mystery label series must not be created, got:\n%s", body)
	}

	// Nil-receiver parity.
	var nilM *wire.OpsMetrics
	nilM.ObserveAdvisoryBatchResult("ok") // must not panic
}

// TestOpsMetrics_PreInstantiatesAdvisoryBatchSeries — the closed
// result set must surface in /metrics from the moment the
// daemon boots, all four rows at value 0. This is the
// pre-instantiation contract that lets the §12 dashboard panel
// render "no data → 0" rather than "no data → missing series"
// on an idle box.
func TestOpsMetrics_PreInstantiatesAdvisoryBatchSeries(t *testing.T) {
	m := wire.NewOpsMetrics("vmmd")
	body := render(t, m)
	for _, result := range []string{"ok", "dial_failed", "rejected", "unavailable_after_retry"} {
		want := `vmmd_stateless_advisory_batches_emitted_total{result="` + result + `"} 0`
		if !strings.Contains(body, want) {
			t.Errorf("pre-instantiated line %q missing in:\n%s", want, body)
		}
	}
}

// TestOpsMetrics_ObserveStatelessAdvisory (Mega-PR B) — apid
// receiver-side counter. Same closed-set semantics as the vmmd
// forward counter but labels by severity ∈ {high, warn, info}.
// Mirrors cmd/apid/advisory_receiver.go's advisoryBatchSeverity
// vocabulary; an unknown severity (e.g. "urgent") must NOT
// create a label series.
func TestOpsMetrics_ObserveStatelessAdvisory(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	m.ObserveStatelessAdvisory("high")
	m.ObserveStatelessAdvisory("high")
	m.ObserveStatelessAdvisory("warn")
	m.ObserveStatelessAdvisory("info")
	// Unknown severity: closed-set guard.
	m.ObserveStatelessAdvisory("urgent")

	body := render(t, m)
	wantLines := []string{
		`apid_stateless_advisory_events_total{severity="high"} 2`,
		`apid_stateless_advisory_events_total{severity="warn"} 1`,
		`apid_stateless_advisory_events_total{severity="info"} 1`,
	}
	for _, want := range wantLines {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, `severity="urgent"`) {
		t.Errorf("urgent label series must not be created, got:\n%s", body)
	}

	// Nil-receiver parity.
	var nilM *wire.OpsMetrics
	nilM.ObserveStatelessAdvisory("high") // must not panic
}

// TestOpsMetrics_PreInstantiatesStatelessAdvisorySeries — the
// closed severity set must surface in /metrics from boot, all
// three rows at value 0. Same pre-instantiation contract as the
// vmmd forward counter above.
func TestOpsMetrics_PreInstantiatesStatelessAdvisorySeries(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	body := render(t, m)
	for _, sev := range []string{"high", "warn", "info"} {
		want := `apid_stateless_advisory_events_total{severity="` + sev + `"} 0`
		if !strings.Contains(body, want) {
			t.Errorf("pre-instantiated line %q missing in:\n%s", want, body)
		}
	}
}

// TestOpsMetrics_ObserveGithubdPathFilter (issue #432 phase 5 /
// ADR-050 §109). The path-filter mode counter is labelled by
// mode ∈ {paths, full_fallback, truncated, error, breaker_open}.
// Closed-set semantics — an unknown mode (e.g. "discarded")
// must NOT create a label series, and the nil-receiver must
// not panic.
func TestOpsMetrics_ObserveGithubdPathFilter(t *testing.T) {
	m := wire.NewOpsMetrics("githubd")
	m.ObserveGithubdPathFilter(wire.PathFilterModePaths)
	m.ObserveGithubdPathFilter(wire.PathFilterModePaths)
	m.ObserveGithubdPathFilter(wire.PathFilterModeFullFallback)
	m.ObserveGithubdPathFilter(wire.PathFilterModeTruncated)
	m.ObserveGithubdPathFilter(wire.PathFilterModeError)
	m.ObserveGithubdPathFilter(wire.PathFilterModeBreakerOpen)
	// Unknown mode: closed-set guard.
	m.ObserveGithubdPathFilter("discarded")

	body := render(t, m)
	wantLines := []string{
		`githubd_path_filter_total{mode="paths"} 2`,
		`githubd_path_filter_total{mode="full_fallback"} 1`,
		`githubd_path_filter_total{mode="truncated"} 1`,
		`githubd_path_filter_total{mode="error"} 1`,
		`githubd_path_filter_total{mode="breaker_open"} 1`,
	}
	for _, want := range wantLines {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, `mode="discarded"`) {
		t.Errorf("discarded label series must not be created, got:\n%s", body)
	}

	// Nil-receiver parity.
	var nilM *wire.OpsMetrics
	nilM.ObserveGithubdPathFilter(wire.PathFilterModePaths) // must not panic
}

// TestOpsMetrics_PreInstantiatesGithubdPathFilterSeries — the
// closed `mode` label set must surface in /metrics from boot,
// all five rows at value 0. Same pre-instantiation contract as
// every other CounterVec on this struct.
func TestOpsMetrics_PreInstantiatesGithubdPathFilterSeries(t *testing.T) {
	m := wire.NewOpsMetrics("githubd")
	body := render(t, m)
	for _, mode := range []string{wire.PathFilterModePaths, wire.PathFilterModeFullFallback, wire.PathFilterModeTruncated, wire.PathFilterModeError, wire.PathFilterModeBreakerOpen} {
		want := `githubd_path_filter_total{mode="` + mode + `"} 0`
		if !strings.Contains(body, want) {
			t.Errorf("pre-instantiated line %q missing in:\n%s", want, body)
		}
	}
}
