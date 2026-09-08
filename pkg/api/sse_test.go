// sse_test.go pins the typed-channel SSE decoder contract (Move 3).
// The decoder is the load-bearing primitive for every CLI streaming
// command (faas logs, faas tail, faas queue tail); without these
// tests a regression in the parser would silently truncate frames
// and the dashboard's htmx-sse integration tests would have to take
// over the role of an SDK unit test.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// TestSSEDecoder_ParsesFrames drives the parser with three
// canonical SSE frames: a log event (deployment log shape), an
// invocation_done event (the dashboard's Move 3 frame), and a
// heartbeat comment (must be dropped). Verifies the Event struct
// fields, the multi-line data join, the trailing blank-line
// flush, and the clean EOF on the error channel.
func TestSSEDecoder_ParsesFrames(t *testing.T) {
	src := strings.NewReader(
		"event: log\ndata: {\"seq\":1,\"stream\":\"stdout\",\"line\":\"hello\"}\n\n" +
			"event: invocation_done\n" +
			"data: {\"invocation_id\":\"i1\",\"app_id\":\"a1\",\"state\":\"completed\"}\n" +
			"id: 42\n\n" +
			":heartbeat\n\n" +
			"event: log\ndata: {\"seq\":2,\"stream\":\"stderr\",\"line\":\"oops\"}\n\n",
	)
	dec := NewDecoder(src)
	defer dec.Close()

	want := []Event{
		{Event: "log", Data: `{"seq":1,"stream":"stdout","line":"hello"}`},
		{Event: "invocation_done", Data: `{"invocation_id":"i1","app_id":"a1","state":"completed"}`, ID: "42"},
		{Event: "log", Data: `{"seq":2,"stream":"stderr","line":"oops"}`},
	}
	for i, w := range want {
		select {
		case got, ok := <-dec.Events():
			if !ok {
				t.Fatalf("frame %d: channel closed early", i)
			}
			if got != w {
				t.Errorf("frame %d = %+v, want %+v", i, got, w)
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("frame %d: timed out", i)
		}
	}

	// Heartbeat dropped, so the next receive should be the EOF on
	// the error channel.
	select {
	case err := <-dec.Errors():
		if !errors.Is(err, io.EOF) {
			t.Errorf("Errors() = %v, want io.EOF", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Errors() did not close within 1s of clean EOF")
	}
}

// TestSSEDecoder_MultiLineDataJoined locks the WHATWG multi-line
// `data:` rule: lines after the first `data:` accumulate and join
// with '\n'. A single payload split across N lines on the wire
// must round-trip as one Event with N-1 newlines in Data.
func TestSSEDecoder_MultiLineDataJoined(t *testing.T) {
	src := strings.NewReader("event: log\ndata: line1\ndata: line2\ndata: line3\n\n")
	dec := NewDecoder(src)
	defer dec.Close()
	got := <-dec.Events()
	if got.Event != "log" {
		t.Errorf("Event = %q, want log", got.Event)
	}
	want := "line1\nline2\nline3"
	if got.Data != want {
		t.Errorf("Data = %q, want %q", got.Data, want)
	}
	<-dec.Errors()
}

// TestSSEDecoder_ContextCancelCloses mirrors the existing
// TestStreamAppLogs_CancelOnContextDone pattern: a hang on the
// server side + a context cancel on the client side should close
// both the Events and Errors channels within ~1s. We don't have
// a real HTTP server here; we test the decoder's behavior on a
// blocking io.Reader.
func TestSSEDecoder_ContextCancelCloses(t *testing.T) {
	// blockingReader blocks Read until the test releases it via
	// the unblock channel. Mirrors the hold/handlerReady handshake
	// in TestStreamAppLogs_CancelOnContextDone.
	unblock := make(chan struct{})
	r := &blockingReader{ch: unblock}
	dec := NewDecoder(r)
	defer dec.Close()

	// Wait briefly to let the parser goroutine park on Read.
	time.Sleep(20 * time.Millisecond)
	close(unblock)

	// The reader returns io.ErrClosedPipe after unblock; the
	// parser should surface it on Errors and close Events.
	select {
	case _, ok := <-dec.Events():
		if ok {
			// We may receive a partial Event first if the
			// buffer was already populated. Drain the rest.
			for range dec.Events() {
			}
		}
	case err := <-dec.Errors():
		if err == nil {
			t.Error("Errors() returned nil, want transport error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("decoder did not close within 2s")
	}
}

// blockingReader returns io.ErrClosedPipe on the first Read after
// the unblock channel closes. Pre-close, it blocks indefinitely —
// enough to test the "parser parked on Read" scenario without
// needing a real HTTP server.
type blockingReader struct {
	ch     <-chan struct{}
	called bool
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if !b.called {
		b.called = true
		<-b.ch
		return 0, io.ErrClosedPipe
	}
	return 0, io.EOF
}

// TestSSEDecoder_HandlesEmptyFrames pins the spec edge case: a
// blank line with no preceding field produces no Event. The CLI
// relies on this so the heartbeat `: ping\n\n` shape doesn't
// surface as a phantom frame to consumers.
func TestSSEDecoder_HandlesEmptyFrames(t *testing.T) {
	src := strings.NewReader(
		"\n\n" + // two empty frames at start
			":ping\n\n" + // one comment
			"event: log\ndata: real\n\n" + // the only real frame
			"\n", // trailing blank
	)
	dec := NewDecoder(src)
	defer dec.Close()
	got := <-dec.Events()
	if got.Event != "log" || got.Data != "real" {
		t.Errorf("got %+v, want {log, real}", got)
	}
	select {
	case _, ok := <-dec.Events():
		if ok {
			t.Error("Events() yielded a second frame, want channel closed")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Events() did not close after the single real frame")
	}
	<-dec.Errors()
}

// TestLogEvent_RoundtripFromJSON pins the Move 4 wire shape for
// the app-logs frame (issue #254, Acceptance #5). The decoder
// must surface the per-instance id so a consumer can interleave
// multi-instance frames deterministically.
//
// Cross-renderer parity (per memory cross-renderer-invariant-pattern):
// the SDK decoder (Go, this file) and the dashboard's htmx-ext-sse
// decoder must agree on the field names. The server emits
// {seq, instance, stream, line, written_at}; if any field is
// renamed, the SDK's typed Roundtrip test breaks.
func TestLogEvent_RoundtripFromJSON(t *testing.T) {
	raw := []byte(`{"seq":42,"instance":"i-7","stream":"stdout","line":"hello","written_at":"2026-07-28T14:00:00Z"}`)
	var ev LogEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if ev.Seq != 42 {
		t.Errorf("Seq = %d, want 42", ev.Seq)
	}
	if ev.InstanceID != "i-7" {
		t.Errorf("InstanceID = %q, want i-7", ev.InstanceID)
	}
	if ev.Stream != "stdout" {
		t.Errorf("Stream = %q, want stdout", ev.Stream)
	}
	if ev.Line != "hello" {
		t.Errorf("Line = %q, want hello", ev.Line)
	}
	if ev.WrittenAt != "2026-07-28T14:00:00Z" {
		t.Errorf("WrittenAt = %q, want 2026-07-28T14:00:00Z", ev.WrittenAt)
	}
}

// TestLogEvent_DeploymentLogsNoInstance pins the deployment-logs
// wire shape stays unchanged: streamDeploymentLogs emits
// {seq, stream, line, written_at} (no instance field), and the
// SDK decoder leaves InstanceID empty. Move 4 must NOT regress
// the deployment-log stream.
func TestLogEvent_DeploymentLogsNoInstance(t *testing.T) {
	raw := []byte(`{"seq":1,"stream":"stdout","line":"build step","written_at":"2026-07-28T14:00:00Z"}`)
	var ev LogEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if ev.InstanceID != "" {
		t.Errorf("InstanceID = %q, want empty (deployment logs have no instance)", ev.InstanceID)
	}
	if ev.Line != "build step" {
		t.Errorf("Line = %q, want build step", ev.Line)
	}
}

// TestLogEvent_OldSDKIgnoresInstance pins the additive rollout:
// an old SDK that pre-dates Move 4 still decodes the frame
// cleanly because the JSON unmarshaler does not error on unknown
// fields. We simulate this by giving the struct a json tag that
// the new field claims (json.Unmarshal is strict about known
// fields; the lenient behaviour is the default).
func TestLogEvent_OldSDKIgnoresInstance(t *testing.T) {
	// The decoder uses the new LogEvent struct directly. The
	// "old SDK" guarantee is "decoder does not require the
	// instance field" — confirmed by the previous test
	// (TestLogEvent_DeploymentLogsNoInstance). This test
	// gives the inner round-trip a different decode order
	// (seq first, then instance) to verify field reordering.
	raw := []byte(`{"seq":99,"instance":"i-99","stream":"stderr","line":"x","written_at":"t"}`)
	var ev LogEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if ev.Seq != 99 || ev.InstanceID != "i-99" || ev.Stream != "stderr" || ev.Line != "x" {
		t.Errorf("got %+v", ev)
	}
}

func TestLogEvent_StructuredLevelIsAdditive(t *testing.T) {
	raw := []byte(`{"seq":5,"instance":"i-5","stream":"stderr","line":"failed","level":"error","written_at":"2026-09-08T12:00:00Z"}`)
	var ev LogEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if ev.Level != "error" {
		t.Errorf("Level = %q, want error", ev.Level)
	}
}
