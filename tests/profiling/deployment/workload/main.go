// A disposable deployed workload for route profiling acceptance.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/guestprofiling"
)

var sink atomic.Uint64
var requests atomic.Uint64

//go:noinline
func hotWork(iterations int) uint64 {
	x := uint64(1)
	for i := 0; i < iterations; i++ {
		x = x*6364136223846793005 + 1
	}
	return x
}

func main() {
	mode := os.Getenv("PROFILE_ACCEPTANCE_MODE")
	if mode == "" {
		mode = "baseline"
	}
	if mode != "baseline" && mode != "regression" && mode != "label_loss" && mode != "sparse" {
		log.Fatal("invalid PROFILE_ACCEPTANCE_MODE")
	}
	guestprofiling.Start(context.Background())
	mux := http.NewServeMux()
	installNativeProbe(mux)
	ready := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"mode": mode, "requests": requests.Load()})
	}
	mux.HandleFunc("GET /healthz", ready)
	mux.HandleFunc("GET /readyz", ready)
	mux.HandleFunc("GET /hot/{id}", func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		work := func(context.Context) {
			iterations := 8000000
			if mode == "regression" {
				iterations *= 4
			}
			sink.Store(hotWork(iterations))
		}
		if mode == "label_loss" && n%2 == 0 {
			// Keep CPU labels while omitting half the request-entry counters.
			guestprofiling.WithRoute(r.Context(), "GET /hot/{id}", work)
		} else {
			guestprofiling.WithRouteRequest(r.Context(), "GET /hot/{id}", work)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if _, err := strconv.Atoi(port); err != nil {
		log.Fatal("invalid PORT")
	}
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
