package imaged

// adr: 435. These executable fixtures exercise the default scanner dispatch,
// filesystem handoff and process boundary. They do not prove real Grype,
// native OverlayFS composition or Firecracker acceptance.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/scanview"
)

func viewScannerFixture(t *testing.T, body string) (string, string) {
	t.Helper()
	root := t.TempDir()
	bin, record := filepath.Join(root, "scanner"), filepath.Join(root, "view-path")
	script := "#!/bin/sh\nset -eu\nview=${1#dir:}\n[ \"$1\" = \"dir:$view\" ]\n[ \"$2\" = '-o' ]\n[ \"$3\" = 'json' ]\nprintf '%s' \"$view\" > " + viewShellQuote(record) + "\n"
	script += body + "\nprintf '%s\\n' " + viewShellQuote(grypeEmptyMatchesJSON) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

func viewShellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func scanViewSourceFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	for _, dir := range []string{"", "usr/lib", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range map[string]string{"file": "one", "usr/lib/module": "guest library"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"bin/module": "/usr/lib/module", "broken": "absent-original"} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func assertScanViewRemoved(t *testing.T, source, record string, expected scanview.Tree) {
	t.Helper()
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	dir := string(raw)
	if filepath.Dir(dir) != filepath.Dir(source) || !strings.HasPrefix(filepath.Base(dir), "imaged-scan-view-") {
		t.Fatal("scanner did not receive a private copied view")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("scanner staging remained after completion", err)
	}
	after, err := scanview.Snapshot(t.Context(), source)
	if err != nil || after != expected {
		t.Fatal("scanner changed the input filesystem", err)
	}
}

func TestDefaultGrypeDispatchUsesConfinedReadableView(t *testing.T) {
	source := scanViewSourceFixture(t)
	host := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(host, []byte("host-only bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(host, filepath.Join(source, "host-secret")); err != nil {
		t.Fatal(err)
	}
	expected, err := scanview.Snapshot(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	body := "[ \"$(cat \"$view/bin/module\")\" = 'guest library' ]\n[ -L \"$view/host-secret\" ]\n[ ! -e \"$view/host-secret\" ]\n[ ! -e \"$view/broken\" ]"
	bin, record := viewScannerFixture(t, body)
	result, err := runGrypeImpl(t.Context(), bin, source)
	if err != nil || result == nil || len(result.Vulnerabilities) != 0 || result.ScannerName != "grype" {
		t.Fatal("default process dispatch did not return a complete clean fixture result", err)
	}
	assertScanViewRemoved(t, source, record, expected)
}

func TestDefaultGrypeDispatchRefusesChangedView(t *testing.T) {
	cases := map[string]string{
		"same-size bytes": "printf 'two' > \"$view/file\"",
		"raw broken link": "rm \"$view/broken\"\nln -s 'absent-replaced' \"$view/broken\"",
		"added file":      "printf 'unexpected' > \"$view/added\"",
		"removed file":    "rm \"$view/file\"",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			source := scanViewSourceFixture(t)
			expected, err := scanview.Snapshot(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			bin, record := viewScannerFixture(t, body)
			result, err := runGrypeImpl(t.Context(), bin, source)
			if result != nil || !errors.Is(err, scanview.ErrChanged) {
				t.Fatal("changed view acquired a clean scan result", err)
			}
			assertScanViewRemoved(t, source, record, expected)
		})
	}
}

func TestDefaultGrypeDispatchCleansUpFailedScanner(t *testing.T) {
	for _, body := range []string{"printf 'PRIVATE-SCAN-DATA' >&2\nexit 7", "printf 'malformed'\nexit 0"} {
		source := scanViewSourceFixture(t)
		expected, err := scanview.Snapshot(t.Context(), source)
		if err != nil {
			t.Fatal(err)
		}
		bin, record := viewScannerFixture(t, body)
		result, err := runGrypeImpl(t.Context(), bin, source)
		if result != nil || err == nil || strings.Contains(err.Error(), "PRIVATE-SCAN-DATA") {
			t.Fatal("failed scanner was accepted or echoed private diagnostics", err)
		}
		assertScanViewRemoved(t, source, record, expected)
	}
}

func TestDefaultGrypeDispatchCancellationRemovesView(t *testing.T) {
	source := scanViewSourceFixture(t)
	expected, err := scanview.Snapshot(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	bin, record := viewScannerFixture(t, "exec /bin/sleep 30")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		result, err := runGrypeImpl(ctx, bin, source)
		if result != nil {
			err = errors.New("canceled scanner returned a result")
		}
		done <- err
	}()
	awaitScanViewProcess(t, record)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("scanner cancellation lost its cause", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled scanner did not finish")
	}
	assertScanViewRemoved(t, source, record, expected)
}

func awaitScanViewProcess(t *testing.T, record string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		if raw, err := os.ReadFile(record); err == nil && len(raw) > 0 {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("scanner never reached its filesystem handoff")
		}
	}
}
