package internal

import (
	"context"
	"io"
	"log"
	"net/http"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
)

// serveFunctionHook answers guest-init's call for a snapshot hook the function
// declares in code (ADR-20261110190000 §3): it runs the hook in every loaded
// worker and answers 204 only when every one of them succeeded. Any failure,
// timeout or worker that cannot run it answers 503, which guest-init treats as
// a failed hook: after a restore the instance is discarded and the wake cold
// boots; before a snapshot no snapshot is captured.
func serveFunctionHook(w http.ResponseWriter, r *http.Request, cfg ServeConfig, hook string) {
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
	ctx, cancel := context.WithTimeout(r.Context(), invocationTimeout(r.Header))
	defer cancel()
	env := Envelope{Method: http.MethodPost, Path: r.URL.Path, Headers: map[string]string{},
		LifecycleHook: lifecycleHook(r), FunctionHook: hook}
	results, err := workerpool.BroadcastIfSupported(ctx, cfg.Spec, env)
	if err != nil {
		log.Printf("%s runner: function %s hook: %v", cfg.Runtime, hook, err)
		http.Error(w, "function hook failed", http.StatusServiceUnavailable)
		return
	}
	for i, result := range results {
		if result.Status < 200 || result.Status >= 300 {
			log.Printf("%s runner: function %s hook failed in worker %d of %d (status %d)", cfg.Runtime, hook, i+1, len(results), result.Status)
			http.Error(w, "function hook failed", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
