// adr: 375
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/circuit"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func decisionSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	previous := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})
	return recorder
}

func assertDecisionAttributes(t *testing.T, span sdktrace.ReadOnlySpan, attempts int64, fields map[string]string) {
	t.Helper()
	fields["decision_version"] = "v1"
	for key, want := range fields {
		if got := spanAttribute(span.Attributes(), "gregale.traffic."+key); got != attribute.StringValue(want) {
			t.Errorf("%s = %v, want %q", key, got, want)
		}
	}
	for key, want := range map[string]int64{"forward_attempts": attempts, "replays": max(0, attempts-1)} {
		if got := spanAttribute(span.Attributes(), "gregale.traffic."+key); got != attribute.Int64Value(want) {
			t.Errorf("%s = %v, want %d", key, got, want)
		}
	}
}

func TestTrafficDecisionRecordBoundsAndSealing(t *testing.T) {
	ctx := withTrafficDecision(t.Context(), false)
	private := strings.Repeat("private-credential:/customer-path?token=secret", 1000)
	recordTrafficRetryStop(ctx, private)
	recordTrafficLimiter(ctx, private)
	recordTrafficCache(ctx, private)
	recordTrafficRefusal(ctx, private)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 1000 {
				recordTrafficAttempt(ctx)
				stop := measureTrafficPhase(ctx, trafficBody)
				stop()
				stop()
			}
		})
	}
	workers.Wait()
	if observed := trafficDecisionEvidence(ctx, http.StatusServiceUnavailable, false); observed.attempts != 8000 {
		t.Fatalf("concurrent dispatch observations lost: %d", observed.attempts)
	}
	mutateTrafficDecision(ctx, func(value *trafficDecisionSnapshot) {
		value.attempts = math.MaxInt64
		value.durations[trafficPolicy] = math.MaxInt64
	})
	recordTrafficAttempt(ctx)
	measureTrafficPhase(ctx, trafficPolicy)()
	evidence := trafficDecisionEvidence(ctx, http.StatusServiceUnavailable, true)
	if evidence.attempts != math.MaxInt64 || evidence.durations[trafficPolicy] != math.MaxInt64 || evidence.durations[trafficBody] < 0 {
		t.Fatalf("bounded counters overflowed: %+v", evidence)
	}
	if evidence.measuredPhases() != "policy,body" || len(evidence.attributes()) != 17 {
		t.Fatalf("unbounded or missing measurements: %+v", evidence)
	}
	for key, want := range map[string]string{"retry_stop": "other", "limiter_scope": "other", "cache_outcome": "other", "rejection_reason": "other"} {
		if got := spanAttribute(evidence.attributes(), "gregale.traffic."+key); got != attribute.StringValue(want) {
			t.Errorf("unknown %s leaked: %v", key, got)
		}
	}
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Info("decision", evidence.logAttribute())
	if strings.Contains(output.String(), "private") || output.Len() > 2000 {
		t.Fatalf("record leaked or grew with input: %d bytes", output.Len())
	}
	recordTrafficCache(ctx, "hit")
	recordTrafficRefusal(ctx, "deadline")
	measureTrafficPhase(ctx, trafficWake)()
	if got := trafficDecisionEvidence(ctx, http.StatusOK, true); got != evidence {
		t.Fatalf("sealed evidence changed: %+v", got)
	}
	child := withTrafficDecision(ctx, true)
	recordTrafficAttempt(child)
	if got := trafficDecisionEvidence(child, http.StatusOK, true); got.path != "managed_service" || got.attempts != 1 {
		t.Fatalf("child reused parent's record: %+v", got)
	}
	if got := trafficDecisionEvidence(withoutTrafficDecision(ctx), http.StatusOK, true); got.path != "" {
		t.Fatalf("detached background inherited a record: %+v", got)
	}
}

