package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

// fakeDispatcher records Wake + Invoke calls so the handler can run
// end-to-end (covers both legacy wake-only and Move 1 invoke paths).
type fakeDispatcher struct{ calls []string }

func (f *fakeDispatcher) Wake(_ context.Context, appID string) error {
	f.calls = append(f.calls, appID)
	return nil
}

// Invoke echoes the wake path; the integration test only verifies the
// wire reached the dispatcher, not the response shape.
func (f *fakeDispatcher) Invoke(_ context.Context, appID string, inv state.Invocation) (state.Invocation, error) {
	f.calls = append(f.calls, appID)
	inv.State = state.InvocationDispatching
	return inv, nil
}

// TestSynthHandlerSanitizesLogFields asserts that a synthesized request
// with embedded CR/LF (the CWE-117 injection vector) does NOT leak newlines
// into the structured log. The handler should sanitize before logging.
func TestSynthHandlerSanitizesLogFields(t *testing.T) {
	// Buffer-backed slog handler so we can assert on emitted JSON.
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	dir := t.TempDir()
	// macOS limits unix socket paths to 104 bytes; t.TempDir already
	// nests under /var/folders/... which exhausts that. Use /tmp with
	// the test name to stay under the cap, and unlink on cleanup.
	sock := "/tmp/" + strings.ReplaceAll(t.Name(), "/", "_") + ".sock"
	_ = os.Remove(sock)
	s := NewSynthServer(sock, &fakeDispatcher{}, log)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		_ = s.Stop(context.Background())
		_ = os.Remove(sock)
	}()
	_ = dir

	// Dial the unix socket directly — the test bypasses any HTTP client
	// that might rewrite payloads.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Craft a request whose app_id and path embed newlines + carriage
	// returns — the canonical log-injection vector. After the handler
	// runs, the captured log must not contain raw \n or \r within the
	// JSON value (slog's JSON encoder would itself escape them, but
	// the helper is meant to prevent forged lines, which means
	// stripping the control bytes entirely).
	payload, _ := json.Marshal(map[string]string{
		"app_id": "app\nINJECTED",
		"method": "POST",
		"path":   "/foo\rFAKE",
	})
	req := fmt.Sprintf("POST /v1/synthesize HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(payload), payload)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read the response so the server doesn't hang.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	respBuf := make([]byte, 4096)
	_, _ = conn.Read(respBuf)

	out := buf.String()
	if out == "" {
		t.Fatal("expected log output, got empty buffer")
	}

	// The structured log line is one JSON object — it must NOT contain
	// the raw injected newlines INSIDE the dispatched-record's JSON
	// value. Newlines BETWEEN records are fine (slog emits one record
	// per line by default). The harder, semantically important check
	// is that sanitizeLogField replaced the CR/LF with the middle-dot
	// placeholder before the value was even handed to slog.
	//
	// slog's JSON encoder would escape \n → \\n if we passed an
	// unsanitized value; finding that escape sequence means the helper
	// did NOT run on this field.
	if strings.Contains(out, `app\nINJECTED`) {
		t.Errorf("log line was not sanitized (slog escaped an unsanitized value): %s", out)
	}
	if strings.Contains(out, `/foo\rFAKE`) {
		t.Errorf("log line was not sanitized (slog escaped an unsanitized value): %s", out)
	}

	// Finally: the placeholder variant must appear (proves the helper ran).
	if !strings.Contains(out, "app·INJECTED") {
		t.Errorf("expected sanitized app_id 'app·INJECTED' in log; got: %s", out)
	}
	if !strings.Contains(out, "/foo·FAKE") {
		t.Errorf("expected sanitized path '/foo·FAKE' in log; got: %s", out)
	}
}

// guard against stale /tmp files in case the test runs on a sandbox that
// still mounts /tmp as world-writable.
func TestMain(m *testing.M) {
	_ = os.Setenv("TMPDIR", os.TempDir())
	os.Exit(m.Run())
}
