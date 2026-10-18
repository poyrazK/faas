// adr: 20261017233500
package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const traceFixtureID = "4bf92f3577b34da6a3ce929d0e0e4736"
const traceRowID = "33333333-3333-4333-8333-333333333333"

func selectedTraceFixture(appID string) api.SelectedTraceLookupResponse {
	return api.SelectedTraceLookupResponse{Selection: "explicit_app_ids_v1", TraceID: traceFixtureID, Apps: []api.SelectedTraceApp{{ID: appID, Slug: "alpha"}}, GeneratedAt: "2026-10-17T11:00:00Z", Limit: api.SelectedTraceRowsDefault, RequestWindowStart: "2026-10-14T11:00:00Z", RequestWindowEnd: "2026-10-17T11:00:00Z", RequestRetentionDays: 3,
		RequestsStatus: "ok", InvocationsStatus: "ok", Requests: []api.SelectedTraceRequest{{AppID: appID, ID: traceRowID, DeploymentID: discoveryDeploymentID, Route: "/orders?private-query=excluded", Method: "GET", Status: 503, LatencyMS: 90, Count: 7, ReceivedAt: "2026-10-17T10:59:00Z"}},
		Invocations: []api.SelectedTraceInvocation{{AppID: appID, ID: "44444444-4444-4444-8444-444444444444", Source: "queue " + testToken, State: "pending", Attempts: 2, CreatedAt: "2020-10-17T10:00:00Z"}}}
}

type traceFixture struct {
	mu              sync.Mutex
	apps            map[string]string
	native          api.SelectedTraceLookupResponse
	reads, metadata int
	scope, owner    string
	blocked         bool
	entered         chan struct{}
	onRead          func(string)
}

func newTraceFixture(t *testing.T) (*traceFixture, *httptest.Server) {
	t.Helper()
	f := &traceFixture{apps: map[string]string{"alpha": "app-1", "beta": "app-2"}, native: selectedTraceFixture("app-1"), scope: "staging", owner: "app-1", entered: make(chan struct{}, 1)}
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != "GET" {
			t.Error("trace lookup wrote native state")
			w.WriteHeader(405)
			return
		}
		var value any
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/apps/"):
			f.metadata++
			slug := strings.TrimPrefix(r.URL.Path, "/v1/apps/")
			value = api.AppResponse{ID: f.apps[slug], Slug: slug}
		case r.URL.Path == "/v1/deployments/"+discoveryDeploymentID:
			value = api.DeploymentResponse{ID: discoveryDeploymentID, AppID: f.owner, Scope: f.scope}
		case r.URL.Path == "/v1/account/traces/"+traceFixtureID+"/selected-apps":
			f.reads++
			if f.blocked {
				f.entered <- struct{}{}
				f.mu.Unlock()
				<-r.Context().Done()
				f.mu.Lock()
				return
			}
			ids := []string{}
			for _, a := range f.native.Apps {
				ids = append(ids, a.ID)
			}
			if !reflect.DeepEqual(r.URL.Query()["app_id"], ids) || r.URL.Query().Get("limit") != fmt.Sprint(f.native.Limit) {
				t.Error("trace query widened selection or changed native source limit")
			}
			value = f.native
		default:
			t.Errorf("unscoped/unexpected trace request %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		data, _ := json.Marshal(value)
		if f.onRead != nil {
			f.onRead(r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	t.Cleanup(b.Close)
	return f, b
}

func traceCallArgs() traceArgs { return traceArgs{Apps: []string{"alpha"}, TraceID: traceFixtureID} }

func TestTraceConsentAndProjection(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "partially_denied"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				f, b := newTraceFixture(t)
				opts := Options{APIBase: b.URL}
				args := traceCallArgs()
				if mode != "local" {
					opts.Environments = &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}
				}
				if mode == "denied" {
					opts.Environments.RequestReadApps = nil
				}
				if mode == "replaced" {
					opts.Environments.RequestReadApps["alpha"] = "old-app"
				}
				if mode == "partially_denied" {
					args.Apps = append(args.Apps, "beta")
				}
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				denied := mode == "denied" || mode == "replaced" || mode == "partially_denied"
				out := callTool(t, ctx, cs, "gregale_get_trace", args, denied)
				if denied {
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.reads != 0 {
						t.Fatal("denied selection fetched native trace data")
					}
					return
				}
				data, _ := json.Marshal(out)
				var v traceView
				_ = json.Unmarshal(data, &v)
				if v.Scope != "selected_apps" || v.CorrelationBasis != "exact_trace_id_only" || v.Status != "ok" || v.Partial || v.SourceCompleteness != "unknown" || len(v.Requests) != 1 || len(v.Invocations) != 1 || v.Requests[0].LookupIdentifier != traceRowID || v.Requests[0].Scope != "staging" || v.Invocations[0].StartedAt != "" || v.Invocations[0].Attempts != 2 {
					t.Fatal("trace lost provenance, request links or lifecycle uncertainty")
				}
				for _, bad := range []string{"private-query", "excluded", "fp_test_", `"headers"`, `"spans"`, `"traceparent"`, `"queue_name"`} {
					if strings.Contains(string(data), bad) {
						t.Fatal("trace leaked excluded data")
					}
				}
				f.mu.Lock()
				reads := f.reads
				f.mu.Unlock()
				if !strings.Contains(string(data), "[REDACTED]") || reads != 1 {
					t.Fatal("redaction or single scoped lookup lost")
				}
			})
		}
	}
}