func TestPublicTrafficDecisionEvidence(t *testing.T) {
	for _, kind := range []string{"guest-401", "authentication", "unknown-owner", "policy-outage", "account-outage", "app-outage", "tenant-outage", "cache-hit", "cache-404", "preview-404", "account-limited", "app-limited", "tenant-limited", "rule-limited", "consumer-limited", "surface-limited"} {
		t.Run(kind, func(t *testing.T) {
			spans := decisionSpanRecorder(t)
			h, backend, _ := newTestHandler(t)
			var logs bytes.Buffer
			h.log = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			var forwards atomic.Int64
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					forwards.Add(1)
					w.WriteHeader(http.StatusUnauthorized)
				})
			})
			url := "http://" + backend.host + "/catalog"
			wantStatus, attempts := http.StatusUnauthorized, int64(0)
			fields := map[string]string{"path": "public_http", "outcome": "refused", "cache_outcome": "not_consulted", "circuit_verdict": "not_observed"}
			var central *fakeCentral
			switch kind {
			case "guest-401":
				backend.setLegacyHot()
				attempts, fields["outcome"] = 1, "upstream_response"
				fields["rejection_reason"] = ""
			case "authentication":
				backend.app.ConsumerAuthMode = api.ConsumerAuthModeRequired
				fields["phase"], fields["rejection_reason"] = "authentication", "authentication_response"
			case "unknown-owner":
				url, wantStatus = "http://unclaimed.example/catalog", http.StatusNotFound
				fields["phase"], fields["rejection_reason"] = "ownership", "ownership_response"
			case "policy-outage":
				h.WithEdgeRules(snapshotTestMatcher{err: errors.New("private store failure")}, nil, nil)
				wantStatus, fields["rejection_reason"] = http.StatusServiceUnavailable, "policy_unavailable"
			case "account-outage", "app-outage":
				central = newFakeCentral()
				central.consumeResult = func() (int, bool, error) { return 0, false, errors.New("private counter failure") }
				h.WithCentralBackend(central)
				fields["limiter_scope"] = strings.TrimSuffix(kind, "-outage")
				if kind == "app-outage" {
					h.accountLimiter = h.accountLimiter.WithNoop()
				}
				wantStatus, fields["rejection_reason"] = http.StatusServiceUnavailable, "rate_limit_unavailable"
			case "account-limited", "app-limited":
				central = newFakeCentral()
				central.consumeResult = func() (int, bool, error) { return 0, false, nil }
				h.WithCentralBackend(central)
				fields["limiter_scope"] = strings.TrimSuffix(kind, "-limited")
				if kind == "app-limited" {
					h.accountLimiter = h.accountLimiter.WithNoop()
				}
				wantStatus, fields["rejection_reason"] = http.StatusTooManyRequests, "rate_limited"
			case "rule-limited", "consumer-limited":
				central = newFakeCentral()
				central.consumeResult = func() (int, bool, error) { return 0, false, nil }
				h.WithCentralBackend(central)
				rule := &EdgeRuleThrottleResolved{ID: "rule", AccountID: backend.app.AccountID, AppID: backend.app.ID, RequestsPerSecond: 1, Burst: 1}
				if kind == "consumer-limited" {
					rule.KeyBy, rule.MissingKeyPolicy = api.ThrottleKeyByAPIKey, api.ThrottleMissingKeyShared
				}
				h.edgeRules = stubEdgeRuleMatcher{throttle: rule}
				fields["limiter_scope"] = strings.TrimSuffix(kind, "-limited")
				wantStatus, fields["rejection_reason"] = http.StatusTooManyRequests, "rate_limited"
			case "tenant-limited":
				backend.app.PlatformTenantID, backend.app.RoutedSurfaceID = "tenant", "surface"
				store := &testTenantBudgetStore{}
				store.calls.Store(1)
				h.WithTenantRequestBudgetStore(store)
				wantStatus, fields["rejection_reason"], fields["limiter_scope"] = http.StatusTooManyRequests, "rate_limited", "tenant"
			case "surface-limited":
				backend.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 1, Burst: 1}
				h.preAuthLimiter.Allow(backend.app.ID, preAuthSourceKey(net.ParseIP("203.0.113.10")), 1, 1)
				wantStatus, fields["rejection_reason"], fields["limiter_scope"] = http.StatusTooManyRequests, "rate_limited", "surface"
			case "tenant-outage":
				backend.app.PlatformTenantID, backend.app.RoutedSurfaceID = "tenant", "surface"
				store := &testTenantBudgetStore{}
				store.fail.Store(true)
				h.WithTenantRequestBudgetStore(store)
				wantStatus, fields["rejection_reason"], fields["limiter_scope"] = http.StatusServiceUnavailable, "rate_limit_unavailable", "tenant"
			case "preview-404":
				backend.app.IsPreview = true
				h.edgeRules = &respondOnlyMatcher{host: backend.host, rule: EdgeRuleRespondResolved{
					ID: "fixed", AccountID: backend.app.AccountID, AppID: backend.app.ID, StatusCode: http.StatusNotFound, Body: []byte(`{"error":"missing"}`),
				}}
				wantStatus, fields["outcome"], fields["phase"], fields["rejection_reason"] = http.StatusNotFound, "edge_response", "response", ""
			case "cache-hit", "cache-404":
				central = newFakeCentral()
				central.consumeResult = func() (int, bool, error) { return 8, true, nil }
				h.WithCentralBackend(central).WithResponseCache(NewResponseCache())
				rule := EdgeRuleCacheResolved{ID: "cached", AccountID: backend.app.AccountID, AppID: backend.app.ID, MaxAgeSeconds: 60}
				seedCacheRule(t, h, backend.host, rule)
				key := CacheKey{AppID: backend.app.ID, RuleID: rule.ID, Method: http.MethodGet, NormalizedPath: "/catalog", VaryHash: hostVaryHash(backend.host)}
				wantStatus = http.StatusOK
				if kind == "cache-404" {
					wantStatus = http.StatusNotFound
				}
				h.responseCache.Put(key, wantStatus, http.Header{}, []byte("cached"), time.Now().Add(time.Minute), time.Now().Add(time.Minute), rule.toStateEdgeRuleCacheAction())
				fields["outcome"], fields["cache_outcome"], fields["rejection_reason"] = "edge_response", "hit", ""
			}
			rec := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, url, nil)
			request.Header.Set("X-Forwarded-For", "203.0.113.10")
			h.ServeHTTP(rec, request)
			if rec.Code != wantStatus || forwards.Load() != attempts || (attempts == 0 && backend.admits != 0) {
				t.Fatalf("status=%d forwards=%d wakes=%d body=%s", rec.Code, forwards.Load(), backend.admits, rec.Body)
			}
			span := findEndedSpan(t, spans.Ended(), "gateway.request")
			assertDecisionAttributes(t, span, attempts, fields)
			if strings.HasPrefix(kind, "cache-") && central.consumeCalls.Load() != 2 {
				t.Fatalf("cache hit lost account/app admission: %d", central.consumeCalls.Load())
			}
			assertDecisionLogMatchesSpan(t, logs.Bytes(), span)
		})
	}
}

