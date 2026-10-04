// hello-gregale: minimal stdlib HTTP handler for gregale.
//
// Listens on :8080 (the port guest-init forwards to). No external deps
// so the build is fast and the binary is tiny.
package main

import (
	"encoding/json"
	"net/http"
	"os"
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
	// This URL is public: do not echo environment variable names here
	// (secret names reveal integrations). `gregale secrets list --app
	// <slug>` shows which keys the app has.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"message":    "hello from gregale",
		"go_version": runtimeGoVersion(),
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
