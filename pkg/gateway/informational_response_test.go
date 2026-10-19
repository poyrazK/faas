// spec: §4.1
// adr: 122, 125, 197, 201, 1188, 1202, 1505
package gateway

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestResponseWritersPreserveFinalResponseAfterEarlyHints(t *testing.T) {
	for _, wrapper := range []struct {
		name string
		wrap func(*testing.T, http.ResponseWriter, *http.Request, int) (http.ResponseWriter, func())
	}{
		{"retry", func(_ *testing.T, w http.ResponseWriter, _ *http.Request, _ int) (http.ResponseWriter, func()) {
			rw := newRetryWriter(w)
			return rw, rw.finish
		}},
		{"service buffer", func(_ *testing.T, w http.ResponseWriter, _ *http.Request, _ int) (http.ResponseWriter, func()) {
			rw := newServiceProxyResponseWriter(w)
			return rw, func() { rw.commit(); rw.commitTrailers() }
		}},
		{"service trace", func(t *testing.T, w http.ResponseWriter, _ *http.Request, status int) (http.ResponseWriter, func()) {
			rw := &serviceProxyTraceResponseWriter{ResponseWriter: w}
			return rw, func() {
				if rw.status != status {
					t.Errorf("trace status = %d, want final %d", rw.status, status)
				}
			}
		}},
		{"status recorder", func(t *testing.T, w http.ResponseWriter, r *http.Request, status int) (http.ResponseWriter, func()) {
			rw := &statusRecorder{ResponseWriter: w, request: r}
			return rw, func() {
				if rw.status != status || rw.ContentType != "text/plain" {
					t.Errorf("recorded status=%d, type=%q, want final %d text/plain", rw.status, rw.ContentType, status)
				}
			}
		}},
		{"cache", func(t *testing.T, w http.ResponseWriter, _ *http.Request, status int) (http.ResponseWriter, func()) {
			rw := newCacheWriter(w, nil, &EdgeRuleCacheResolved{ID: "rule", MaxAgeSeconds: 60}, 1024)
			return rw, func() {
				if rw.status != status || rw.header.Get("X-Final") != "yes" || rw.shouldStore() != (status != http.StatusServiceUnavailable) {
					t.Errorf("cache captured interim state: status=%d header=%v storable=%v", rw.status, rw.header, rw.shouldStore())
				}
			}
		}},
		{"content observer", func(t *testing.T, w http.ResponseWriter, r *http.Request, status int) (http.ResponseWriter, func()) {
			rw := newContentTee(w, newContentObserver(4), "app", r)
			return rw, func() {
				want := api.ContentOutcomeFirst
				if status == http.StatusServiceUnavailable {
					want = api.ContentOutcomeUncacheable
				}
				if got := rw.finish(time.Now()); got != want {
					t.Errorf("content outcome = %s, want %s for final %d", got, want, status)
				}
			}
		}},
	} {
		for _, h2 := range []bool{false, true} {
			for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusServiceUnavailable} {
				t.Run(fmt.Sprintf("%s/h2=%v/status=%d", wrapper.name, h2, status), func(t *testing.T) {
					server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						rw, finish := wrapper.wrap(t, w, r, status)
						for _, hint := range []string{"first", "second"} {
							rw.Header().Set("X-Interim", hint)
							rw.WriteHeader(http.StatusEarlyHints)
							// This is also how ReverseProxy separates its header blocks.
							clear(rw.Header())
						}
						rw.Header().Set("Content-Type", "text/plain")
						rw.Header().Set("X-Final", "yes")
						rw.WriteHeader(status)
						_, _ = rw.Write([]byte("final body"))
						finish()
					}))
					server.EnableHTTP2 = h2
					if h2 {
						server.StartTLS()
					} else {
						server.Start()
					}
					t.Cleanup(server.Close)
					var mu sync.Mutex
					var hints []string
					trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
						mu.Lock()
						defer mu.Unlock()
						hints = append(hints, fmt.Sprintf("%d:%s", code, header.Get("X-Interim")))
						return nil
					}}
					req, err := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), http.MethodGet, server.URL, nil)
					if err != nil {
						t.Fatal(err)
					}
					response, err := server.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					body, err := io.ReadAll(response.Body)
					if err != nil || response.StatusCode != status || string(body) != "final body" {
						t.Errorf("final response = %d %q, err=%v; want %d final body", response.StatusCode, body, err, status)
					}
					if response.Header.Get("X-Final") != "yes" || response.Header.Get("X-Interim") != "" {
						t.Errorf("interim headers contaminated final headers: %v", response.Header)
					}
					mu.Lock()
					defer mu.Unlock()
					if !slices.Equal(hints, []string{"103:first", "103:second"}) {
						t.Errorf("interim responses = %v, want both early hints", hints)
					}
				})
			}
		}
	}
}

