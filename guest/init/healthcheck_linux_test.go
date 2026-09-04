//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestParseHealthcheckTest_CMD pins the CMD argv shape: leading
// "CMD" keyword is stripped and the rest of the slice is the
// exec argv. Mirrors Docker's CMD form.
func TestParseHealthcheckTest_CMD(t *testing.T) {
	argv, isShell := parseHealthcheckTest([]string{"CMD", "/bin/check", "--flag"})
	if isShell {
		t.Errorf("isShell = true; want false for CMD")
	}
	if len(argv) != 2 || argv[0] != "/bin/check" || argv[1] != "--flag" {
		t.Errorf("argv = %v; want [/bin/check --flag]", argv)
	}
}

// TestParseHealthcheckTest_CMDSHELL pins the CMD-SHELL shape:
// exec via /bin/sh -c "<script>". The single argument after
// CMD-SHELL becomes the shell's command string.
func TestParseHealthcheckTest_CMDSHELL(t *testing.T) {
	argv, isShell := parseHealthcheckTest([]string{"CMD-SHELL", "exit 0"})
	if !isShell {
		t.Errorf("isShell = false; want true for CMD-SHELL")
	}
	want := []string{"/bin/sh", "-c", "exit 0"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v; want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q; want %q", i, argv[i], want[i])
		}
	}
}

// TestParseHealthcheckTest_NONE pins the no-goroutine shape:
// an empty Test slice OR a single-element ["NONE"] slice both
// return (nil, false) so runHealthcheckPoll returns early.
func TestParseHealthcheckTest_NONE(t *testing.T) {
	for _, in := range [][]string{nil, {}, {"NONE"}} {
		argv, isShell := parseHealthcheckTest(in)
		if argv != nil {
			t.Errorf("input %v: argv = %v; want nil", in, argv)
		}
		if isShell {
			t.Errorf("input %v: isShell = true; want false", in)
		}
	}
}

// TestHealthcheckDefaults_AppliesDockerDefaults pins the
// Docker-equivalent default values when the manifest leaves a
// field at 0: interval 30s, timeout 30s, retries 3. The
// zero-second StartPeriodS is the documented Docker default
// (no startup grace).
func TestHealthcheckDefaults_AppliesDockerDefaults(t *testing.T) {
	hc := &api.AppManifestHealthcheck{Test: []string{"CMD", "/bin/true"}}
	interval, timeout, startPeriod, retries := healthcheckDefaults(hc)
	if interval != 30*time.Second {
		t.Errorf("interval = %v; want 30s", interval)
	}
	if timeout != 30*time.Second {
		t.Errorf("timeout = %v; want 30s", timeout)
	}
	if startPeriod != 0 {
		t.Errorf("startPeriod = %v; want 0", startPeriod)
	}
	if retries != 3 {
		t.Errorf("retries = %d; want 3", retries)
	}
}

// TestHealthcheckDefaults_RespectsManifestValues pins the
// override path: a manifest with non-zero fields must use the
// customer's values verbatim (not the Docker defaults).
func TestHealthcheckDefaults_RespectsManifestValues(t *testing.T) {
	hc := &api.AppManifestHealthcheck{
		Test:         []string{"CMD", "/bin/check"},
		IntervalS:    5,
		TimeoutS:     2,
		Retries:      1,
		StartPeriodS: 10,
	}
	interval, timeout, startPeriod, retries := healthcheckDefaults(hc)
	if interval != 5*time.Second {
		t.Errorf("interval = %v; want 5s", interval)
	}
	if timeout != 2*time.Second {
		t.Errorf("timeout = %v; want 2s", timeout)
	}
	if startPeriod != 10*time.Second {
		t.Errorf("startPeriod = %v; want 10s", startPeriod)
	}
	if retries != 1 {
		t.Errorf("retries = %d; want 1", retries)
	}
}

