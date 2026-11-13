package main_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const reconnectLogPath = "/v1/jobs/batch/runs/11111111-1111-4111-8111-111111111111/tasks/0/logs/stream"

func reconnectLog(instance string, seq int64, stream, line string) string {
	return fmt.Sprintf("event: log\ndata: {\"instance\":%q,\"seq\":%d,\"stream\":%q,\"line\":%q}\n\n", instance, seq, stream, line)
}

func reconnectEnd(reason string) string {
	return fmt.Sprintf("event: end\ndata: {\"reason\":%q}\n\n", reason)
}

func TestJobLogReconnectReplayAndAttemptBoundaries(t *testing.T) {
	a := reconnectLog("vm-a", 1, "stdout", "one")
	b := reconnectLog("vm-a", 2, "stderr", "two")
	c := reconnectLog("vm-a", 3, "stdout", "three")
	d := reconnectLog("vm-a", 4, "stdout", "four")
	gap := "event: gap\ndata: {\"reason\":\"seq_below_retained\"}\n\n"
	finished := reconnectEnd(api.JobTaskLogsEndFinished)
	for _, tc := range []struct {
		name           string
		responses      []string
		stdout, stderr string
		lines, calls   int
		complete, gaps bool
		attemptChanges int
		unverified     bool
		wantErr        bool
	}{
		{name: "replay keeps streams and identical text", responses: []string{a + b + c, a + b + c + d + reconnectLog("vm-a", 5, "stdout", "four") + finished}, stdout: "one\nthree\nfour\nfour\n", stderr: "two\n", lines: 5, calls: 2, complete: true},
		{name: "duplicates within a stream", responses: []string{a + a + b + finished}, stdout: "one\n", stderr: "two\n", lines: 2, calls: 1, complete: true},
		{name: "expired already printed prefix", responses: []string{a + b + c, gap + c + d + finished}, stdout: "one\nthree\nfour\n", stderr: "two\n", lines: 4, calls: 2, complete: true},
		{name: "gateway reconnect within replay", responses: []string{a + b + c, b + gap + c + d + finished}, stdout: "one\nthree\nfour\n", stderr: "two\n", lines: 4, calls: 2, complete: true},
		{name: "gap covers only printed lines", responses: []string{a + b + gap + c + finished}, stdout: "one\nthree\n", stderr: "two\n", lines: 3, calls: 1, complete: true},
		{name: "unresolved live gap", responses: []string{a + gap + finished}, stdout: "one\n", lines: 1, calls: 1, gaps: true},
		{name: "retained window lost new output", responses: []string{a, gap + c + finished}, stdout: "one\nthree\n", lines: 2, calls: 2, gaps: true},
		{name: "unconfirmed replay gap", responses: []string{a, gap + finished}, stdout: "one\n", lines: 1, calls: 2, gaps: true, unverified: true},
		{name: "empty resumed attempt", responses: []string{a, finished}, stdout: "one\n", lines: 1, calls: 2, unverified: true},
		{name: "unidentified resumed output is not duplicated", responses: []string{a, reconnectLog("", 1, "stdout", "one") + finished}, stdout: "one\n", lines: 1, calls: 2, wantErr: true},
		{name: "attempt changed while disconnected", responses: []string{a + b + c, reconnectLog("vm-b", 1, "stdout", "new attempt") + finished}, stdout: "one\nthree\nnew attempt\n", stderr: "two\n", lines: 4, calls: 2, attemptChanges: 1},
		{name: "normal attempt transition", responses: []string{a + reconnectLog("vm-b", 1, "stdout", "new attempt") + finished}, stdout: "one\nnew attempt\n", lines: 2, calls: 1, complete: true},
		{name: "missing instance cannot be replayed", responses: []string{reconnectLog("", 1, "stdout", "legacy")}, stdout: "legacy\n", lines: 1, calls: 1, wantErr: true},
		{name: "missing sequence cannot be replayed", responses: []string{reconnectLog("vm-a", 0, "stdout", "legacy")}, stdout: "legacy\n", lines: 1, calls: 1, wantErr: true},
		{name: "legacy complete stream", responses: []string{reconnectLog("", 0, "stdout", "legacy") + finished}, stdout: "legacy\n", lines: 1, calls: 1, complete: true},
		{name: "malformed log remains incomplete", responses: []string{"event: log\ndata: {\n\n" + a + finished}, stdout: "one\n", lines: 1, calls: 1, gaps: true},
		{name: "malformed end is final", responses: []string{a + "event: end\ndata: {\n\n"}, stdout: "one\n", lines: 1, calls: 1, wantErr: true},
		{name: "node reconnect", responses: []string{reconnectEnd(api.JobTaskLogsEndUnreachable), a + finished}, stdout: "one\n", lines: 1, calls: 2, complete: true},
		{name: "backstop reconnect", responses: []string{reconnectEnd(api.JobTaskLogsEndTimeout), a + finished}, stdout: "one\n", lines: 1, calls: 2, complete: true},
		{name: "stored output stays final", responses: []string{reconnectEnd(api.JobTaskLogsEndStoredOutput)}, calls: 1},
		{name: "not found stays final", responses: []string{reconnectEnd(api.JobTaskLogsEndNotFound)}, calls: 1},
		{name: "unknown end stays final", responses: []string{reconnectEnd("unknown")}, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != reconnectLogPath || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("reconnect changed the read target, cursor safety, or authentication")
				}
				n := int(calls.Add(1)) - 1
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.responses[min(n, len(tc.responses)-1)])
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var stdout, stderr bytes.Buffer
			live, err := main.FollowJobTaskLogsWithRetry(ctx, api.NewClient(server.URL, "test-token"), "batch", "11111111-1111-4111-8111-111111111111", 0, &stdout, &stderr, []time.Duration{0, 0, 0})
			if (err != nil) != tc.wantErr || live.Complete() != tc.complete || (live.Gaps > 0) != tc.gaps || live.AttemptChanges != tc.attemptChanges || live.UnverifiedResume != tc.unverified || live.Lines != tc.lines || int(calls.Load()) != tc.calls || stdout.String() != tc.stdout || stderr.String() != tc.stderr {
				t.Fatalf("lines=%d complete=%v gaps=%d changed=%d calls=%d err=%v stdout=%q stderr=%q", live.Lines, live.Complete(), live.Gaps, live.AttemptChanges, calls.Load(), err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestJobLogReconnectBudgetIncludesProgress(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "100000") // A truncated body preserves its underlying read error.
		_, _ = io.WriteString(w, reconnectLog("vm-a", int64(n), "stdout", fmt.Sprint(n)))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	live, err := main.FollowJobTaskLogsWithRetry(ctx, api.NewClient(server.URL, "test-token"), "batch", "run", 0, &out, io.Discard, []time.Duration{0, 0, 0})
	if !errors.Is(err, io.ErrUnexpectedEOF) || calls.Load() != 4 || live.Lines != 4 || out.String() != "1\n2\n3\n4\n" {
		t.Fatalf("unbounded or repeated output: calls=%d lines=%d err=%v output=%q", calls.Load(), live.Lines, err, out.String())
	}
}

func TestJobLogReconnectHTTPRefusalsAreFinal(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"status":%d,"code":"refused","title":"Refused"}`, status)
			}))
			defer server.Close()
			live, err := main.FollowJobTaskLogsWithRetry(context.Background(), api.NewClient(server.URL, "test-token"), "batch", "run", 0, io.Discard, io.Discard, []time.Duration{0, 0, 0})
			var problem *api.APIError
			if !errors.As(err, &problem) || problem.Problem.Status != status || calls.Load() != 1 || live.Lines != 0 {
				t.Fatalf("HTTP refusal retried or changed: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestJobLogReconnectHTTP2Reset(t *testing.T) {
	var calls atomic.Int32
	line := reconnectLog("vm-a", 1, "stdout", "one")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Error("fixture did not use HTTP/2")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, line)
		if calls.Add(1) == 1 {
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler) // RST_STREAM after a complete log frame.
		}
		_, _ = io.WriteString(w, reconnectLog("vm-a", 2, "stderr", "two")+reconnectEnd(api.JobTaskLogsEndFinished))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client := api.NewClient(server.URL, "test-token")
	client.HTTPClient().Transport = server.Client().Transport
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out, stderr bytes.Buffer
	live, err := main.FollowJobTaskLogsWithRetry(ctx, client, "batch", "run", 0, &out, &stderr, []time.Duration{0, 0, 0})
	if err != nil || !live.Complete() || calls.Load() != 2 || out.String() != "one\n" || stderr.String() != "two\n" {
		t.Fatalf("HTTP/2 reset lost or repeated output: calls=%d complete=%v err=%v out=%q stderr=%q", calls.Load(), live.Complete(), err, out.String(), stderr.String())
	}
}

func TestJobLogReconnectOversizeFrameIsFinal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, reconnectLog("vm-a", 1, "stdout", "one"))
		_, _ = io.WriteString(w, "data: "+strings.Repeat("x", api.SSEMaxLineBytes+1))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	live, err := main.FollowJobTaskLogsWithRetry(ctx, api.NewClient(server.URL, "test-token"), "batch", "run", 0, &out, io.Discard, []time.Duration{0, 0, 0})
	if !errors.Is(err, bufio.ErrTooLong) || live.Lines != 1 || calls.Load() != 1 || out.String() != "one\n" {
		t.Fatalf("oversized frame retried: calls=%d lines=%d err=%v", calls.Load(), live.Lines, err)
	}
}

func TestJobLogReconnectCancellation(t *testing.T) {
	for _, connected := range []bool{false, true} {
		t.Run(fmt.Sprintf("connected=%v", connected), func(t *testing.T) {
			opened := make(chan struct{})
			closed := make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) != 1 {
					t.Error("canceled follow reconnected")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, reconnectLog("vm-a", 1, "stdout", "one"))
				w.(http.Flusher).Flush()
				close(opened)
				if connected {
					<-r.Context().Done()
				}
				close(closed)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := main.FollowJobTaskLogsWithRetry(ctx, api.NewClient(server.URL, "test-token"), "batch", "run", 0, io.Discard, io.Discard, []time.Duration{time.Hour})
				done <- err
			}()
			select {
			case <-opened:
			case <-time.After(3 * time.Second):
				t.Fatal("stream did not open")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel returned %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancel did not stop the follow promptly")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("cancel left the stream open")
			}
		})
	}
}

type reconnectFailWriter struct {
	calls int
	short bool
}

func (w *reconnectFailWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.short {
		return len(p) / 2, nil
	}
	return 0, io.ErrClosedPipe
}

func TestJobLogReconnectOutputFailureClosesBufferedDecoder(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(fmt.Sprintf("short=%v", short), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				for i := 1; i <= 100; i++ {
					_, _ = io.WriteString(w, reconnectLog("vm-a", int64(i), "stdout", "line"))
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			out := &reconnectFailWriter{short: short}
			live, err := main.FollowJobTaskLogsWithRetry(ctx, api.NewClient(server.URL, "test-token"), "batch", "run", 0, out, io.Discard, []time.Duration{0, 0, 0})
			wantErr := io.ErrClosedPipe
			if short {
				wantErr = io.ErrShortWrite
			}
			if !errors.Is(err, wantErr) || live.Lines != 0 || out.calls != 1 || calls.Load() != 1 {
				t.Fatalf("output failure replayed: lines=%d writes=%d calls=%d err=%v", live.Lines, out.calls, calls.Load(), err)
			}
		})
	}
}

// Cancellation after the body closed must also interrupt the backoff itself.
func TestJobLogReconnectCanceledDuringBackoff(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := main.FollowJobTaskLogsWithRetry(ctx, api.NewClient(server.URL, "test-token"), "batch", "run", 0, io.Discard, io.Discard, []time.Duration{time.Hour})
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("backoff ignored deadline: calls=%d err=%v", calls.Load(), err)
	}
}
