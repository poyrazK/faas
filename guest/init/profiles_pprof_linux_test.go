//go:build linux

package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

// fakePprof serves the net/http/pprof paths the scraper reads.
type fakePprof struct {
	mu      sync.Mutex
	hits    map[string]int
	seconds []string
	status  map[string]int
	heap    string
	// blockCPU holds the CPU request open until the client goes away.
	blockCPU   bool
	cpuStarted chan struct{}
	cpuGone    chan struct{}
}

func (f *fakePprof) start(t *testing.T) int {
	t.Helper()
	f.hits, f.cpuStarted, f.cpuGone = map[string]int{}, make(chan struct{}), make(chan struct{})
	if f.status == nil {
		f.status = map[string]int{}
	}
	if f.heap == "" {
		f.heap = "heap-profile"
	}
	reply := func(path, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.hits[path]++
			code := f.status[path]
			f.mu.Unlock()
			if code != 0 {
				http.Error(w, "profiling failed", code)
				return
			}
			_, _ = w.Write([]byte(body))
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /debug/pprof/{$}", reply("index", "<html>pprof</html>"))
	mux.HandleFunc("GET /debug/pprof/goroutine", reply("goroutine", "stacks-profile"))
	for _, kind := range []string{"block", "mutex"} {
		mux.HandleFunc("GET /debug/pprof/"+kind, func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.seconds = append(f.seconds, kind+"="+r.URL.Query().Get("seconds"))
			f.mu.Unlock()
			reply(kind, kind+"-delta")(w, r)
		})
	}
	mux.HandleFunc("GET /debug/pprof/heap", func(w http.ResponseWriter, r *http.Request) { reply("heap", f.heap)(w, r) })
	var started, gone sync.Once
	mux.HandleFunc("GET /debug/pprof/profile", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seconds = append(f.seconds, r.URL.Query().Get("seconds"))
		block := f.blockCPU
		f.mu.Unlock()
		if block {
			started.Do(func() { close(f.cpuStarted) })
			<-r.Context().Done()
			gone.Do(func() { close(f.cpuGone) })
			return
		}
		reply("profile", "cpu-profile")(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func (f *fakePprof) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func startPprofScraper(t *testing.T, b *profileBridge, port int) {
	t.Helper()
	s := newPprofScraper(b, port, 4242)
	s.poll, s.dormantPoll, s.probeEvery = 10*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if s.waitForEndpoint(ctx) {
			s.run(ctx)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (b *profileBridge) locked(f func() bool) func() bool {
	return func() bool { b.mu.Lock(); defer b.mu.Unlock(); return f() }
}

func TestPprofScraperCapturesEveryKindForTheProcess(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error {
		t.Error("dormant bridge forwarded a scraped capture profile")
		return nil
	})
	f := &fakePprof{}
	startPprofScraper(t, b, f.start(t))
	waitFor(t, "the scraper to register", b.locked(func() bool { return !b.pprofSeen.IsZero() }))
	if f.count("profile") != 0 || f.count("heap") != 0 {
		t.Fatal("a dormant scraper fetched profiles")
	}
	req := captureRequest("cpu", "heap", "stacks")
	result, ack := b.runCapture(req, func(d time.Duration) {
		if d == req.Duration() {
			waitFor(t, "three scraped profiles", b.locked(func() bool { return b.capture != nil && len(b.capture.profiles) == 3 }))
		}
	})
	if ack != profileproto.CaptureAckOK || len(result.Profiles) != 3 || result.Processes != 1 || result.Reason != "" || result.Partial {
		t.Fatalf("capture = %+v, want three profiles from one process", result)
	}
	got := map[string]string{}
	for _, p := range result.Profiles {
		if p.ProcessID != "4242" {
			t.Fatalf("profile %s labelled with process %q, want the workload's 4242", p.Kind, p.ProcessID)
		}
		got[p.Kind] = string(p.Profile)
	}
	if got["cpu"] != "cpu-profile" || got["heap"] != "heap-profile" || got["stacks"] != "stacks-profile" {
		t.Fatalf("profiles = %v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seconds) != 1 || f.seconds[0] != "2" {
		t.Fatalf("CPU requests asked for seconds %v, want one request for the 2 s window", f.seconds)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPprofScraperReadsBlockAndMutexOverTheWindow(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	f := &fakePprof{}
	startPprofScraper(t, b, f.start(t))
	waitFor(t, "the scraper to register", b.locked(func() bool { return !b.pprofSeen.IsZero() }))
	req := captureRequest("block", "mutex")
	result, _ := b.runCapture(req, func(d time.Duration) {
		if d == req.Duration() {
			waitFor(t, "two contention profiles", b.locked(func() bool { return b.capture != nil && len(b.capture.profiles) == 2 }))
		}
	})
	got := map[string]string{}
	for _, p := range result.Profiles {
		got[p.Kind] = string(p.Profile)
	}
	if len(result.Profiles) != 2 || got["block"] != "block-delta" || got["mutex"] != "mutex-delta" || result.Reason != "" {
		t.Fatalf("capture = %+v, want one block and one mutex delta", result)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	asked := strings.Join(f.seconds, ",")
	if !strings.Contains(asked, "block=2") || !strings.Contains(asked, "mutex=2") || f.hits["profile"] != 0 {
		t.Fatalf("requests = %q (cpu=%d); want block and mutex deltas over the 2 s window and no CPU profile", asked, f.hits["profile"])
	}
}

func TestPprofScraperStandsDownForTheApplicationsCollector(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	h := b.handler()
	f := &fakePprof{}
	pollControl(t, h, "7")
	startPprofScraper(t, b, f.start(t))
	result, _ := b.runCapture(captureRequest("cpu", "heap"), func(d time.Duration) {
		// The SDK polls every 100 ms while a capture runs.
		for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			pollControl(t, h, "7")
		}
	})
	if f.count("profile") != 0 || f.count("heap") != 0 {
		t.Fatalf("scraper fetched while the application's collector polled: profile=%d heap=%d", f.count("profile"), f.count("heap"))
	}
	if result.Processes != 1 {
		t.Fatalf("processes = %d, want only the application's collector", result.Processes)
	}
	b.mu.Lock()
	b.processes["7"] = time.Now().Add(-2 * api.ProfileProcessStaleAfter)
	b.mu.Unlock()
	if _, ok := b.pprofControl("4242"); !ok {
		t.Fatal("scraper stayed down after the application's collector went stale")
	}
}

func TestPprofScraperExplainsAFailedKind(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	f := &fakePprof{status: map[string]int{"profile": http.StatusInternalServerError}}
	port := f.start(t)
	startPprofScraper(t, b, port)
	waitFor(t, "the scraper to register", b.locked(func() bool { return !b.pprofSeen.IsZero() }))
	req := captureRequest("cpu", "heap")
	result, _ := b.runCapture(req, func(d time.Duration) {
		if d == req.Duration() {
			waitFor(t, "the heap profile", b.locked(func() bool { return b.capture != nil && len(b.capture.profiles) == 1 }))
		}
	})
	if len(result.Profiles) != 1 || result.Profiles[0].Kind != "heap" {
		t.Fatalf("profiles = %+v, want the heap profile despite the CPU failure", result.Profiles)
	}
	if !strings.Contains(result.Reason, "cpu profile") || !strings.Contains(result.Reason, "HTTP 500") || strings.Contains(result.Reason, "profiling failed") {
		t.Fatalf("reason = %q, want the CPU failure with its status and without the response body", result.Reason)
	}
}

func TestPprofScraperExplainsAnEndpointThatNeverAnswered(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	startPprofScraper(t, b, port)
	result, _ := b.runCapture(captureRequest("cpu"), func(time.Duration) {
		waitFor(t, "a probe during the capture", b.locked(func() bool { return b.capture == nil || b.capture.probeNote != "" }))
	})
	if result.Processes != 0 || !strings.Contains(result.Reason, "nothing answered at 127.0.0.1:") {
		t.Fatalf("capture = %+v, want the unanswered endpoint as the reason", result)
	}
	if profileCollectorsActiveFor(b) {
		t.Fatal("an endpoint that never answered counts as a collector for memory pressure")
	}
}

func TestPprofScraperKeepsHeapCheckpointsThroughAnOOMKill(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	f := &fakePprof{}
	startPprofScraper(t, b, f.start(t))
	waitFor(t, "the scraper to register", b.locked(func() bool { return !b.pprofSeen.IsZero() }))
	req := captureRequest("heap")
	req.CheckpointMillis = 1000
	req.DurationMillis = 5000
	result, _ := b.runCapture(req, func(d time.Duration) {
		if d == req.Duration() {
			waitFor(t, "a heap checkpoint", b.locked(func() bool { return b.capture != nil && len(b.capture.checkpoints) == 1 }))
			b.interruptCaptureForOOM(480)
		}
	})
	if !result.Partial || !result.OOMKilled || result.Reason != captureInterruptedOOM {
		t.Fatalf("capture = %+v, want a partial reply marked with the kill", result)
	}
	if len(result.Profiles) != 1 || result.Profiles[0].Kind != "heap" || result.Profiles[0].ProcessID != "4242" {
		t.Fatalf("profiles = %+v, want the scraped heap checkpoint", result.Profiles)
	}
}

func TestPprofScraperStopsForACheckpoint(t *testing.T) {
	var mu sync.Mutex
	var forwarded int
	b := newProfileBridge(&api.ProfilingConfig{Enabled: true, WindowSeconds: 1}, func(context.Context, profileproto.Upload) error {
		mu.Lock()
		forwarded++
		mu.Unlock()
		return nil
	})
	f := &fakePprof{blockCPU: true}
	startPprofScraper(t, b, f.start(t))
	select {
	case <-f.cpuStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("continuous collection never started a CPU profile")
	}
	b.checkpoint()
	select {
	case <-f.cpuGone:
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight CPU profile was not abandoned for the checkpoint")
	}
	waitFor(t, "the scraper's acknowledgement", b.locked(func() bool { return b.pprofAck == b.control.Epoch }))
	mu.Lock()
	defer mu.Unlock()
	if forwarded != 0 {
		t.Fatalf("forwarded %d pre-checkpoint profiles, want none", forwarded)
	}
}

func TestPprofScraperFetchBounds(t *testing.T) {
	f := &fakePprof{heap: strings.Repeat("x", api.ProfileMaxCompressedBytes+1)}
	s := newPprofScraper(nil, f.start(t), 1)
	if _, err := s.fetch(context.Background(), "/debug/pprof/heap", time.Second, api.ProfileMaxCompressedBytes); !errors.Is(err, errPprofTooLarge) {
		t.Fatalf("oversized profile: err = %v, want errPprofTooLarge", err)
	}
	if _, err := s.fetch(context.Background(), "/debug/pprof/missing", time.Second, api.ProfileMaxCompressedBytes); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("missing path: err = %v, want HTTP 404", err)
	}
}

func TestRunPprofScraperRefusesGuestInitPorts(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	for _, port := range append(api.ProfilePprofReservedPorts(), 0, 65536) {
		done := make(chan struct{})
		go func() { runPprofScraper(context.Background(), b, port, 1, nil); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("port %d: scraper started instead of refusing", port)
		}
	}
}

func TestPprofScraperCountsForMemoryPressure(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	if profileCollectorsActiveFor(b) {
		t.Fatal("an idle bridge reported an active collector")
	}
	if _, ok := b.pprofControl("4242"); !ok || !profileCollectorsActiveFor(b) {
		t.Fatal("a registered scraper does not enable memory-pressure reports")
	}
}

func profileCollectorsActiveFor(b *profileBridge) bool {
	guestProfiles.Lock()
	previous := guestProfiles.bridge
	guestProfiles.bridge = b
	guestProfiles.Unlock()
	defer func() { guestProfiles.Lock(); guestProfiles.bridge = previous; guestProfiles.Unlock() }()
	return profileCollectorsActive()
}
