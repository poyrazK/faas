package instancebridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func controlListener(t *testing.T) (string, net.Listener) {
	t.Helper()
	// Short: t.TempDir() under a long test name passes macOS's 104-byte
	// unix socket path limit.
	dir, err := os.MkdirTemp("", "ibctl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "control.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	return path, ln
}

func TestCountersTrackRequestsAndSessions(t *testing.T) {
	now := time.Unix(1000, 0)
	c := &Counters{now: func() time.Time { return now }}
	if s := c.Snapshot(); s.Busy() || !s.LastEnd.IsZero() {
		t.Fatalf("fresh counters = %+v, want idle with no last end", s)
	}
	endReq := c.BeginRequest()
	endSession := c.BeginSession()
	if s := c.Snapshot(); s.Inflight != 1 || s.Sessions != 1 || !s.Busy() {
		t.Fatalf("counters = %+v, want 1 request and 1 session", s)
	}
	endReq()
	endReq() // a second call does not count again
	now = now.Add(time.Second)
	endSession()
	s := c.Snapshot()
	if s.Busy() || s.Inflight != 0 || s.Sessions != 0 || !s.LastEnd.Equal(now) {
		t.Fatalf("counters = %+v, want idle, last end %v", s, now)
	}
	var nilCounters *Counters
	nilCounters.BeginRequest()()
	nilCounters.BeginSession()()
}

func TestControlStatsRoundTrip(t *testing.T) {
	path, ln := controlListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Counters{}
	done := make(chan error, 1)
	go func() { done <- ServeControl(ctx, ln, "inst-1", c) }()

	client, err := DialControl(ctx, path, "inst-1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	end := c.BeginRequest()
	got, err := client.Stats(ctx)
	if err != nil || got.Inflight != 1 || got.Sessions != 0 || !got.LastEnd.IsZero() {
		t.Fatalf("stats = %+v, %v; want 1 in flight", got, err)
	}
	end()
	got, err = client.Stats(ctx)
	if err != nil || got.Busy() || got.LastEnd.IsZero() {
		t.Fatalf("stats = %+v, %v; want idle with a last end", got, err)
	}
	// Concurrent readers share the connection safely.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Stats(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("ServeControl = %v, want context.Canceled after shutdown", err)
	}
}

// A control socket that answers for another instance is never trusted.
func TestControlHelloMustNameTheInstance(t *testing.T) {
	path, ln := controlListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ServeControl(ctx, ln, "inst-other", &Counters{}) }()
	if _, err := DialControl(ctx, path, "inst-1"); err == nil || !strings.Contains(err.Error(), "inst-other") {
		t.Fatalf("err = %v, want a hello mismatch naming the served instance", err)
	}
}

func TestControlRefusesUnknownProtocolAndRequests(t *testing.T) {
	path, ln := controlListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ServeControl(ctx, ln, "inst-1", &Counters{}) }()
	for _, tc := range []struct{ send, want string }{
		{"FAAS-IBRIDGE-CTL/9\n", "ERR unsupported control protocol"},
		{ControlHello + "\nRESET\n", "ERR unknown request"},
		{strings.Repeat("x", maxControlLine+10) + "\n", ""},
	} {
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = fmt.Fprint(conn, tc.send)
		r := bufio.NewReader(conn)
		var last string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			last = strings.TrimSuffix(line, "\n")
		}
		_ = conn.Close()
		if last != tc.want {
			t.Fatalf("send %.30q: last answer %q, want %q then close", tc.send, last, tc.want)
		}
	}
}

func TestParseStatsRejectsMalformedAnswers(t *testing.T) {
	for _, line := range []string{"STATS 1 2", "STATS 1 2 3 4", "STATS -1 0 0", "STATS a 0 0", "STAT 1 2 3"} {
		if _, err := parseStats(line); err == nil {
			t.Fatalf("parseStats(%q) accepted a malformed answer", line)
		}
	}
	s, err := parseStats("STATS 2 1 0")
	if err != nil || s.Inflight != 2 || s.Sessions != 1 || !s.LastEnd.IsZero() {
		t.Fatalf("parseStats = %+v, %v", s, err)
	}
}