func assertDecisionLogMatchesSpan(t *testing.T, logs []byte, span sdktrace.ReadOnlySpan) {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(logs), []byte("\n")) {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err)
		}
		if row["traffic_decision"] == nil {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(row["traffic_decision"], &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 17 {
			t.Fatalf("decision log field count = %d", len(fields))
		}
		for key, value := range fields {
			attr := spanAttribute(span.Attributes(), "gregale.traffic."+key)
			if attr.Type() == attribute.INT64 {
				if value != float64(attr.AsInt64()) {
					t.Errorf("log %s=%v differs from span=%v", key, value, attr)
				}
			} else if value != attr.AsString() {
				t.Errorf("log %s=%v differs from span=%v", key, value, attr)
			}
		}
		return
	}
	t.Fatal("public request log omitted its decision record")
}

func TestPublicTrafficDecisionRetries(t *testing.T) {
	for _, kind := range []string{"retry", "disabled", "post", "aggregate-outage"} {
		t.Run(kind, func(t *testing.T) {
			spans := decisionSpanRecorder(t)
			h, _, forwards := retryTestHandler(t)
			h.WithRetryEnabled(kind != "disabled").WithRetryDefault(RetryPolicy{MaxAttempts: 2, Backoff: 2 * time.Millisecond})
			method, attempts, stop := http.MethodGet, int64(1), ""
			if kind == "post" {
				method, stop = http.MethodPost, RetrySkipNonIdempotent
			} else if kind == "retry" {
				attempts = 2
			} else if kind == "aggregate-outage" {
				budget, err := NewSharedRetryBudget(&failingOriginalBudget{})
				if err != nil {
					t.Fatal(err)
				}
				h.WithRetryBudget(budget)
				stop = RetrySkipAggregate
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, "http://jane-api.apps.dom/charge", http.NoBody))
			if int64(forwards.Load()) != attempts || (attempts == 2 && rec.Code != http.StatusOK) {
				t.Fatalf("forward count=%d status=%d", forwards.Load(), rec.Code)
			}
			span := findEndedSpan(t, spans.Ended(), "gateway.request")
			assertDecisionAttributes(t, span, attempts, map[string]string{"outcome": "upstream_response", "retry_stop": stop})
			if attempts == 2 && spanAttribute(span.Attributes(), "gregale.traffic.retry_backoff_ms").AsInt64() < 1 {
				t.Fatal("actual retry backoff omitted")
			}
		})
	}
}

