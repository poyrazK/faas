package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// crashCaptureEndpoint is the guest metadata endpoint for the ADR-733 SDK
// trigger, served by guest-init.
const crashCaptureEndpoint = "http://169.254.169.254/v1/crash-snapshots:capture"

// serveCrashCapture adds the -crash-capture fixture routes:
//
//	/boom          asks for a crash capture of this instance from inside the
//	               handler (as an app's error handler would), records the
//	               answer and fails with 500.
//	/last-capture  returns the last recorded answer. In a fork of the
//	               capture, the /boom call resumes and records in_fork.
//
//	/counter       POST increments an in-memory counter, GET returns it, so a
//	               test can tell which moment a fork's memory comes from.
func serveCrashCapture(mux *http.ServeMux) {
	var mu sync.Mutex
	last := json.RawMessage(`null`)
	counter := 0
	mux.HandleFunc("/counter", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Method == http.MethodPost {
			counter++
		}
		n := counter
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"counter": n})
	})
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		req, _ := json.Marshal(map[string]any{"reason": "fixture boom", "route": r.URL.Path, "wait_ms": 20000})
		client := &http.Client{Timeout: 60 * time.Second}
		answer := []byte(`{"status":"unreachable"}`)
		if resp, err := client.Post(crashCaptureEndpoint, "application/json", bytes.NewReader(req)); err == nil {
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(resp.Body)
			_ = resp.Body.Close()
			answer = bytes.TrimSpace(buf.Bytes())
		}
		mu.Lock()
		last = append(json.RawMessage(nil), answer...)
		mu.Unlock()
		// ?ok=1 answers 200, so the gateway's 5xx trigger does not also ask
		// for a capture.
		status := http.StatusInternalServerError
		if r.URL.Query().Get("ok") == "1" {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(answer)
	})
	mux.HandleFunc("/last-capture", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		body := last
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}