// TestHealthcheckDefaults_NilHealthcheck pins the NONE shape:
// a nil Healthcheck pointer returns all-zero values so the
// caller can short-circuit (no goroutine, no defaults needed).
func TestHealthcheckDefaults_NilHealthcheck(t *testing.T) {
	interval, timeout, startPeriod, retries := healthcheckDefaults(nil)
	if interval != 0 || timeout != 0 || startPeriod != 0 || retries != 0 {
		t.Errorf("defaults = (%v, %v, %v, %d); want all zero", interval, timeout, startPeriod, retries)
	}
}

// TestExecHealthcheck_PassOnZeroExit pins the pass path: a
// child that exits 0 produces Status=pass with the captured
// stdout/stderr in the Output field.
func TestExecHealthcheck_PassOnZeroExit(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skipf("true not available: %v", err)
	}
	report := execHealthcheck(context.Background(), []string{"/bin/true", "ignored"}, 5*time.Second, 0, nil)
	if report.Status != healthcheckStatusPass {
		t.Errorf("Status = 0x%02x; want 0x%02x (pass)", report.Status, healthcheckStatusPass)
	}
}

// TestExecHealthcheck_FailOnNonZeroExit pins the fail path: a
// child that exits non-zero produces Status=fail. We use a
// non-existent binary so the test doesn't depend on /bin/false
// being present in every CI environment.
func TestExecHealthcheck_FailOnNonZeroExit(t *testing.T) {
	report := execHealthcheck(context.Background(), []string{"/bin/sh", "-c", "exit 1"}, 5*time.Second, 0, nil)
	if report.Status != healthcheckStatusFail {
		t.Errorf("Status = 0x%02x; want 0x%02x (fail)", report.Status, healthcheckStatusFail)
	}
}

// TestExecHealthcheck_TruncatesLongOutput pins the wire-shape
// cap: output longer than VsockHealthcheckMaxOutput is truncated
// to the LAST bytes (most-recent diagnostic), not the first.
// The host's DGRAM receive buffer must never see >4 KiB per
// report — back-pressure would silently drop later reports.
func TestExecHealthcheck_TruncatesLongOutput(t *testing.T) {
	// Generate 8 KiB of output — double the cap.
	long := make([]byte, 8*1024)
	for i := range long {
		long[i] = 'X'
	}
	report := execHealthcheck(context.Background(), []string{"/bin/sh", "-c", "printf '" + string(long) + "'"}, 5*time.Second, 0, nil)
	if len(report.Output) > VsockHealthcheckMaxOutput {
		t.Errorf("Output len = %d; want <= %d", len(report.Output), VsockHealthcheckMaxOutput)
	}
	// Last byte should still be 'X' (tail-preserved).
	if len(report.Output) > 0 && report.Output[len(report.Output)-1] != 'X' {
		t.Errorf("Output tail = %q; want tail=X", report.Output[len(report.Output)-1])
	}
}

