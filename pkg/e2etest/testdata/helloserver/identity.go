package main

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// serveIdentityProbe adds /identity for the -identity-probe fixture: it asks
// guest-init's loopback workload identity endpoint for a token and relays
// the status and body unchanged, so a test can verify the token.
func serveIdentityProbe(mux *http.ServeMux) {
	mux.HandleFunc("/identity", func(w http.ResponseWriter, r *http.Request) {
		endpoint, err := url.Parse(os.Getenv("FAAS_WORKLOAD_IDENTITY_ENDPOINT"))
		if err != nil || endpoint.Host == "" {
			http.Error(w, "no workload identity endpoint", http.StatusServiceUnavailable)
			return
		}
		query := endpoint.Query()
		query.Set("audience", r.URL.Query().Get("audience"))
		endpoint.RawQuery = query.Encode()
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(endpoint.String())
		if err != nil {
			http.Error(w, "identity endpoint unreachable: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 16<<10))
	})
}