func TestTraceSelectionBeforeReads(t *testing.T) {
	for _, change := range []func(*traceArgs){func(a *traceArgs) { a.Apps = nil }, func(a *traceArgs) { a.Apps = []string{"alpha", "alpha"} }, func(a *traceArgs) { a.Apps = []string{"../alpha"} }, func(a *traceArgs) { a.Apps = []string{"a", "b", "c", "d", "e", "f"} }, func(a *traceArgs) { a.TraceID = "00000000000000000000000000000000" }, func(a *traceArgs) { a.TraceID = strings.ToUpper(traceFixtureID) }, func(a *traceArgs) { a.Limit = 21 }, func(a *traceArgs) { a.Limit = -1 }} {
		f, b := newTraceFixture(t)
		cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
		args := traceCallArgs()
		change(&args)
		callTool(t, ctx, cs, "gregale_get_trace", args, true)
		f.mu.Lock()
		if f.reads != 0 || f.metadata != 0 {
			t.Fatal("invalid trace selection caused native reads")
		}
		f.mu.Unlock()
	}
}

func TestTracePartialAndBounds(t *testing.T) {
	for _, mode := range []string{"empty", "requests_failed", "invocations_failed", "both_failed", "bounded", "two_apps"} {
		t.Run(mode, func(t *testing.T) {
			f, b := newTraceFixture(t)
			args := traceCallArgs()
			if mode == "empty" {
				f.native.Requests = nil
				f.native.Invocations = nil
			}
			if mode == "requests_failed" || mode == "both_failed" {
				f.native.Requests = nil
				f.native.RequestsStatus = "unavailable"
				f.native.Partial = true
			}
			if mode == "invocations_failed" || mode == "both_failed" {
				f.native.Invocations = nil
				f.native.InvocationsStatus = "unavailable"
				f.native.Partial = true
			}
			if mode == "bounded" {
				args.Limit = api.SelectedTraceRowsMax
				f.native.Limit = args.Limit
				f.native.RequestsTruncated = true
				f.native.InvocationsTruncated = true
				r := f.native.Requests[0]
				inv := f.native.Invocations[0]
				f.native.Requests = nil
				f.native.Invocations = nil
				for i := 0; i < args.Limit; i++ {
					r.ID = fmt.Sprintf("%08x-1111-4111-8111-111111111111", i+1)
					r.Route = "/" + strings.Repeat("&", api.MCPRequestFieldMaxBytes)
					inv.ID = r.ID
					f.native.Requests = append(f.native.Requests, r)
					f.native.Invocations = append(f.native.Invocations, inv)
				}
			}
			if mode == "two_apps" {
				args.Apps = append(args.Apps, "beta")
				f.native.Apps = append(f.native.Apps, api.SelectedTraceApp{ID: "app-2", Slug: "beta"})
				f.native.Invocations[0].AppID = "app-2"
			}
			cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_get_trace", args, false)
			data, _ := json.Marshal(out)
			var v traceView
			_ = json.Unmarshal(data, &v)
			if len(data) > maxResultBytes || v.Partial != f.native.Partial || v.RequestsStatus != f.native.RequestsStatus || v.InvocationsStatus != f.native.InvocationsStatus || len(v.Requests) != len(f.native.Requests) || len(v.Invocations) != len(f.native.Invocations) || v.RequestsTruncated != f.native.RequestsTruncated {
				t.Fatal("trace source limits or partial status changed")
			}
			if mode == "both_failed" && v.Status != "unavailable" || mode == "empty" && v.Status != "ok" {
				t.Fatal("empty and unavailable sources were conflated")
			}
		})
	}
}

