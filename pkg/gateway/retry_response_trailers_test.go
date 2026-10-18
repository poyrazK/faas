// adr: 197
// adr: 201
package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBufferedResponseWritersPreserveFinalTrailers(t *testing.T) {
	for _, writer := range []struct {
		name  string
		serve func(http.ResponseWriter, *http.Request, http.Handler)
	}{
		{"retry", func(w http.ResponseWriter, r *http.Request, origin http.Handler) {
			// HTTP/2 supplies a body even for this empty GET. Mirror body
			// admission so both protocols exercise the retry buffer.
			r.GetBody = func() (io.ReadCloser, error) { return http.NoBody, nil }
			runWithRetry(w, r, Target{}, testPolicy(), func(Target) {},
				func(w http.ResponseWriter, r *http.Request, _ Target) { origin.ServeHTTP(w, r) },
				func() (Target, bool) { return Target{}, false }, nil)
		}},
		{"service proxy", func(w http.ResponseWriter, r *http.Request, origin http.Handler) {
			buffer := newServiceProxyResponseWriter(w)
			origin.ServeHTTP(buffer, r)
			buffer.commit()
			buffer.commitTrailers()
		}},
	} {
		for _, protocol := range []string{"http1", "http2"} {
			for _, tc := range []struct {
				name    string
				status  int
				flush   bool
				initial bool
			}{
				{"success", http.StatusOK, false, false},
				{"updated trailer values", http.StatusOK, false, true},
				{"buffered error", http.StatusServiceUnavailable, false, false},
				{"flushed error", http.StatusServiceUnavailable, true, false},
			} {
				t.Run(writer.name+"/"+protocol+"/"+tc.name, func(t *testing.T) {
					origin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "application/grpc")
						w.Header().Add("Trailer", "Grpc-Status, Grpc-Message")
						if tc.initial {
							w.Header().Set("Grpc-Status", "0")
							w.Header()["Grpc-Message"] = nil
						}
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte("response body"))
						if tc.flush {
							_ = http.NewResponseController(w).Flush()
						}
						w.Header().Set("Grpc-Status", "7")
						w.Header().Set("Grpc-Message", "permission denied")
						w.Header().Set(http.TrailerPrefix+"X-Final", "complete")
					})
					server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						writer.serve(w, r, origin)
					}))
					if protocol == "http2" {
						server.EnableHTTP2 = true
						server.StartTLS()
					} else {
						server.Start()
					}
					t.Cleanup(server.Close)
					response, err := server.Client().Get(server.URL)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					body, err := io.ReadAll(response.Body)
					if err != nil || response.StatusCode != tc.status || string(body) != "response body" {
						t.Fatalf("response=%d %q, err=%v", response.StatusCode, body, err)
					}
					if protocol == "http2" && response.ProtoMajor != 2 {
						t.Fatalf("expected HTTP/2, got %s", response.Proto)
					}
					for key, want := range map[string]string{"Grpc-Status": "7", "Grpc-Message": "permission denied", "X-Final": "complete"} {
						if got := response.Trailer.Get(key); got != want {
							t.Errorf("trailer %s = %q, want %q", key, got, want)
						}
					}
				})
			}
		}
	}
}

func TestHandlerRetryPreservesTrailersAfterReplay(t *testing.T) {
	h, _, _ := retryTestHandler(t)
	h.WithRetryEnabled(true).WithRetryDefault(RetryPolicy{MaxAttempts: 2})
	var attempts atomic.Int32
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			if target.InstanceID == "instance-dead" {
				w.Header().Set("X-Discarded-Attempt", "must not escape")
				markStaleTarget(r.Context())
				http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Add("Trailer", "X-Checksum")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("successful replay"))
			w.Header().Set("X-Checksum", "verified")
			w.Header().Set(http.TrailerPrefix+"X-Final", "complete")
		})
	})
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	r, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "jane-api.apps.dom"
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "successful replay" || attempts.Load() != 2 {
		t.Fatalf("response=%d %q, attempts=%d, err=%v", response.StatusCode, body, attempts.Load(), err)
	}
	if response.Header.Get("X-Discarded-Attempt") != "" || strings.Contains(string(body), "unavailable") {
		t.Fatal("the failed attempt escaped into the response")
	}
	if response.Trailer.Get("X-Checksum") != "verified" || response.Trailer.Get("X-Final") != "complete" {
		t.Fatalf("successful replay lost its trailers: %v", response.Trailer)
	}
}
