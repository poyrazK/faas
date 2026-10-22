// adr: 1202 — scoped response loss preserves post-commit ambiguity and recovery.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestServiceProxyResponseLostAfterCommit(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%t", h2), func(t *testing.T) {
			var mu sync.Mutex
			ledger := map[string]string{}
			var attempts, matches atomic.Int32
			metrics := NewMetrics()
			active := atomic.Bool{}
			active.Store(true)
			proxy := NewServiceProxy(ServiceProxyConfig{
				Metrics:  metrics,
				Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-payments", Endpoints: []ServiceEndpoint{{InstanceID: "instance-payments", NodeID: "node-a", Port: 8080}}}},
				Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
					return ServiceTarget{AppID: "app-payments", ScenarioTestRunID: "write-run"}, true, nil
				},
				Authorize: func(context.Context, string, string) (ServiceCaller, error) {
					return ServiceCaller{AppID: "app-worker"}, nil
				},
				ResolveChaos: func(context.Context, string, string, string) (chaos.Lease, error) {
					if !active.Load() {
						return chaos.Lease{}, nil
					}
					return chaos.Lease{Generation: "write-generation", ExpiresAt: time.Now().Add(time.Minute), Rules: []chaos.Rule{{To: "payments", Kind: chaos.KindHTTPResponseLost, Percent: 100}}}, nil
				},
				ObserveChaosMatch: func(run, caller, generation string, rule chaos.Rule) {
					if run != "write-run" || caller != "app-worker" || generation != "write-generation" || rule.Kind != chaos.KindHTTPResponseLost {
						t.Error("lost-response evidence omitted scope")
					}
					matches.Add(1)
				},
				Forward: func(Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method == http.MethodHead {
							w.WriteHeader(200)
							return
						}
						attempts.Add(1)
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
							return
						}
						key := r.Header.Get("Idempotency-Key")
						mu.Lock()
						defer mu.Unlock()
						if prior, ok := ledger[key]; ok && prior != string(body) {
							w.Header().Set("X-Conflict", "preserved")
							w.WriteHeader(409)
							_, _ = io.WriteString(w, `{"error":"idempotency_conflict"}`)
							return
						}
						ledger[key] = string(body) // Commit happens before any acknowledgement.
						w.Header().Set("Set-Cookie", "upstream-ack=secret")
						w.Header().Set("Trailer", "X-Result")
						w.WriteHeader(201)
						_, _ = io.WriteString(w, `{"id":"order-one","confirmed":true}`)
						if f, ok := w.(http.Flusher); ok {
							f.Flush()
						}
						w.Header().Set("X-Result", "confirmed")
					})
				},
			})
			server := httptest.NewUnstartedServer(proxy)
			server.EnableHTTP2 = h2
			if h2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			type responseSnapshot struct {
				StatusCode, ProtoMajor int
				Header, Trailer        http.Header
			}
			call := func(payload string) (responseSnapshot, []byte, error) {
				t.Helper()
				request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/internal/services/payments/charge", strings.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set(ServiceProxyCallerAppHeader, "app-worker")
				request.Header.Set("Idempotency-Key", "order-one")
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				return responseSnapshot{StatusCode: response.StatusCode, ProtoMajor: response.ProtoMajor, Header: response.Header.Clone(), Trailer: response.Trailer.Clone()}, body, readErr
			}
			for i := 0; i < 3; i++ {
				response, body, err := call(`{"quantity":1}`)
				if response.StatusCode != 201 || response.Header.Get("X-Gregale-Chaos-Injected") != chaos.KindHTTPResponseLost || response.Header.Get("Set-Cookie") != "" || err == nil || len(body) != 0 || (h2 && response.ProtoMajor != 2) {
					t.Fatalf("reply was not lost after commit: status=%d body=%q err=%v headers=%v", response.StatusCode, body, err, response.Header)
				}
			}
			if attempts.Load() != 3 || matches.Load() != 3 || len(ledger) != 1 || ledger["order-one"] != `{"quantity":1}` {
				t.Fatal("hidden retry, missing commit or duplicate write")
			}
			response, body, err := call(`{"quantity":2}`)
			if err != nil || response.StatusCode != 409 || response.Header.Get("X-Conflict") != "preserved" || response.Header.Get("X-Gregale-Chaos-Injected") != "" || !json.Valid(body) || matches.Load() != 3 {
				t.Fatal("conflict masked by response-loss fault")
			}
			head, err := http.NewRequest(http.MethodHead, server.URL+"/v1/internal/services/payments/charge", nil)
			if err != nil {
				t.Fatal(err)
			}
			head.Header.Set(ServiceProxyCallerAppHeader, "app-worker")
			reply, err := server.Client().Do(head)
			if err != nil {
				t.Fatal(err)
			}
			_ = reply.Body.Close()
			if reply.Header.Get("X-Gregale-Chaos-Injected") != "" || matches.Load() != 3 || attempts.Load() != 4 {
				t.Fatal("HEAD injected a body loss")
			}
			active.Store(false)
			response, body, err = call(`{"quantity":1}`)
			if err != nil || response.StatusCode != 201 || !json.Valid(body) || response.Trailer.Get("X-Result") != "confirmed" || len(ledger) != 1 || matches.Load() != 3 {
				t.Fatalf("recovery lost original reply or trailers: %q %v %v", body, response.Trailer, err)
			}
			if got := labelledCounterValue(t, metrics.Registry(), "gateway_service_chaos_injected_total", "kind", chaos.KindHTTPResponseLost); got != 3 {
				t.Fatalf("loss metric=%v", got)
			}
		})
	}
}

func TestResponseLostWriterPassThroughAndCancellation(t *testing.T) {
	for _, status := range []int{204, 205, 400, 409, 500} {
		dst := httptest.NewRecorder()
		writer := newScenarioHTTPResponseWriter(dst)
		writer.Header().Set("X-Upstream", "original")
		writer.WriteHeader(status)
		writer.Flush()
		writer.Header().Set(http.TrailerPrefix+"X-Result", "late")
		injected := false
		writer.finish(context.Background(), func() { injected = true })
		if injected || dst.Code != status || dst.Header().Get("X-Upstream") != "original" || dst.Header().Get(http.TrailerPrefix+"X-Result") != "late" {
			t.Fatal("non-eligible response was altered")
		}
	}
	dst := httptest.NewRecorder()
	writer := newScenarioHTTPResponseWriter(dst)
	writer.Header().Set("Content-Length", "0")
	writer.WriteHeader(200)
	writer.finish(context.Background(), func() { t.Fatal("empty reply counted as loss") })
	if dst.Header().Get("Content-Length") != "0" {
		t.Fatal("empty reply changed")
	}
	writer = newScenarioHTTPResponseWriter(httptest.NewRecorder())
	_, _ = writer.Write([]byte("committed"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writer.finish(ctx, func() { t.Fatal("cancellation counted as injection") })
}