func TestManagedServiceTrafficDecisionEvidence(t *testing.T) {
	for _, kind := range []string{"guest-401", "retry", "post", "policy-outage", "authorization", "revoked", "circuit"} {
		t.Run(kind, func(t *testing.T) {
			spans := decisionSpanRecorder(t)
			parent := withTrafficDecision(t.Context(), false)
			recordTrafficAttempt(parent)
			parentRecord := trafficDecisionFrom(parent)
			provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "target", Endpoints: []ServiceEndpoint{
				{InstanceID: "a", NodeID: "node", Port: 8080, DeploymentID: "dep"}, {InstanceID: "b", NodeID: "node", Port: 8081, DeploymentID: "dep"},
			}}}
			cfg := ServiceProxyConfig{Provider: provider, RetryPolicy: RetryPolicy{Enabled: true, MaxAttempts: 2, Backoff: 2 * time.Millisecond},
				Policy: func(context.Context, string, string, bool) (ServicePolicySnapshot, error) {
					return ServicePolicySnapshot{InputRevision: "revision", Found: true, Target: ServiceTarget{AppID: "target"}, Caller: ServiceCaller{AppID: "caller", AccountID: "account"},
						Routing: &ServiceRoutingSnapshot{Weights: []DeploymentWeightsRow{{ID: "dep", TrafficPercent: 100}}}}, nil
				},
			}
			var forwards atomic.Int64
			cfg.Forward = func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					call := forwards.Add(1)
					if trafficDecisionFrom(r.Context()) == parentRecord {
						t.Error("managed child shares its parent's decision")
					}
					if (kind == "retry" || kind == "post") && call == 1 {
						markStaleTarget(r.Context())
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					w.WriteHeader(http.StatusUnauthorized)
				})
			}
			method, status, attempts := http.MethodGet, http.StatusUnauthorized, int64(1)
			fields := map[string]string{"path": "managed_service", "outcome": "upstream_response", "cache_outcome": "not_consulted", "circuit_verdict": "admitted", "rejection_reason": ""}
			switch kind {
			case "retry":
				attempts = 2
			case "post":
				method, status, fields["retry_stop"] = http.MethodPost, http.StatusServiceUnavailable, RetrySkipNonIdempotent
			case "policy-outage":
				cfg.Policy = func(context.Context, string, string, bool) (ServicePolicySnapshot, error) {
					return ServicePolicySnapshot{}, errors.New("private failure")
				}
				status, attempts, fields["outcome"], fields["rejection_reason"], fields["circuit_verdict"] = http.StatusServiceUnavailable, 0, "refused", "policy_unavailable", "not_observed"
			case "authorization":
				pin := cfg.Policy
				cfg.Policy = func(ctx context.Context, caller, service string, alias bool) (ServicePolicySnapshot, error) {
					snapshot, err := pin(ctx, caller, service, alias)
					snapshot.AuthorizationError = ErrServiceProxyBindingDenied
					return snapshot, err
				}
				status, attempts, fields["outcome"], fields["rejection_reason"], fields["circuit_verdict"] = http.StatusForbidden, 0, "refused", "authentication_response", "not_observed"
			case "revoked":
				store := &gatewaySecurityStore{}
				store.set(trafficrevocation.Scope{Kind: "app", ID: "caller"}, 1, true)
				cfg.TrafficRevocations = trafficrevocation.New(store)
				t.Cleanup(cfg.TrafficRevocations.Close)
				status, attempts, fields["outcome"], fields["rejection_reason"], fields["circuit_verdict"] = http.StatusForbidden, 0, "refused", "security_revoked", "not_observed"
			case "circuit":
				cfg.Breaker = circuit.NewGroup(circuit.LegacyQuarantineConfig(), time.Now)
				for _, endpoint := range provider.snapshot.Endpoints {
					cfg.Breaker.Failure(serviceProxyEndpointKey("target", endpoint.InstanceID))
				}
				status, attempts, fields["outcome"], fields["rejection_reason"], fields["circuit_verdict"] = http.StatusServiceUnavailable, 0, "refused", "circuit", "refused"
			}
			request := httptest.NewRequest(method, "http://gateway/v1/internal/services/orders/customer?token=private", nil).WithContext(parent)
			request.Header.Set(ServiceProxyCallerAppHeader, "caller")
			request.Header.Set("Authorization", "Bearer private-credential")
			rec := httptest.NewRecorder()
			NewServiceProxy(cfg).ServeHTTP(rec, request)
			if rec.Code != status || forwards.Load() != attempts {
				t.Fatalf("status=%d attempts=%d body=%s", rec.Code, forwards.Load(), rec.Body)
			}
			span := findEndedSpan(t, spans.Ended(), "service.orders")
			assertDecisionAttributes(t, span, attempts, fields)
			for _, attr := range span.Attributes() {
				if strings.HasPrefix(string(attr.Key), "gregale.traffic.") && strings.Contains(attr.Value.AsString(), "private") {
					t.Errorf("private request text entered decision: %v", attr)
				}
			}
			if got := trafficDecisionEvidence(parent, http.StatusOK, false); got.attempts != 1 || got.path != "public_http" {
				t.Fatalf("child changed parent: %+v", got)
			}
		})
	}
}

