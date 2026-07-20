// hello-faas: minimal stdlib HTTP handler for faas.
//
// Listens on :8080 (the port guest-init forwards to). No external deps
// so the build is fast and the binary is tiny.
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/healthz", handleHealthz)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	// Use http.Server with explicit timeouts so a slowloris-style
	// client can't pin the guest's idle timeout. 60s is well above
	// guest-init's max request budget and matches the platform's
	// normal tail behaviour.
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	_ = srv.ListenAndServe()
}

func handleRoot(w http.ResponseWriter, _ *http.Request) {
	// Surface customer secret KEY names only — values never cross
	// the response boundary.
	skipPrefix := "FAAS_"
	keys := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		k := kv[:i]
		if strings.HasPrefix(k, skipPrefix) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"message":     "hello from faas",
		"go_version":  runtimeGoVersion(),
		"secret_keys": keys,
	})
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func runtimeGoVersion() string {
	// runtime.Version() is constant in the binary; embedding at build
	// time keeps the response self-describing without leaking the host.
	return runtimeVersion
}
