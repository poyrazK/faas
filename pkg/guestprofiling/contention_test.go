package guestprofiling

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
)

var contendedMu sync.Mutex

//go:noinline
func firstWindowLockFixture() {
	contendedMu.Lock()
	time.Sleep(2 * time.Millisecond)
	contendedMu.Unlock()
}

//go:noinline
func secondWindowLockFixture() {
	contendedMu.Lock()
	time.Sleep(2 * time.Millisecond)
	contendedMu.Unlock()
}

//go:noinline
func windowBlockFixture(ch <-chan struct{}) { <-ch }

func contend(lock func()) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); lock() }()
	}
	wg.Wait()
}

func block(d time.Duration) {
	ch := make(chan struct{})
	done := make(chan struct{})
	go func() { windowBlockFixture(ch); close(done) }()
	time.Sleep(d)
	close(ch)
	<-done
}

// profileFunctions reports whether a gzip pprof profile has a non-zero sample
// through each named function.
func profileFunctions(t *testing.T, raw []byte) map[string]bool {
	t.Helper()
	p, err := profile.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range p.Sample {
		for _, loc := range s.Location {
			for _, line := range loc.Line {
				name := line.Function.Name
				if i := strings.LastIndex(name, "."); i >= 0 {
					name = name[i+1:]
				}
				seen[name] = true
			}
		}
	}
	return seen
}

func TestContentionWindowsReportOnlyTheirOwnDelay(t *testing.T) {
	if previous := runtime.SetMutexProfileFraction(-1); previous != 0 {
		t.Skipf("mutex profiling already enabled (%d) by another test", previous)
	}
	first := startContention(api.ProfileKindMutex)
	if first == nil || runtime.SetMutexProfileFraction(-1) != api.ProfileMutexFraction {
		t.Fatal("a capture window did not enable mutex sampling the application left off")
	}
	for i := 0; i < 20; i++ {
		contend(firstWindowLockFixture)
	}
	raw, err := first.finish(true)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.SetMutexProfileFraction(-1) != 0 {
		t.Fatal("mutex sampling not turned back off after the window")
	}
	if !profileFunctions(t, raw)["firstWindowLockFixture"] {
		t.Fatal("first window lacks its own contention")
	}
	second := startContention(api.ProfileKindMutex)
	for i := 0; i < 20; i++ {
		contend(secondWindowLockFixture)
	}
	raw, err = second.finish(true)
	if err != nil {
		t.Fatal(err)
	}
	seen := profileFunctions(t, raw)
	if seen["firstWindowLockFixture"] || !seen["secondWindowLockFixture"] {
		t.Fatalf("second window functions = %v; want only its own contention, not the first window's", seen)
	}
}

func TestContentionKeepsTheApplicationsMutexFraction(t *testing.T) {
	previous := runtime.SetMutexProfileFraction(3)
	defer runtime.SetMutexProfileFraction(previous)
	c := startContention(api.ProfileKindMutex)
	if c == nil || runtime.SetMutexProfileFraction(-1) != 3 {
		t.Fatal("a capture changed the application's own mutex fraction")
	}
	if _, err := c.finish(false); err != nil || runtime.SetMutexProfileFraction(-1) != 3 {
		t.Fatalf("finish changed the application's mutex fraction: %v", err)
	}
}

func TestGoCollectorCaptureUploadsBlockAndMutex(t *testing.T) {
	if previous := runtime.SetMutexProfileFraction(-1); previous != 0 {
		t.Skipf("mutex profiling already enabled (%d) by another test", previous)
	}
	var mu sync.Mutex
	bodies := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control":
			_ = json.NewEncoder(w).Encode(control{Enabled: true, Epoch: "33333333333333333333333333333333", WindowSeconds: 1, Kinds: []string{"block", "mutex"}, Capture: true})
		case "/ingest":
			body, _ := io.ReadAll(io.LimitReader(r.Body, api.ProfileMaxCompressedBytes+1))
			mu.Lock()
			bodies[r.URL.Query().Get("kind")] = body
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); run(ctx, server.URL) }()
	// Contend while the window is open: it starts within one control poll.
	time.Sleep(300 * time.Millisecond)
	for i := 0; i < 20; i++ {
		contend(secondWindowLockFixture)
	}
	block(50 * time.Millisecond)
	time.Sleep(1500 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(bodies["mutex"]) == 0 || len(bodies["block"]) == 0 || len(bodies) != 2 {
		t.Fatalf("uploaded kinds = %v; want one block and one mutex profile", keys(bodies))
	}
	if !profileFunctions(t, bodies["mutex"])["secondWindowLockFixture"] {
		t.Fatal("mutex capture lacks the contended lock")
	}
	if !profileFunctions(t, bodies["block"])["windowBlockFixture"] {
		t.Fatal("block capture lacks the blocked receive")
	}
	if runtime.SetMutexProfileFraction(-1) != 0 {
		t.Fatal("mutex sampling left on after the capture")
	}
}

func keys(m map[string][]byte) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
