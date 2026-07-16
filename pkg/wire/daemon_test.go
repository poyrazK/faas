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