func TestTrafficDecisionDetachedCacheRefreshOwnsNoRecord(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.setLegacyHot()
	h.WithResponseCache(NewResponseCache())
	parent := withTrafficDecision(t.Context(), false)
	recordTrafficCache(parent, "hit")
	completed := make(chan *trafficDecision, 1)
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recordTrafficAttempt(r.Context())
			measureTrafficPhase(r.Context(), trafficWake)()
			recordTrafficCache(r.Context(), "miss")
			completed <- trafficDecisionFrom(r.Context())
			w.WriteHeader(http.StatusNoContent)
		})
	})
	rule := &EdgeRuleCacheResolved{ID: "rule", MaxAgeSeconds: 60}
	request := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil).WithContext(parent)
	h.startCacheRefresh(request, backend.app, rule, CacheKey{AppID: backend.app.ID, RuleID: rule.ID, Method: http.MethodGet, NormalizedPath: "/"})
	select {
	case record := <-completed:
		if record != nil {
			t.Fatal("detached refresh retained the live parent record")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not reach forwarding")
	}
	if evidence := trafficDecisionEvidence(parent, http.StatusOK, true); evidence.attempts != 0 || evidence.cache != "hit" || evidence.measured != 0 {
		t.Fatalf("background work revised parent before sealing: %+v", evidence)
	}
}

