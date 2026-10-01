package faas

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testFlagCustomer = "00000000-0000-0000-0000-000000000001"
	testFlagApp      = "11111111-1111-4111-8111-111111111111"
	testFlagEnv      = "22222222-2222-4222-8222-222222222222"
)

func TestFlagAllocationMatchesSharedRuntimeVectors(t *testing.T) {
	for _, test := range []struct {
		file string
		fn   func(string, string, string) int
	}{
		{file: "../../pkg/flags/testdata/allocation.json", fn: flagBucket},
		{file: "../../pkg/flags/testdata/variant_allocation.json", fn: flagVariantBucket},
	} {
		raw, err := os.ReadFile(test.file)
		if err != nil {
			t.Fatal(err)
		}
		var vectors []struct {
			Seed, Key, Customer string
			Bucket              int
		}
		if err := json.Unmarshal(raw, &vectors); err != nil {
			t.Fatal(err)
		}
		for _, vector := range vectors {
			if got := test.fn(vector.Seed, vector.Key, vector.Customer); got != vector.Bucket {
				t.Errorf("%s/%s/%s: bucket %d, want %d", test.file, vector.Seed, vector.Customer, got, vector.Bucket)
			}
		}
	}
}

func TestRuntimeBundleEvaluatesOrderedCustomerAndVariantRules(t *testing.T) {
	const raw = `{
	  "environment_id":"22222222-2222-4222-8222-222222222222",
	  "version":7,
	  "groups":{"internal":["00000000-0000-0000-0000-000000000001"]},
	  "flags":[
	    {"key":"new-export","enabled":true,"default":false,"seed":"seed","rules":[
	      {"id":"selected","customers":["00000000-0000-0000-0000-000000000001"],"value":true},
	      {"id":"internal","group":"internal","value":false}
	    ]},
	    {"key":"pipeline","type":"variant","enabled":true,"default":"legacy","seed":"stable","variants":[
	      {"key":"legacy","weight":9000},{"key":"new","weight":1000}
	    ],"rules":[{"id":"experiment","rollout":10000}]}
	  ]
	}`
	bundle, err := validateRuntimeBundle([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	decision := evaluateRuntimeBoolean(bundle, "new-export", testFlagCustomer, false)
	if decision.Value != true || decision.ConfigVersion != 7 || decision.RuleID != "selected" || decision.Reason != "rule_match" || decision.Source != "configuration" {
		t.Fatalf("unexpected customer decision: %+v", decision)
	}
	decision = evaluateRuntimeBoolean(bundle, "new-export", "00000000-0000-0000-0000-000000000002", true)
	if decision.Value != false || decision.Reason != "default" {
		t.Fatalf("untargeted customer did not receive the default: %+v", decision)
	}
	decision = evaluateRuntimeVariant(bundle, "pipeline", testFlagCustomer, "legacy")
	if decision.Type != "variant" || decision.Value != "new" && decision.Value != "legacy" || decision.RuleID != "experiment" || decision.Bucket == nil || *decision.Bucket != flagVariantBucket("stable", "pipeline", testFlagCustomer) || decision.RolloutBucket == nil || *decision.RolloutBucket != flagBucket("stable", "pipeline", testFlagCustomer) {
		t.Fatalf("unexpected variant decision: %+v", decision)
	}
	decision = evaluateRuntimeBoolean(bundle, "missing", testFlagCustomer, true)
	if decision.Value != true || decision.Reason != "flag_missing" || decision.Source != "fallback" {
		t.Fatalf("unexpected missing-flag fallback: %+v", decision)
	}
}

func TestRuntimeBundleRejectsMalformedConfiguration(t *testing.T) {
	base := `{"environment_id":"22222222-2222-4222-8222-222222222222","version":1,"groups":{},"flags":[{"key":"export","enabled":true,"default":false,"seed":"seed","rules":[]}]}`
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "invalid key", raw: strings.Replace(base, `"export"`, `"Bad"`, 1)},
		{name: "missing stable seed", raw: strings.Replace(base, `"seed":"seed"`, `"seed":""`, 1)},
		{name: "unknown rule group", raw: strings.Replace(base, `"rules":[]`, `"rules":[{"id":"internal","group":"missing","value":true}]`, 1)},
		{name: "null group members", raw: strings.Replace(base, `"groups":{}`, `"groups":{"internal":null}`, 1)},
		{name: "null boolean variants", raw: strings.Replace(base, `"rules":[]`, `"rules":[],"variants":null`, 1)},
		{name: "invalid variant weights", raw: strings.Replace(base, `"default":false`, `"type":"variant","default":"a","variants":[{"key":"a","weight":4000},{"key":"b","weight":4000}]`, 1)},
		{name: "trailing data", raw: base + `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateRuntimeBundle([]byte(test.raw)); err == nil {
				t.Fatalf("accepted invalid bundle: %s", test.raw)
			}
		})
	}
}

func TestNewGregaleFlagsValidatesEndpointsAndBounds(t *testing.T) {
	for _, options := range []GregaleFlagsOptions{
		{APIURL: "http://api.gregale.dev", IdentityEndpoint: "http://127.0.0.1/identity"},
		{APIURL: "https://api.gregale.dev", IdentityEndpoint: "http://192.0.2.1/identity"},
		{APIURL: "https://api.gregale.dev", IdentityEndpoint: "http://localhost/identity", RefreshInterval: 61 * time.Second},
	} {
		if _, err := NewGregaleFlags(options); err == nil {
			t.Fatalf("accepted invalid options: %+v", options)
		}
	}
	flags, err := NewGregaleFlags(GregaleFlagsOptions{APIURL: "https://api.gregale.dev", IdentityEndpoint: "http://[::1]:9999/identity"})
	if err != nil {
		t.Fatalf("rejected IPv6 loopback endpoint: %v", err)
	}
	flags.Close()
	if err := flags.Start(context.Background()); !errors.Is(err, ErrFlagsClosed) {
		t.Fatalf("Start after Close error = %v", err)
	}
}

func TestGregaleFlagsMiddlewareRefreshesAndAttachesEvidence(t *testing.T) {
	t.Setenv("FAAS_APP_ID", testFlagApp)
	identity, api, flags := newTestFlagsServer(t, nil, nil)
	defer identity.Close()
	defer api.Close()
	if err := flags.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer flags.Close()

	handler := flags.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(GregaleFlagEvidenceHeader, "app-supplied-value")
		decision, err := flags.Boolean(r.Context(), "new-export", false)
		if err != nil {
			t.Errorf("Boolean: %v", err)
			http.Error(w, "flag error", http.StatusInternalServerError)
			return
		}
		if !decision.Value.(bool) || decision.RuleID != "selected" {
			t.Errorf("unexpected decision: %+v", decision)
		}
		if err := flags.Used(r.Context(), "new-export"); err != nil {
			t.Errorf("Used: %v", err)
		}
		_, _ = io.WriteString(w, "new export")
	}))
	request := httptest.NewRequest(http.MethodGet, "/exports", nil)
	request.Header.Set(GregaleFlagCustomerHeader, testFlagCustomer)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "new export" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	encoded := response.Header().Get(GregaleFlagEvidenceHeader)
	if encoded == "" || encoded == "app-supplied-value" {
		t.Fatalf("middleware did not replace application evidence header: %q", encoded)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var evidence []FlagEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].Flag != "new-export" || !evidence[0].Used || evidence[0].Value != true || evidence[0].RuleID != "selected" {
		t.Fatalf("response evidence = %+v", evidence)
	}
}

func TestGregaleFlagsMiddlewareDoesNotCommitOuterMiddlewareResponse(t *testing.T) {
	identity, api, flags := newTestFlagsServer(t, nil, nil)
	defer identity.Close()
	defer api.Close()
	if err := flags.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer flags.Close()
	inner := flags.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := flags.Boolean(r.Context(), "new-export", false); err != nil {
			t.Errorf("Boolean: %v", err)
		}
	}))
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "outer response")
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(GregaleFlagCustomerHeader, testFlagCustomer)
	response := httptest.NewRecorder()
	outer.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Body.String() != "outer response" || response.Header().Get(GregaleFlagEvidenceHeader) == "" {
		t.Fatalf("outer response was committed or lost its evidence: status=%d body=%q headers=%v", response.Code, response.Body.String(), response.Header())
	}
}

func TestGregaleFlagsUsesExplicitFallbackAfterStaleRefreshFailure(t *testing.T) {
	clock := &testFlagClock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	var failAPI atomic.Bool
	version := atomic.Int64{}
	version.Store(1)
	identity, api, flags := newTestFlagsServer(t, clock.Now, func(w http.ResponseWriter, _ *http.Request) {
		if failAPI.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		defaultValue := version.Load() == 1
		writeTestFlagBundle(w, defaultValue, version.Load())
	})
	defer identity.Close()
	defer api.Close()
	if err := flags.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer flags.Close()

	getDecision := func() FlagDecision {
		t.Helper()
		var got FlagDecision
		handler := flags.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			decision, err := flags.Boolean(r.Context(), "new-export", false)
			if err != nil {
				t.Errorf("Boolean: %v", err)
				return
			}
			got = decision
			w.WriteHeader(http.StatusNoContent)
		}))
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set(GregaleFlagCustomerHeader, testFlagCustomer)
		handler.ServeHTTP(httptest.NewRecorder(), request)
		return got
	}
	if got := getDecision(); got.Value != true || got.ConfigVersion != 1 {
		t.Fatalf("initial decision = %+v", got)
	}
	failAPI.Store(true)
	clock.Add(61 * time.Second)
	if got := getDecision(); got.Value != false || got.Reason != "configuration_stale" || got.Source != "fallback" || got.ConfigVersion != 1 {
		t.Fatalf("stale decision = %+v", got)
	}
	failAPI.Store(false)
	version.Store(2)
	if got := getDecision(); got.Value != false || got.Reason != "rule_match" || got.RuleID != "selected" || got.Source != "configuration" || got.ConfigVersion != 2 {
		t.Fatalf("refreshed decision = %+v", got)
	}
}

func TestGregaleFlagsStartupOutageUsesExplicitFallback(t *testing.T) {
	identity, api, flags := newTestFlagsServer(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	defer identity.Close()
	defer api.Close()
	if err := flags.Start(context.Background()); err != nil {
		t.Fatalf("Start should preserve explicit fallback behavior during an outage: %v", err)
	}
	defer flags.Close()
	var got FlagDecision
	handler := flags.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decision, err := flags.Boolean(r.Context(), "new-export", true)
		if err != nil {
			t.Errorf("Boolean: %v", err)
			return
		}
		got = decision
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if got.Value != true || got.Reason != "configuration_stale" || got.Source != "fallback" || got.ConfigVersion != 0 {
		t.Fatalf("startup outage decision = %+v", got)
	}
}

func TestGregaleFlagsDecisionPropagationAndTransportIsolation(t *testing.T) {
	t.Setenv("FAAS_APP_ID", testFlagApp)
	identity, api, flags := newTestFlagsServer(t, nil, nil)
	defer identity.Close()
	defer api.Close()
	if err := flags.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer flags.Close()
	base := &flagCaptureRoundTripper{}
	serviceClient := &http.Client{Transport: GregaleFlagsTransport{Flags: flags, Base: base}}
	var contextHeader string
	handler := flags.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decision, err := flags.Variant(r.Context(), "pipeline", "legacy")
		if err != nil {
			t.Errorf("Variant: %v", err)
			return
		}
		if decision.Source != "configuration" || decision.Reason != "rule_match" {
			t.Errorf("unexpected variant decision: %+v", decision)
		}
		if _, err := flags.Boolean(r.Context(), "new-export", false); err != nil {
			t.Errorf("unused Boolean: %v", err)
		}
		if err := flags.Used(r.Context(), "pipeline"); err != nil {
			t.Errorf("Used: %v", err)
		}
		contextHeader = flags.PropagationHeader(r.Context())
		for _, target := range []string{"http://billing.svc.gregale/export", "https://third-party.example/export"} {
			request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
			if err != nil {
				t.Errorf("NewRequest: %v", err)
				return
			}
			request.Header.Set(GregaleFlagContextHeader, "caller-forgery")
			if _, err := serviceClient.Do(request); err != nil {
				t.Errorf("service request: %v", err)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(GregaleFlagCustomerHeader, testFlagCustomer)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if contextHeader == "" {
		t.Fatal("used decision did not produce a propagation context")
	}
	envelope := decodeFlagPropagation(contextHeader)
	if envelope == nil || envelope.CustomerID != testFlagCustomer || len(envelope.Decisions) != 1 || envelope.Decisions[0].Flag != "pipeline" || envelope.Decisions[0].Origin.AppID != testFlagApp || envelope.Decisions[0].Origin.EnvironmentID != testFlagEnv {
		t.Fatalf("invalid propagation context: %+v", envelope)
	}
	base.mu.Lock()
	managed, external := base.headers[0], base.headers[1]
	base.mu.Unlock()
	if managed == "" || managed != contextHeader {
		t.Fatalf("managed service context = %q, want %q", managed, contextHeader)
	}
	if external != "" {
		t.Fatalf("external request retained untrusted flag context: %q", external)
	}

	var inherited FlagDecision
	var inheritedEvidence string
	downstream := flags.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decision, err := flags.Variant(r.Context(), "pipeline", "legacy")
		if err != nil {
			t.Errorf("inherited Variant: %v", err)
			return
		}
		inherited = decision
		if err := flags.Used(r.Context(), "pipeline"); err != nil {
			t.Errorf("inherited Used: %v", err)
		}
		inheritedEvidence = flags.PropagationHeader(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	downstreamRequest := httptest.NewRequest(http.MethodGet, "/worker", nil)
	downstreamRequest.Header.Set(GregaleFlagContextHeader, contextHeader)
	downstream.ServeHTTP(httptest.NewRecorder(), downstreamRequest)
	if inherited.Source != "inherited" || inherited.Value != envelope.Decisions[0].Value || inherited.ConfigVersion != 1 || inherited.InheritedFrom == nil || inherited.InheritedFrom.AppID != testFlagApp {
		t.Fatalf("downstream did not reuse the originating decision: %+v", inherited)
	}
	inheritedEnvelope := decodeFlagPropagation(inheritedEvidence)
	if inheritedEnvelope == nil || len(inheritedEnvelope.Decisions) != 1 || inheritedEnvelope.Decisions[0].Origin != envelope.Decisions[0].Origin {
		t.Fatalf("forwarded inherited decision lost its original owner: %+v", inheritedEnvelope)
	}
}

func TestGregaleFlagsRequestScopeErrorsAndEvidenceLimit(t *testing.T) {
	identity, api, flags := newTestFlagsServer(t, nil, nil)
	defer identity.Close()
	defer api.Close()
	ctx := context.Background()
	if _, err := flags.Boolean(ctx, "new-export", false); !errors.Is(err, ErrFlagRequestMissing) {
		t.Fatalf("Boolean outside middleware error = %v", err)
	}
	var state *flagRequestState
	handler := flags.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, _ = r.Context().Value(flagRequestContextKey{}).(*flagRequestState)
		for i := 0; i < maxFlagEvidence+1; i++ {
			key := "flag-" + strconv.Itoa(i)
			if _, err := flags.Boolean(r.Context(), key, false); err != nil {
				t.Errorf("Boolean(%s): %v", key, err)
			}
		}
		if err := flags.Used(r.Context(), "flag-0"); err != nil {
			t.Errorf("Used: %v", err)
		}
		if err := flags.Used(r.Context(), "flag-32"); err != nil {
			t.Errorf("overflow Used should be ignored: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if state == nil || len(flags.Evidence(context.WithValue(ctx, flagRequestContextKey{}, state))) != maxFlagEvidence {
		t.Fatal("request evidence exceeded its bound")
	}
	if err := flags.Used(ctx, "flag-0"); !errors.Is(err, ErrFlagRequestMissing) {
		t.Fatalf("Used outside middleware error = %v", err)
	}
}

type testFlagClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testFlagClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testFlagClock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestFlagsServer(t *testing.T, now func() time.Time, apiHandler http.HandlerFunc) (*httptest.Server, *httptest.Server, *GregaleFlags) {
	t.Helper()
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("audience") != "gregale:flags" {
			http.Error(w, "wrong audience", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "test-workload-token"})
	}))
	if apiHandler == nil {
		apiHandler = func(w http.ResponseWriter, _ *http.Request) { writeTestFlagBundle(w, true, 1) }
	}
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runtime/flags" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-workload-token" || r.Header.Get("Cache-Control") != "no-store" {
			http.Error(w, "missing workload auth or cache controls", http.StatusUnauthorized)
			return
		}
		apiHandler(w, r)
	}))
	options := GregaleFlagsOptions{
		APIURL: api.URL, IdentityEndpoint: identity.URL + "/identity?audience=wrong&keep=yes",
		HTTPClient: api.Client(), RefreshInterval: 30 * time.Second, MaxStale: 60 * time.Second,
		Timeout: 250 * time.Millisecond, Now: now,
	}
	flags, err := NewGregaleFlags(options)
	if err != nil {
		identity.Close()
		api.Close()
		t.Fatal(err)
	}
	return identity, api, flags
}

func writeTestFlagBundle(w http.ResponseWriter, defaultValue bool, version int64) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"environment_id": testFlagEnv,
		"version":        version,
		"groups":         map[string][]string{},
		"flags": []any{
			map[string]any{
				"key": "new-export", "enabled": true, "default": defaultValue, "seed": "export-seed",
				"rules": []any{map[string]any{"id": "selected", "customers": []string{testFlagCustomer}, "value": defaultValue}},
			},
			map[string]any{
				"key": "pipeline", "type": "variant", "enabled": true, "default": "legacy", "seed": "pipeline-seed",
				"variants": []any{map[string]any{"key": "legacy", "weight": 9000}, map[string]any{"key": "new", "weight": 1000}},
				"rules":    []any{map[string]any{"id": "experiment", "rollout": 10000}},
			},
		},
	})
}

type flagCaptureRoundTripper struct {
	mu      sync.Mutex
	headers []string
}

func (r *flagCaptureRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.headers = append(r.headers, request.Header.Get(GregaleFlagContextHeader))
	r.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    request,
	}, nil
}