func TestStatusRecorderEarlyHintsPreserveFinalHeaderRules(t *testing.T) {
	// Recorder supports 1xx only on real net/http writers; ResponseRecorder
	// treats any first status as final, so use a minimal header-block capture.
	live := &interimHeaderProbe{header: make(http.Header)}
	rec := &statusRecorder{ResponseWriter: live}
	rec.installHeaderOps([]EdgeRuleHeaderOp{{Action: "add", Name: "X-Edge-Rule", Value: "applied"}})
	cached := newCacheWriter(rec, rec, &EdgeRuleCacheResolved{ID: "rule", MaxAgeSeconds: 60}, 1024)
	cached.Header().Set(preAuthTargetHeader, "interim-target")
	cached.Header().Set("Cache-Tag", "interim-tag")
	cached.WriteHeader(http.StatusEarlyHints)
	clear(cached.Header())
	cached.Header().Set(preAuthTargetHeader, "final-target")
	cached.Header().Set("Cache-Tag", "final-tag")
	cached.WriteHeader(http.StatusOK)
	if len(live.headers) != 2 {
		t.Fatalf("header blocks = %d, want interim and final", len(live.headers))
	}
	for _, header := range live.headers {
		if header.Get(preAuthTargetHeader) != "" || header.Get("Cache-Tag") != "" {
			t.Errorf("control metadata escaped: %v", header)
		}
		if values := header.Values("X-Edge-Rule"); !slices.Equal(values, []string{"applied"}) {
			t.Errorf("edge rule values = %v, want applied once per header block", values)
		}
	}
	if rec.preAuthTarget != "final-target" || !slices.Equal(cached.tags, []string{"final-tag"}) {
		t.Errorf("final control metadata lost: target=%q tags=%v", rec.preAuthTarget, cached.tags)
	}
}

type interimHeaderProbe struct {
	header   http.Header
	headers  []http.Header
	statuses []int
}

func (w *interimHeaderProbe) Header() http.Header { return w.header }
func (w *interimHeaderProbe) WriteHeader(status int) {
	w.headers = append(w.headers, w.header.Clone())
	w.statuses = append(w.statuses, status)
}
func (w *interimHeaderProbe) Write(p []byte) (int, error) { return len(p), nil }

func TestEarlyHintsDoNotBypassDurableUsage(t *testing.T) {
	for _, maxBytes := range []int64{1, 4096} {
		f := newUsageCommitFixture(t, http.MethodGet, maxBytes, true)
		f.rec.WriteHeader(http.StatusEarlyHints)
		f.rec.Header().Set("Content-Length", "4")
		f.rec.WriteHeader(http.StatusOK)
		n, err := f.rec.Write([]byte("body"))
		if maxBytes == 1 {
			if n != 0 || !errors.Is(err, errUsageNotDurable) || len(f.probe.body) != 0 {
				t.Errorf("completing write = %d, %v, body=%q; want withheld without durable usage", n, err, f.probe.body)
			}
		} else if n != 4 || err != nil || f.probe.pendingAtLast != 1 {
			t.Errorf("completing write = %d, %v, pending=%d; want durable usage before the body", n, err, f.probe.pendingAtLast)
		}
	}
}

func TestServiceUpgradeAfterEarlyHintsReportsHandshake(t *testing.T) {
	live := &interimHeaderProbe{header: make(http.Header)}
	var statuses []int
	handshakes := 0
	w := &serviceProxyUpgradeProbeWriter{ResponseWriter: live,
		onResponse:  func(status int) { statuses = append(statuses, status) },
		onHandshake: func() { handshakes++ },
	}
	w.WriteHeader(http.StatusEarlyHints)
	w.WriteHeader(http.StatusSwitchingProtocols)
	if handshakes != 1 || !w.handshake || !slices.Equal(statuses, []int{http.StatusSwitchingProtocols}) {
		t.Fatalf("handshakes=%d, handshake=%v, statuses=%v; want one 101 handshake", handshakes, w.handshake, statuses)
	}
}

