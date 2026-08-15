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
	"path/filepath"
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

// TestDaemon_SIGHUPReloadsLevel re-execs the test binary as a real
// daemon child, sends it SIGHUP, and asserts the shared leveler
// mutated — observable from the parent via the child's stderr
// (issue #550 acceptance criteria).
//
// Flake history (issue #550): a StderrPipe + bufio.Scanner version was
// environmentally flaky — the kernel pipe buffer was not reliably
// drained to the parent's scanner before the deadline. This version
// hands the child an *os.File (temp file) as stderr: exec wires the fd
// straight into the child, every slog emit is a direct write(2), and
// the parent polls by re-reading the file. No pipe is involved.
//
// Level choreography: the package-level LevelVar initializes from
// FAAS_LOG_LEVEL at package init (daemon.go), so the parent strips the
// variable from the child's env (child boots at INFO) and the child
// sets it to "debug" after init, before Daemon(). The SIGHUP re-read
// then flips INFO → DEBUG. The daemon body emits log.Debug probes on a
// ticker: suppressed before the flip, visible after — direct evidence
// the handler's leveler mutated, not just that the reload goroutine
// logged.
func TestDaemon_SIGHUPReloadsLevel(t *testing.T) {
	if os.Getenv("WIRE_SIGHUP_CHILD") == "1" {
		// Child: package init already ran with FAAS_LOG_LEVEL unset,
		// so the leveler is at INFO. Arrange for the SIGHUP re-read
		// to see "debug".
		os.Setenv(wire.EnvLogLevel, "debug")
		wire.Daemon("testd", func(ctx context.Context, log *slog.Logger) error {
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(25 * time.Millisecond):
					// Suppressed at INFO; visible only after the
					// SIGHUP-driven flip to DEBUG.
					log.Debug("debug probe")
				}
			}
		})
		return
	}

	stderrPath := filepath.Join(t.TempDir(), "child-stderr.log")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("create child stderr file: %v", err)
	}
	defer stderrFile.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemon_SIGHUPReloadsLevel$")
	// Strip FAAS_LOG_LEVEL so the child's package init boots the
	// leveler at INFO regardless of the CI environment.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, wire.EnvLogLevel+"=") {
			env = append(env, kv)
		}
	}
	// Sentinel last: exec.Cmd dedups by keeping the final occurrence of
	// a key, so appending here beats any ambient WIRE_SIGHUP_CHILD in
	// the developer's shell (a stale "=0" would send the child down the
	// parent branch and fork-bomb the test).
	env = append(env, "WIRE_SIGHUP_CHILD=1")
	cmd.Env = env
	// Both streams go to the same file: slog writes to stderr, but the
	// child's go-test framework reports failures on stdout — without
	// this a child framework failure surfaces as a bare "exit status 1".
	cmd.Stdout = stderrFile
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort: no-ops with "process already finished" after a
		// clean Wait; prevents an orphan on assertion failure.
		_ = cmd.Process.Kill()
	})

	// waitFor polls the child's stderr file until substr appears,
	// returning the full contents; fails the test on timeout.
	waitFor := func(substr string, timeout time.Duration) string {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			b, readErr := os.ReadFile(stderrPath)
			if readErr == nil && strings.Contains(string(b), substr) {
				return string(b)
			}
			time.Sleep(25 * time.Millisecond)
		}
		b, _ := os.ReadFile(stderrPath)
		t.Fatalf("timed out after %v waiting for %q in child stderr; got:\n%s", timeout, substr, b)
		return "" // unreachable
	}

	// Daemon() calls signal.Notify for SIGHUP before it logs
	// "starting" (daemon.go), so once "starting" is visible the HUP
	// handler is installed and there is no delivery race.
	waitFor(`"msg":"starting"`, 10*time.Second)

	// Snapshot stderr strictly before the signal: everything in it was
	// written while the leveler was still at INFO, so it is a
	// race-free witness for the pre-flip assertion below. Slicing the
	// post-flip output at the "log level changed" marker would NOT be:
	// watchLogLevelReload calls logLevel.Set(next) before it emits that
	// marker (daemon.go), so a probe tick landing in that window is
	// legitimately written ahead of the marker.
	preFlip, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("snapshot child stderr before SIGHUP: %v", err)
	}

	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}

	out := waitFor(`"msg":"log level changed"`, 10*time.Second)
	if !strings.Contains(out, `"prev":"INFO"`) || !strings.Contains(out, `"next":"DEBUG"`) {
		t.Errorf(`"log level changed" record lacks "prev":"INFO"/"next":"DEBUG"; child stderr:`+"\n%s", out)
	}
	// Before the flip, INFO suppressed the probes.
	if strings.Contains(string(preFlip), "debug probe") {
		t.Errorf("debug probe leaked before SIGHUP (leveler was not at INFO); child stderr:\n%s", preFlip)
	}
	// After the flip, the mutated leveler must let a probe through.
	waitFor(`"msg":"debug probe"`, 10*time.Second)

	// Clean shutdown: SIGTERM cancels Daemon's ctx, fn returns nil,
	// the child exits 0.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			b, _ := os.ReadFile(stderrPath)
			t.Fatalf("child exited non-zero: %v; stderr:\n%s", err, b)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child did not exit within 10s of SIGTERM")
	}
}
