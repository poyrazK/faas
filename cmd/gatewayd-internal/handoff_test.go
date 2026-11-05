package main

// adr: 1736

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
	"github.com/onebox-faas/faas/pkg/genslot"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
	"github.com/onebox-faas/faas/pkg/wire"
)

func startDrainedServer(t *testing.T, handler http.Handler) (drainedServer, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	d := wire.NewHTTPDrain(srv)
	go func() { _ = d.ServeErr(srv.Serve(d.Listener(l))) }()
	t.Cleanup(func() { _ = srv.Close() })
	return drainedServer{srv: srv, drain: d}, l.Addr().String()
}

// A request longer than the 25 s restart grace finishes on the old
// generation after a hand-off; its listener closes at once.
func TestDrainOldGenerationFinishesRequestsLongerThanTheRestartGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a request longer than the restart grace")
	}
	long := drain.DrainGrace + 2*time.Second
	started := make(chan struct{})
	server, addr := startDrainedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-time.After(long):
			_, _ = io.WriteString(w, "done")
		case <-r.Context().Done():
		}
	}))
	result := make(chan string, 1)
	go func() {
		resp, err := (&http.Client{Timeout: 2 * long}).Get("http://" + addr + "/long") //nolint:noctx // test client
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		result <- string(b)
	}()
	<-started
	done := make(chan error, 1)
	go func() {
		done <- drainOldGeneration(context.Background(), discardLogger(), []drainedServer{server}, nil, nil, runDeps{drain: drain.NewTracker()})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			break // the successor owns new connections
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Fatal("old generation still accepts after the hand-off")
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case got := <-result:
		if got != "done" {
			t.Fatalf("long request = %q, want it finished", got)
		}
	case <-time.After(2 * long):
		t.Fatal("long request never finished")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("old generation drain: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("old generation did not exit after its last request")
	}
}

// An SSE stream stays open on the old generation and ends cleanly when the
// unit stops it.
func TestDrainOldGenerationKeepsStreamsUntilStopped(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set(api.StreamingStatusHeader, string(api.StreamingStatusStreaming))
		// gatewayd-internal's authenticated long-lived marker (ADR-570).
		w.Header().Set("X-Faas-Traffic-Response-Session", "long-lived")
		_, _ = io.WriteString(w, "data: 1\n\n")
		http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	target, _ := url.Parse(upstream.URL)
	tracker := drain.NewTracker()
	proxy := gateway.NewInternalReverseProxy(gateway.NewTCPDialer(target.Host), target, discardLogger(), false).WithInFlightTracker(tracker)
	server, addr := startDrainedServer(t, proxy)

	resp, err := http.Get("http://" + addr + "/events") //nolint:noctx // test client
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "data: 1\n" {
		t.Fatalf("first event = %q, %v", line, err)
	}
	ended := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, reader); ended <- err }()

	stopping, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- drainOldGeneration(stopping, discardLogger(), []drainedServer{server}, nil, nil, runDeps{drain: tracker})
	}()
	select {
	case err := <-ended:
		t.Fatalf("SSE stream ended (%v) as the hand-off began; the old generation keeps it", err)
	case <-time.After(500 * time.Millisecond):
	}
	stop() // SIGTERM to the old generation
	select {
	case err := <-ended:
		if err != nil {
			t.Fatalf("SSE stream aborted (%v); want a clean end the client reconnects from", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SSE stream still open after the old generation was asked to stop")
	}
	select {
	case <-done:
	case <-time.After(drain.DrainGrace + 5*time.Second):
		t.Fatal("old generation's drain did not finish")
	}
}

// The old and new generations write separate egress ledgers and usage
// outboxes; once the old one exits, the new one adopts what it left.
func TestGenerationStoresAcrossAHandoff(t *testing.T) {
	egressRoot, usageRoot := t.TempDir(), t.TempDir()
	ledger := filepath.Join(egressRoot, "pending.json")
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	old := &generationStores{log: discardLogger()}
	oldSink := egresssink.NewEgressSinkWithClock(func() time.Time { return now })
	if _, err := old.openEgress(oldSink, ledger); err != nil {
		t.Fatal(err)
	}
	oldUsage, err := old.openUsage(usageRoot)
	if err != nil {
		t.Fatal(err)
	}
	next := &generationStores{log: discardLogger()}
	nextEgress, err := next.openEgress(egresssink.NewEgressSinkWithClock(func() time.Time { return now }), ledger)
	if err != nil {
		t.Fatal(err)
	}
	nextUsage, err := next.openUsage(usageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if old.egressSlot.Index() == next.egressSlot.Index() || old.usageSlot.Index() == next.usageSlot.Index() {
		t.Fatal("overlapping generations share a slot")
	}

	// The old generation finishes a long request after the hand-off.
	oldSink.RecordResponseBytes("inst-long", 4096)
	if err := oldUsage.Enqueue(usageoutbox.Event{EventID: "old-1", AccountID: "a", AppID: "app", WindowStart: now, RequestCount: 1}); err != nil {
		t.Fatal(err)
	}
	next.adopt() // nothing yet: the old generation still runs
	if got := nextUsage.Stats().PendingRecords; got != 0 {
		t.Fatalf("adopted %d usage events from a running generation", got)
	}
	old.markHandedOff()
	old.close() // flushes the egress sink; its slot is the root, so records stay

	next.adopt()
	if got := nextUsage.Stats().PendingRecords; got != 1 {
		t.Fatalf("adopted %d usage events, want 1", got)
	}
	if err := nextEgress.Flush(); err != nil {
		t.Fatal(err)
	}
	replay, err := egressLedgerRecords(filepath.Join(next.egressSlot.Dir(), "pending.json"))
	if err != nil || replay != 1 {
		t.Fatalf("successor ledger has %d records (%v), want the old generation's 1", replay, err)
	}

	// The successor exits cleanly with nobody in the root slot: its records
	// return there, where a release without slots looks.
	next.close()
	if n, err := egressLedgerRecords(ledger); err != nil || n != 1 {
		t.Fatalf("root ledger has %d records (%v), want 1", n, err)
	}
	home, err := usageoutbox.Open(usageRoot, usageoutbox.DefaultMaxBytes)
	if err != nil || home.Stats().PendingRecords != 1 {
		t.Fatalf("root outbox: %+v %v", home.Stats(), err)
	}
	if _, err := os.Stat(filepath.Join(usageRoot, ".generation-1")); !os.IsNotExist(err) {
		t.Fatalf("slot directory left behind: %v", err)
	}
	s, err := genslot.Acquire(usageRoot)
	if err != nil || s.Index() != 0 {
		t.Fatalf("root slot not free after both generations exited: %v %v", s, err)
	}
	_ = s.Release()
}

func egressLedgerRecords(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var file struct {
		Records []json.RawMessage `json:"records"`
	}
	err = json.Unmarshal(b, &file)
	return len(file.Records), err
}