// ADR-201 disallows replay once any response header has reached the client.
// Keeping informational and final state separate must not weaken that gate.
func TestEarlyHintsPreserveRetryAdmission(t *testing.T) {
	for _, service := range []bool{false, true} {
		t.Run(fmt.Sprintf("service=%v", service), func(t *testing.T) {
			live := &interimHeaderProbe{header: http.Header{"X-Platform": {"keep"}}}
			calls := 0
			origin := func(w http.ResponseWriter, r *http.Request, target Target) {
				calls++
				w.Header().Set("X-Interim", "hint")
				w.WriteHeader(http.StatusEarlyHints)
				clear(w.Header())
				deadTargetAttempt(w, r, target)
			}
			if service {
				f := newUndeliveredFixture(t, false)
				f.proxy.forward = func(target Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { origin(w, r, target) })
				}
				r := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/work", nil)
				r.Header.Set(ServiceProxyCallerAppHeader, "app-client")
				f.proxy.ServeHTTP(live, r)
				if f.wakes.Load() != 0 {
					t.Error("early hints allowed a second attempt that woke a sibling")
				}
			} else {
				obs := &recordingObserver{}
				runWithRetry(live, replayableRequest(http.MethodGet, ""), Target{}, testPolicy(), func(Target) {}, origin,
					func() (Target, bool) { return Target{}, true }, obs)
				if !slices.Equal(obs.exhausted, []string{RetrySkipCommitted}) {
					t.Errorf("retry exhaustion = %v, want response_committed", obs.exhausted)
				}
			}
			if calls != 1 || !slices.Equal(live.statuses, []int{http.StatusEarlyHints, http.StatusBadGateway}) {
				t.Fatalf("attempts=%d statuses=%v, want one attempt and 103 followed by 502", calls, live.statuses)
			}
			final := live.headers[len(live.headers)-1]
			if final.Get("X-Interim") != "" || final.Get("X-Platform") != "keep" {
				t.Errorf("final response did not preserve the platform header boundary: %v", final)
			}
		})
	}
}

func TestMirrorCaptureUsesFinalResponseAfterEarlyHints(t *testing.T) {
	for _, status := range []int{0, http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			w := newMirrorResponseCapture()
			w.Header().Set("X-Interim", "hint")
			w.WriteHeader(http.StatusEarlyHints)
			clear(w.Header())
			w.Header().Set("X-Final", "yes")
			if status != 0 {
				w.WriteHeader(status)
			}
			_, _ = w.Write([]byte("shadow body"))
			response := w.response()
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			wantStatus := status
			if wantStatus == 0 {
				wantStatus = http.StatusOK
			}
			if err != nil || response.StatusCode != wantStatus || string(body) != "shadow body" || response.Header.Get("X-Final") != "yes" || response.Header.Get("X-Interim") != "" {
				t.Fatalf("shadow response = %d %v %q, err=%v; want final %d", response.StatusCode, response.Header, body, err, wantStatus)
			}
		})
	}
}

func TestResponseLossAfterEarlyHints(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, status := range []int{http.StatusCreated, http.StatusServiceUnavailable} {
			t.Run(fmt.Sprintf("h2=%v/status=%d", h2, status), func(t *testing.T) {
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					rw := newScenarioHTTPResponseWriter(w)
					rw.Header().Set("X-Interim", "hint")
					rw.WriteHeader(http.StatusEarlyHints)
					clear(rw.Header())
					rw.Header().Set("X-Final", "yes")
					rw.WriteHeader(status)
					_, _ = rw.Write([]byte("response body"))
					rw.finish(r.Context(), func() { w.Header().Set("X-Injected", "yes") })
				}))
				server.EnableHTTP2 = h2
				if h2 {
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
				if response.StatusCode != status || response.Header.Get("X-Interim") != "" {
					t.Errorf("final response = %d %v, want %d without interim headers", response.StatusCode, response.Header, status)
				}
				if status == http.StatusCreated {
					if err == nil || len(body) != 0 || response.Header.Get("X-Injected") != "yes" {
						t.Errorf("response loss was not injected: body=%q err=%v headers=%v", body, err, response.Header)
					}
				} else if err != nil || string(body) != "response body" || response.Header.Get("X-Final") != "yes" || response.Header.Get("X-Injected") != "" {
					t.Errorf("upstream error was not preserved: body=%q err=%v headers=%v", body, err, response.Header)
				}
			})
		}
	}
}