func TestTrafficDecisionMeasurementsCoalesceNestedWork(t *testing.T) {
	ctx := withTrafficDecision(t.Context(), true)
	started := time.Now()
	outer := measureTrafficPhase(ctx, trafficWake)
	inner := measureTrafficPhase(ctx, trafficWake)
	<-time.After(2 * time.Millisecond)
	inner()
	outer()
	elapsed := time.Since(started)
	evidence := trafficDecisionEvidence(ctx, http.StatusOK, true)
	if measured := time.Duration(evidence.durations[trafficWake]); measured < time.Millisecond || measured > elapsed {
		t.Fatalf("nested work counted twice: measured=%v elapsed=%v", measured, elapsed)
	}
}

func TestTrafficDecisionRawUpgradeDispatchesOnce(t *testing.T) {
	for _, service := range []bool{false, true} {
		name := "public_http"
		if service {
			name = "managed_service"
		}
		t.Run(name, func(t *testing.T) {
			spans := decisionSpanRecorder(t)
			var dispatches atomic.Int64
			var plainCalls atomic.Int32
			raw := func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					dispatches.Add(1)
					if evidence := trafficDecisionEvidence(r.Context(), http.StatusSwitchingProtocols, false); evidence.attempts != 1 {
						t.Errorf("prepared raw request lost its owner's decision: %+v", evidence)
					}
					w.WriteHeader(http.StatusSwitchingProtocols)
				})
			}
			var handler http.Handler
			var request *http.Request
			spanName := "gateway.request"
			if service {
				handler = newProtocolTestProxy(ServiceTarget{AppID: "app-orders", WebSocketEnabled: true}, okForwarder(nil, &plainCalls), raw)
				request, spanName = upgradeRequest(), "service.orders"
			} else {
				h, backend, _ := newTestHandler(t)
				backend.setLegacyHot()
				backend.app.WebSocketEnabled = true
				handler = h.WithRawForwarding(raw)
				request = httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/socket", nil)
				request.Header.Set("Connection", "Upgrade")
				request.Header.Set("Upgrade", "websocket")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, request)
			if rec.Code != http.StatusSwitchingProtocols || dispatches.Load() != 1 || plainCalls.Load() != 0 {
				t.Fatalf("upgrade status=%d raw dispatches=%d ordinary dispatches=%d", rec.Code, dispatches.Load(), plainCalls.Load())
			}
			assertDecisionAttributes(t, findEndedSpan(t, spans.Ended(), spanName), 1,
				map[string]string{"path": name, "outcome": "upstream_response", "retry_stop": ""})
		})
	}
}

