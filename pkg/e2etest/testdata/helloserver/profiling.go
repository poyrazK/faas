package main

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/guestprofiling"
)

// The fixture is a Go app that opts into Gregale profiling. guestprofiling
// stays dormant unless guest-init stamps the collector environment, so every
// other e2e test sees the plain hello server (ADR-967).
func startProfiling() { guestprofiling.Start(context.Background()) }

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
