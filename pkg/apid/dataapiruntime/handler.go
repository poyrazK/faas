// Package dataapiruntime serves the Data API runtime apid is built with
// (ADR-20261031095954). The CLI deploys this runtime instead of its own copy,
// so a customer on an old CLI release still gets the platform's current
// runtime, and owner tooling compares a running API's version with it.
package dataapiruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

// Current returns the embedded runtime, built once per process.
var Current = sync.OnceValues(func() (api.DataAPIRuntime, error) {
	version, err := templates.DataAPIRuntimeVersion()
	if err != nil {
		return api.DataAPIRuntime{}, err
	}
	archive, err := templates.DataAPIRuntimeArchive()
	if err != nil {
		return api.DataAPIRuntime{}, err
	}
	sum := sha256.Sum256(archive)
	runtime := api.DataAPIRuntime{Version: version, ArchiveSHA256: hex.EncodeToString(sum[:]), Archive: archive}
	if err = runtime.Validate(); err != nil {
		return api.DataAPIRuntime{}, fmt.Errorf("embedded Data API runtime: %w", err)
	}
	return runtime, nil
})

// Serve writes the current runtime. The caller has authorized the app.
func Serve(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	runtime, err := Current()
	if err != nil {
		slog.Error("data api runtime unavailable", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "data_api_runtime_unavailable", "Data API runtime unavailable", "the control plane could not load its Data API runtime"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(runtime)
}
