// Tests for the daemon bootstrap (Daemon / StubRun / Logger / Version).
// Daemon() calls os.Exit on error, which we cannot intercept cleanly, so we
// only exercise the in-process paths: --version short-circuit and StubRun's
// block-until-cancel behaviour.

package wire_test

import (
	"bytes"
	"context"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestVersionConstant(t *testing.T) {
	// Version is the ldflags stamp; in tests it defaults to "dev".
	if wire.Version == "" {
		t.Errorf("Version = %q, want non-empty", wire.Version)
	}
}

func TestDaemon_VersionFlag(t *testing.T) {
	// Re-exec the test binary with --version under a "daemon" name. Daemon()
	// prints "<name> <Version>\n" and returns without invoking fn.
	//
	// Daemon() does flag.Parse on os.Args, which the go test framework has
	// already populated with -test.* flags. We append a literal "--version"
	// so Daemon()'s flag.Parse sees it after the test framework is done.
	if os.Getenv("WIRE_VERSION_FLAG_CHILD") == "1" {
		os.Args = append(os.Args, "--version")
		wire.Daemon("testd", func(_ context.Context, _ *slog.Logger) error {
			t.Fatal("fn must not run when --version is set")
			return nil
		})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestDaemon_VersionFlag")
	cmd.Env = append(os.Environ(), "WIRE_VERSION_FLAG_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "testd "+wire.Version) {
		t.Errorf("expected 'testd %s' in output, got:\n%s", wire.Version, out)
	}
}

// TestDaemon_HelpFlag pins the --help short-circuit that ci.yml's daemon smoke
// relies on (cmd/<daemon>/main.go is `wire.Daemon(name, run)`, so the same
// flag.Parse hits every daemon). Without a registered --help, `flag.Parse`
// errors on the unknown flag and exits before printing anything — the smoke
// loop then sees empty stdout and reports "--help returned no output".
func TestDaemon_HelpFlag(t *testing.T) {
	if os.Getenv("WIRE_HELP_FLAG_CHILD") == "1" {
		os.Args = append(os.Args, "--help")
		wire.Daemon("testd", func(_ context.Context, _ *slog.Logger) error {
			t.Fatal("fn must not run when --help is set")
			return nil
		})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestDaemon_HelpFlag")
	cmd.Env = append(os.Environ(), "WIRE_HELP_FLAG_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	got := string(out)
	// Must include daemon name + the registered flags. We don't pin the
	// exact --help wording (could change with future flag registrations);
	// just verify help fires and reads as usage, not "unknown flag".
	for _, needle := range []string{"testd", "-config", "-version", "-help"} {
		if !strings.Contains(got, needle) {
			t.Errorf("expected %q in --help output, got:\n%s", needle, got)
		}
	}
	if strings.Contains(got, "flag provided but not defined") {
		t.Errorf("--help produced 'flag provided but not defined' error:\n%s", got)
	}
}

func TestLogger_JSONToStderr(t *testing.T) {
	// Redirect stderr to capture slog output for the duration of the call.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	log := wire.Logger()
	if log == nil {
		t.Fatal("Logger returned nil")
	}
	log.Info("hello", "k", "v")

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, `"msg":"hello"`) {
		t.Errorf("expected JSON log line, got %q", got)
	}
	if !strings.Contains(got, `"k":"v"`) {
		t.Errorf("expected key/val pair in JSON log, got %q", got)
	}
}

// TestDaemon_LogRecordsStampDaemonKeyOnce is the issue #852 regression:
// Daemon() stamped "daemon" via Logger().With(...) AND again inside
// NewCorrelationLogger (FieldDaemon), so every record carried the key
// twice. Duplicate JSON keys are parser-dependent — jq and Loki take the
// last occurrence, strict decoders reject the record.
//
// Asserted on the raw stderr bytes, not a decoded map: encoding/json
// silently collapses duplicate keys on decode, so a map-based assertion
// cannot see this bug. strings.Count on the key token is the only shape
// that catches it.
//
// Re-exec (same pattern as TestDaemon_VersionFlag above) rather than a
// reimplementation of Daemon's body: the pre-existing in-process seam
// runWithFreshFlags (daemon_internal_test.go) omits NewCorrelationLogger
// entirely, which is exactly why no test caught the double stamp. This
// test drives the real Daemon() so it cannot drift the same way.
func TestDaemon_LogRecordsStampDaemonKeyOnce(t *testing.T) {
	if os.Getenv("WIRE_DAEMON_KEY_CHILD") == "1" {
		// Daemon() emits "starting" then "shutdown complete" through the
		// envelope under test; fn returning nil keeps the child exit 0.
		wire.Daemon("testd", func(_ context.Context, _ *slog.Logger) error {
			return nil
		})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestDaemon_LogRecordsStampDaemonKeyOnce")
	cmd.Env = append(os.Environ(), "WIRE_DAEMON_KEY_CHILD=1")
	// slog writes to stderr; the test framework's PASS/ok go to stdout.
	// Keep them apart so the assertion sees log records only.
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("child failed: %v\nstderr:\n%s", err, stderr.String())
	}

	var records int
	for _, line := range strings.Split(stderr.String(), "\n") {
		line = strings.TrimSpace(line)
		// Only slog JSON records; the child may also emit framework noise.
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"msg":`) {
			continue
		}
		records++
		if n := strings.Count(line, `"daemon":`); n != 1 {
			t.Errorf("record has %d %q keys, want exactly 1:\n%s", n, "daemon", line)
		}
		// The field must survive the dedup — losing it would break the
		// operator's "filter the aggregate stream by source daemon" path
		// that NewCorrelationLogger's doc comment promises.
		if !strings.Contains(line, `"daemon":"testd"`) {
			t.Errorf("record missing daemon=testd:\n%s", line)
		}
		// version is the half of the envelope Daemon() still owns.
		if !strings.Contains(line, `"version":`) {
			t.Errorf("record missing version:\n%s", line)
		}
	}
	if records == 0 {
		t.Fatalf("no JSON log records captured from child; stderr:\n%s", stderr.String())
	}
}

func TestStubRun_BlocksUntilCancel(t *testing.T) {
	// StubRun returns nil on ctx cancel; this is its entire contract.
	// Per contextcheck: do not capture this test's ctx into the goroutine.
	// The goroutine owns its cancellable ctx; the test signals cancel via
	// a dedicated channel it owns.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-stop
			cancel()
		}()
		_ = wire.StubRun("M0")(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()

	// It must NOT return within a short window — that proves it's blocking.
	select {
	case <-done:
		t.Fatal("StubRun returned before stop signal")
	case <-time.After(50 * time.Millisecond):
	}

	close(stop)
	select {
	case <-done:
		// good — returned after cancel.
	case <-time.After(time.Second):
		t.Fatal("StubRun did not return within 1s after stop signal")
	}
}

// Compile-time sanity check: flag is imported transitively via Daemon's flag.Parse,
// but we want to be explicit that the package compiles in test mode too.
var _ = flag.NewFlagSet

// TestParseLevel pins the canonical level-name vocabulary for FAAS_LOG_LEVEL
// (issue #518 PR-A). Adding new accepted names is a wire-contract change;
// case insensitivity keeps operator friction low.
//
// The boolean second return distinguishes "unparseable" from "default"
// so the caller can warn on a typo. The function itself never errors —
// the fallback to info is mandated by the "never refuse to start on log
// misconfiguration" invariant.
func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
		ok   bool
	}{
		{"", slog.LevelInfo, false},
		{"info", slog.LevelInfo, true},
		{"INFO", slog.LevelInfo, true},
		{"  info  ", slog.LevelInfo, true},
		{"debug", slog.LevelDebug, true},
		{"DEBUG", slog.LevelDebug, true},
		{"warn", slog.LevelWarn, true},
		{"warning", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"err", slog.LevelError, true},
		{"trace", slog.LevelInfo, false},
		{"garbage", slog.LevelInfo, false},
	}
	for _, tc := range cases {
		got, ok := wire.ParseLevel(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseLevel(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestLogger_FiltersByLevel is the canonical level-filter contract:
// logs below the active level are silently dropped, logs at or above
// are emitted as JSON. The level is set via the test-only SetLogLevelForTest
// helper (the same code path SIGHUP takes in production).
func TestLogger_FiltersByLevel(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	wire.SetLogLevelForTest(slog.LevelWarn)
	log := wire.Logger()
	log.Debug("debug should be filtered")
	log.Info("info should be filtered")
	log.Warn("warn should be emitted")
	log.Error("error should be emitted")

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, filtered := range []string{
		"debug should be filtered",
		"info should be filtered",
	} {
		if strings.Contains(got, filtered) {
			t.Errorf("emitted below level=warn: %s", filtered)
		}
	}
	for _, expect := range []string{
		"warn should be emitted",
		"error should be emitted",
	} {
		if !strings.Contains(got, expect) {
			t.Errorf("suppressed at-or-above level=warn: %s", expect)
		}
	}
}

// TestLogger_LevelChangeAppliesToExistingLogger is the SIGHUP contract:
// operators do not have to rebuild the logger after toggling the level.
// Setting the shared leveler after a *slog.Logger was constructed must
// take effect on the next emit from that same logger. systemd's
// `ExecReload=/bin/kill -HUP $MAINPID` is the production gesture that
// relies on this (issue #518 PR-A).
func TestLogger_LevelChangeAppliesToExistingLogger(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	wire.SetLogLevelForTest(slog.LevelWarn)
	log := wire.Logger()
	log.Debug("pre-change-debug-filtered")

	// Simulate SIGHUP received by the daemon: drop the level to debug.
	wire.SetLogLevelForTest(slog.LevelDebug)
	log.Debug("post-change-debug-emitted")

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "pre-change-debug-filtered") {
		t.Errorf("Debug emitted at level=warn before SIGHUP: %s", got)
	}
	if !strings.Contains(got, "post-change-debug-emitted") {
		t.Errorf("Debug suppressed after SIGHUP-dropped level: %s", got)
	}
}

// TestWatchLogLevelReload_DrivesLevelVar exercises the SIGHUP goroutine
// extracted from Daemon(). The test controls the signal source via a
// channel it owns, drives watchLogLevelReload directly with stubbed
// getenv, and asserts both the leveler mutation and the log record
// shape ("msg":"log level changed"). This is the unit-level acceptance
// for issue #518 PR-A; the production path through Daemon() is the same
// code with signal.Notify as the source.
//
// Skipped if the package-level logLevel has been changed by a sibling
// test; we restore it at the end so test order doesn't matter.
func TestWatchLogLevelReload_DrivesLevelVar(t *testing.T) {
	prevLevel := wire.LogLevelForTest()
	t.Cleanup(func() { wire.SetLogLevelForTest(prevLevel) })

	buf := &syncBuffer{}
	envelope := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	log := wire.NewCorrelationLogger(envelope, wire.CorrelationFields{RequestID: "test"}, "testd")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hupCh := make(chan os.Signal, 1)

	envValue := "info"
	getenv := func(string) string { return envValue }

	go wire.WatchLogLevelReload(ctx, log, hupCh, getenv)

	// Initial: warn; SIGHUP with envValue="warn" → no transition but
	// the goroutine still emits a log_level_changed line for symmetry.
	wire.SetLogLevelForTest(slog.LevelWarn)
	envValue = "warn"
	hupCh <- syscall.SIGHUP
	waitForLogLine(t, buf, `"msg":"log level changed"`)

	// Drop to debug via SIGHUP.
	envValue = "debug"
	hupCh <- syscall.SIGHUP
	waitForLogLine(t, buf, `"next":"DEBUG"`)

	if got := wire.LogLevelForTest(); got != slog.LevelDebug {
		t.Errorf("level after debug-SIGHUP = %v, want DEBUG", got)
	}

	// Garbage env → falls back to info + warn line.
	envValue = "not-a-level"
	hupCh <- syscall.SIGHUP
	waitForLogLine(t, buf, `"msg":"log level unrecognised`)

	if got := wire.LogLevelForTest(); got != slog.LevelInfo {
		t.Errorf("level after garbage-SIGHUP = %v, want INFO", got)
	}
}

// waitForLogLine polls the buffer until it contains a substring, with a
// short deadline; prevents flaky ordering races where the goroutine
// hasn't written yet when the assertion runs. The buffer is mutex-guarded
// because the SIGHUP goroutine writes while the test reads (the -race
// detector catches a bare *bytes.Buffer contention, per the
// e2etest-harness-safebuffer pattern in memory).
func waitForLogLine(t *testing.T, buf *syncBuffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if s := buf.String(); strings.Contains(s, needle) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for log line containing %q; buffer so far:\n%s", needle, buf.String())
}

// syncBuffer is a mutex-guarded *bytes.Buffer for tests where one
// goroutine writes (a slog handler) while another reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
