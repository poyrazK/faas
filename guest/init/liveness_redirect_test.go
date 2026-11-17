// adr: 079 — amendment 1: a liveness probe follows only same-origin redirects.
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLivenessProbeRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/slash", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/slash/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/slash/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/https", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.URL.Path, http.StatusMovedPermanently)
	})
	mux.HandleFunc("/idp", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://accounts.example.com/authorize", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := &http.Client{CheckRedirect: livenessCheckRedirect}

	for _, tc := range []struct {
		path    string
		status  int
		wantErr bool
	}{
		{path: "/slash", status: http.StatusOK},
		{path: "/https", status: http.StatusMovedPermanently},
		{path: "/idp", status: http.StatusFound},
		{path: "/loop", wantErr: true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := client.Get(srv.URL + tc.path)
			if tc.wantErr {
				if err == nil {
					_ = resp.Body.Close()
					t.Fatal("redirect loop: want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("GET %s status = %d, want %d", tc.path, resp.StatusCode, tc.status)
			}
		})
	}
}