func TestTrafficDecisionUploadAndCapacityCancellation(t *testing.T) {
	t.Run("upload", func(t *testing.T) {
		spans := decisionSpanRecorder(t)
		h, backend, _ := newTestHandler(t)
		setTotalBudget(h, backend.app, 30)
		reader, writer := io.Pipe()
		defer writer.Close()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://"+backend.host+"/", reader))
		assertTotalTimeout(t, rec)
		span := findEndedSpan(t, spans.Ended(), "gateway.request")
		assertDecisionAttributes(t, span, 0, map[string]string{"phase": "body", "outcome": "deadline", "rejection_reason": "deadline"})
		if spanAttribute(span.Attributes(), "gregale.traffic.body_admission_ms").AsInt64() < 1 || backend.admits != 0 {
			t.Fatal("upload wait missing or expired body reached wake")
		}
	})
	t.Run("wake", func(t *testing.T) {
		spans := decisionSpanRecorder(t)
		base := &fakeBackend{app: App{ID: "cold", AccountID: "owner", Plan: api.PlanPro, Type: AppTypeFunction}, host: "cold.example.test"}
		backend := &delayedAdmitBackend{fakeBackend: base, delay: 150 * time.Millisecond}
		h := NewHandlerWith(backend, NewMetrics(), nil)
		setTotalBudget(h, base.app, 50)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+base.host+"/", nil))
		assertTotalTimeout(t, rec)
		span := findEndedSpan(t, spans.Ended(), "gateway.request")
		assertDecisionAttributes(t, span, 0, map[string]string{"phase": "wake", "outcome": "deadline", "rejection_reason": "deadline"})
		if spanAttribute(span.Attributes(), "gregale.traffic.wake_admission_ms").AsInt64() < 1 {
			t.Fatal("actual cold-wake wait omitted")
		}
	})
	t.Run("capacity", func(t *testing.T) {
		h, backend, _ := newTestHandler(t)
		backend.setLegacyHot()
		pick := backend.Pick(backend.app.ID)
		release, ok := h.vmConcurrency.tryAcquire(pick.Target.InstanceID, string(backend.app.Plan), 1)
		if !ok {
			t.Fatal("could not occupy target")
		}
		defer release()
		ctx, cancel := context.WithTimeout(withTrafficDecision(t.Context(), false), 20*time.Millisecond)
		defer cancel()
		_, acquired, waited, err := h.acquireVMTarget(ctx, backend.app, pick, 1, "", "")
		if !errors.Is(err, ErrConcurrencyQueueWaitTimeout) || acquired != nil || !waited {
			t.Fatalf("wait outcome=%v/%v", waited, err)
		}
		evidence := trafficDecisionEvidence(ctx, http.StatusGatewayTimeout, true)
		if evidence.outcome != "deadline" || evidence.phase != trafficCapacity || evidence.attempts != 0 || evidence.durations[trafficCapacity] < int64(time.Millisecond) {
			t.Fatalf("capacity decision=%+v", evidence)
		}
		h.vmConcurrency.queueMu.Lock()
		defer h.vmConcurrency.queueMu.Unlock()
		if len(h.vmConcurrency.queues) != 0 {
			t.Fatal("canceled capacity wait leaked its queue")
		}
	})
}

func TestTrafficDecisionDetachedStreamKeepsLifetimeReason(t *testing.T) {
	for _, reason := range []error{nil, context.Canceled, trafficrevocation.ErrRevoked, trafficrevocation.ErrUnavailable} {
		t.Run(fmtDecisionCause(reason), func(t *testing.T) {
			base := withTrafficDecision(t.Context(), false)
			budget, cancelBudget, _ := reqbudget.WithRemaining(base, time.Hour, time.Hour, "forward", "")
			defer cancelBudget()
			fenced, cancelLifetime := reqbudget.WithCancellationFence(budget)
			defer cancelLifetime(nil)
			stream, detach, cancelStream := reqbudget.WithStream(fenced)
			defer cancelStream()
			recordTrafficAttempt(fenced)
			detach()
			recordTrafficStreamDetached(fenced)
			cancelBudget()
			if reason != nil {
				cancelLifetime(reason)
				select {
				case <-stream.Done():
				case <-time.After(time.Second):
					t.Fatal("detached stream lost lifetime cancellation")
				}
			}
			evidence := trafficDecisionEvidence(fenced, http.StatusOK, true)
			want := "upstream_response"
			if errors.Is(reason, context.Canceled) {
				want = "canceled"
			} else if reason != nil {
				want = "refused"
				if evidence.refusal != trafficSecurityDecision(reason) {
					t.Fatalf("lost security cause after headers: %+v", evidence)
				}
			}
			if evidence.outcome != want {
				t.Fatalf("stream evidence=%+v, want %s", evidence, want)
			}
		})
	}
}

func fmtDecisionCause(err error) string {
	if err == nil {
		return "successful-stream"
	}
	return err.Error()
}
