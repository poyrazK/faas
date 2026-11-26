// hello-gregale: minimal stdlib HTTP handler for gregale.
//
// Listens on $PORT, which Gregale sets for every app (8080 when run
// locally). No external deps so the build is fast and the binary is tiny.
package main

import (
	"encoding/json"
	"log"
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
	// Use http.Server with explicit timeouts so a slow client that
	// trickles its request can't keep the app from going idle. 60s
	// leaves room for slow handlers while still closing stuck
	// connections.
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	// Anything the app writes to stdout or stderr shows in `gregale logs`.
	// Requests are recorded separately: `gregale logs --source http`.
	log.Printf("hello-go listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

func handleRoot(w http.ResponseWriter, _ *http.Request) {
	// This URL may be made public (access: public): do not echo env names
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
