package main

import (
	"context"
	"log"
	"net/http"
	"net/http/pprof"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/guestprofiling"
)

// The fixture is a Go app that opts into Gregale profiling. guestprofiling
// stays dormant unless guest-init stamps the collector environment, so every
// other e2e test sees the plain hello server (ADR-1155).
func startProfiling() { guestprofiling.Start(context.Background()) }

// servePprof is the uninstrumented variant: it serves net/http/pprof on addr
// and never starts the Gregale collector, like a Go application that only
// sets profiling.pprof_port. It samples contention itself, as such an
// application must for its block and mutex endpoints to have data.
func servePprof(addr string) {
	runtime.SetBlockProfileRate(10_000)
	runtime.SetMutexProfileFraction(10)
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	go func() { log.Fatal(http.ListenAndServe(addr, mux)) }()
}

var (
	retainedMu sync.Mutex
	retained   [][]byte
)

const maxRetained = 1024

//go:noinline
func profileFixtureBurnCPU(d time.Duration) uint64 {
	x := uint64(1)
	for deadline := time.Now().Add(d); time.Now().Before(deadline); {
		for i := 0; i < 10000; i++ {
			x = x*6364136223846793005 + 1442695040888963407
		}
	}
	return x
}

//go:noinline
func profileFixtureRetain(n int) {
	retainedMu.Lock()
	defer retainedMu.Unlock()
	for i := 0; i < n; i++ {
		retained = append(retained, make([]byte, 16<<10))
	}
	if len(retained) > maxRetained {
		retained = retained[len(retained)-maxRetained:]
	}
}

var grown [][]byte

const maxGrownMB = 512

//go:noinline
func profileFixtureGrow(mb int) int {
	retainedMu.Lock()
	defer retainedMu.Unlock()
	for i := 0; i < mb && len(grown) < maxGrownMB; i++ {
		b := make([]byte, 1<<20)
		// Touch every page so the guest counts the memory as used.
		for j := 0; j < len(b); j += 4096 {
			b[j] = 1
		}
		grown = append(grown, b)
	}
	return len(grown)
}

// serveGrow retains N more MiB to drive guest memory pressure:
// GET /grow?mb=4. GET /release drops everything grown.
func serveGrow(w http.ResponseWriter, r *http.Request) {
	mb, err := strconv.Atoi(r.URL.Query().Get("mb"))
	if err != nil || mb < 1 || mb > 64 {
		mb = 4
	}
	_, _ = w.Write([]byte(strconv.Itoa(profileFixtureGrow(mb)) + "\n"))
}

func serveRelease(w http.ResponseWriter, _ *http.Request) {
	retainedMu.Lock()
	grown = nil
	retainedMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// hangGate is never signalled: a hanging request waits on it until its
// timeout, like a request stuck on a lock or a pool that never frees up.
var hangGate = make(chan struct{})

//go:noinline
func profileFixtureHang(d time.Duration) {
	select {
	case <-hangGate:
	case <-time.After(d):
	}
}

// serveHang holds the request for N seconds (default 15) so the gateway sees
// it in flight: GET /hang?s=15.
func serveHang(w http.ResponseWriter, r *http.Request) {
	s, err := strconv.Atoi(r.URL.Query().Get("s"))
	if err != nil || s < 1 || s > 60 {
		s = 15
	}
	profileFixtureHang(time.Duration(s) * time.Second)
	_, _ = w.Write([]byte("released\n"))
}

//go:noinline
func profileFixtureSpin(d time.Duration) uint64 {
	x := uint64(1)
	for deadline := time.Now().Add(d); time.Now().Before(deadline); {
		for i := 0; i < 10000; i++ {
			x = x*2862933555777941757 + 3037000493
		}
	}
	return x
}

// serveSpin burns CPUs in the background and answers at once, so the
// instance sits at its CPU limit without a slow request: GET
// /spin?s=60&cpus=2 burns two CPUs for 60 seconds.
func serveSpin(w http.ResponseWriter, r *http.Request) {
	s, err := strconv.Atoi(r.URL.Query().Get("s"))
	if err != nil || s < 1 || s > 300 {
		s = 60
	}
	cpus, err := strconv.Atoi(r.URL.Query().Get("cpus"))
	if err != nil || cpus < 1 || cpus > 4 {
		cpus = 1
	}
	for range cpus {
		go profileFixtureSpin(time.Duration(s) * time.Second)
	}
	w.WriteHeader(http.StatusAccepted)
}

var contendedMu sync.Mutex

//go:noinline
func profileFixtureLockHold(d time.Duration) {
	contendedMu.Lock()
	time.Sleep(d)
	contendedMu.Unlock()
}

//go:noinline
func profileFixtureChannelWait(ch <-chan struct{}) { <-ch }

// serveContend makes four goroutines take one lock in turn, holding it ms
// each (mutex contention), while another waits on a channel until they are
// done (blocking): GET /contend?ms=20.
func serveContend(w http.ResponseWriter, r *http.Request) {
	ms, err := strconv.Atoi(r.URL.Query().Get("ms"))
	if err != nil || ms < 1 || ms > 200 {
		ms = 20
	}
	release, waited := make(chan struct{}), make(chan struct{})
	go func() { profileFixtureChannelWait(release); close(waited) }()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); profileFixtureLockHold(time.Duration(ms) * time.Millisecond) }()
	}
	wg.Wait()
	close(release)
	<-waited
	_, _ = w.Write([]byte("contended\n"))
}

// serveWork burns CPU and retains heap so captures have known hot frames:
// GET /work?ms=50&retain=8.
func serveWork(w http.ResponseWriter, r *http.Request) {
	ms, err := strconv.Atoi(r.URL.Query().Get("ms"))
	if err != nil || ms < 0 || ms > 1000 {
		ms = 50
	}
	retain, err := strconv.Atoi(r.URL.Query().Get("retain"))
	if err != nil || retain < 0 || retain > 64 {
		retain = 8
	}
	sum := profileFixtureBurnCPU(time.Duration(ms) * time.Millisecond)
	profileFixtureRetain(retain)
	_, _ = w.Write([]byte(strconv.FormatUint(sum%10, 10) + "\n"))
}