func TestTraceRejectsWidenedNativeEvidence(t *testing.T) {
	for _, mode := range []string{"marker", "trace", "app", "window", "partial", "failed_rows", "truncated", "request_owner", "deployment_owner", "request_window", "duplicate", "invocation_owner", "invocation_time", "order"} {
		t.Run(mode, func(t *testing.T) {
			f, b := newTraceFixture(t)
			switch mode {
			case "marker":
				f.native.Selection = ""
			case "trace":
				f.native.TraceID = "ffffffffffffffffffffffffffffffff"
			case "app":
				f.native.Apps[0].Slug = "other"
			case "window":
				f.native.RequestWindowEnd = "2026-10-16T11:00:00Z"
			case "partial":
				f.native.Partial = true
			case "failed_rows":
				f.native.RequestsStatus = "unavailable"
				f.native.Partial = true
			case "truncated":
				f.native.RequestsTruncated = true
			case "request_owner":
				f.native.Requests[0].AppID = "foreign"
			case "deployment_owner":
				f.owner = "foreign"
			case "request_window":
				f.native.Requests[0].ReceivedAt = "2026-10-10T11:00:00Z"
			case "duplicate":
				f.native.Requests = append(f.native.Requests, f.native.Requests[0])
			case "invocation_owner":
				f.native.Invocations[0].AppID = "foreign"
			case "invocation_time":
				f.native.Invocations[0].StartedAt = "invalid"
			case "order":
				r := f.native.Requests[0]
				r.ID = "55555555-5555-4555-8555-555555555555"
				r.ReceivedAt = "2026-10-16T10:00:00Z"
				f.native.Requests = append(f.native.Requests, r)
			}
			// Expected query selection stays unchanged even when the response roster is malformed.
			if mode == "app" {
				f.native.Apps[0].ID = "app-1"
			}
			cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_get_trace", traceCallArgs(), true)
			if out["requests"] != nil || out["invocations"] != nil {
				t.Fatal("invalid native trace released evidence")
			}
		})
	}
}

func TestTraceFinalIdentityChecks(t *testing.T) {
	for _, mode := range []string{"app", "scope", "owner"} {
		t.Run(mode, func(t *testing.T) {
			f, b := newTraceFixture(t)
			f.onRead = func(path string) {
				if path != "/v1/deployments/"+discoveryDeploymentID {
					return
				}
				switch mode {
				case "app":
					f.apps["alpha"] = "replacement"
				case "scope":
					f.scope = "production"
				case "owner":
					f.owner = "foreign"
				}
			}
			cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_get_trace", traceCallArgs(), true)
			if out["requests"] != nil {
				t.Fatal("late identity change released trace")
			}
		})
	}
}

func TestTraceInFlightCancellation(t *testing.T) {
	f, b := newTraceFixture(t)
	f.blocked = true
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.getTrace(ctx, traceCallArgs()); done <- err }()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("trace did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled trace lost context")
		}
	case <-time.After(time.Second):
		t.Fatal("native trace was left running")
	}
}