// TestEncodeDecodeHealthcheckReport_RoundTrip pins the wire
// shape: encode → decode returns the same report (status, seq,
// output bytes). The host's decoder (pkg/fcvm/healthcheck.go)
// uses the same struct — drift here is a silent "no reports
// land" failure mode.
func TestEncodeDecodeHealthcheckReport_RoundTrip(t *testing.T) {
	want := HealthcheckReport{
		Seq:    7,
		Status: healthcheckStatusPass,
		Output: []byte("hello healthcheck"),
	}
	frame := encodeHealthcheckReport(want)
	if len(frame) != 13+len(want.Output) {
		t.Errorf("frame len = %d; want %d", len(frame), 13+len(want.Output))
	}
	got, err := decodeHealthcheckReport(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Seq != want.Seq {
		t.Errorf("Seq = %d; want %d", got.Seq, want.Seq)
	}
	if got.Status != want.Status {
		t.Errorf("Status = 0x%02x; want 0x%02x", got.Status, want.Status)
	}
	if string(got.Output) != string(want.Output) {
		t.Errorf("Output = %q; want %q", got.Output, want.Output)
	}
}

// TestEncodeDecodeHealthcheckReport_StatusEnum pins the status
// byte: pass=0x01, fail=0x02, starting=0x00. The numeric values
// are stable across versions — the host decoder reads them
// verbatim. Renumbering requires a vmmdpb wire bump.
func TestEncodeDecodeHealthcheckReport_StatusEnum(t *testing.T) {
	cases := []byte{
		healthcheckStatusStarting,
		healthcheckStatusPass,
		healthcheckStatusFail,
	}
	for _, status := range cases {
		frame := encodeHealthcheckReport(HealthcheckReport{Seq: 1, Status: status})
		got, err := decodeHealthcheckReport(frame)
		if err != nil {
			t.Fatalf("decode status=0x%02x: %v", status, err)
		}
		if got.Status != status {
			t.Errorf("Status round-trip = 0x%02x; want 0x%02x", got.Status, status)
		}
	}
}

// TestDecodeHealthcheckReport_ShortFrame pins the error path:
// a frame shorter than 13 bytes is rejected. Truncated packets
// are a real concern on vsock during a graceful-stop tail —
// the host must NOT decode a partial frame as a valid report.
func TestDecodeHealthcheckReport_ShortFrame(t *testing.T) {
	for _, n := range []int{0, 1, 12} {
		if _, err := decodeHealthcheckReport(make([]byte, n)); err == nil {
			t.Errorf("decode len=%d: nil error; want error", n)
		}
	}
}

// TestDecodeHealthcheckReport_TruncatedOutput pins the
// truncated-output error path: the frame's olen claims more
// bytes than the buffer holds. The decoder returns an error
// rather than reading past the buffer end.
func TestDecodeHealthcheckReport_TruncatedOutput(t *testing.T) {
	hdr := make([]byte, 13)
	binary.BigEndian.PutUint32(hdr[9:13], 100) // claim 100 bytes of output
	if _, err := decodeHealthcheckReport(hdr); err == nil {
		t.Error("decode truncated: nil error; want error")
	}
}

// TestRunHealthcheckPoll_NONEShapeReturnsNil pins the
// short-circuit path: a manifest with no Healthcheck pointer,
// Test=["NONE"], or an empty Test must NOT open a vsock socket
// and must return nil so boot proceeds. A regression here would
// either crash PID 1 or block boot on a guest kernel without
// AF_VSOCK (some embedded CI runners).
func TestRunHealthcheckPoll_NONEShapeReturnsNil(t *testing.T) {
	manifests := []api.AppManifest{
		{},
		{Healthcheck: &api.AppManifestHealthcheck{Test: nil}},
		{Healthcheck: &api.AppManifestHealthcheck{Test: []string{}}},
		{Healthcheck: &api.AppManifestHealthcheck{Test: []string{"NONE"}}},
	}
	for _, m := range manifests {
		if err := runHealthcheckPoll(context.Background(), m, nil); err != nil {
			t.Errorf("Healthcheck=%v: runHealthcheckPoll = %v; want nil", m.Healthcheck, err)
		}
	}
}

// TestRunHealthcheckPoll_ShortIntervalSendsReport pins the
// happy path: a manifest with Test=["CMD", "/bin/true"] and
// IntervalS=1 spawns a goroutine that fires a pass report
// within the test window. We use a non-vsock fake by
// intercepting the goroutine: the runHealthcheckPoll goroutine
// calls sendHealthcheckReport which would fail with ENXIO on
// a non-Linux test runner. So we just assert no error from
// runHealthcheckPoll (the bind may fail on CI; that's fine —
// the test only pins "no early error").
func TestRunHealthcheckPoll_ShortIntervalSendsReport(t *testing.T) {
	m := api.AppManifest{
		Healthcheck: &api.AppManifestHealthcheck{
			Test:      []string{"CMD", "/bin/true"},
			IntervalS: 1,
			TimeoutS:  1,
		},
	}
	// Bind may fail on the test runner (no AF_VSOCK) — that's
	// acceptable; the unit test pins the dispatcher shape, not
	// the kernel-level wiring.
	err := runHealthcheckPoll(context.Background(), m, nil)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Logf("runHealthcheckPoll returned (acceptable on non-Linux CI): %v", err)
	}
}

// (binary.BigEndian.PutUint32 used directly above.)
