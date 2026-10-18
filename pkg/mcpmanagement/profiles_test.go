// adr: 1639
package mcpmanagement

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const profileStart = "2026-10-16T10:00:00Z"
const profileEnd = "2026-10-16T11:00:00Z"

func profileArgs() profilesArgs {
	return profilesArgs{App: "alpha", DeploymentID: discoveryDeploymentID, Runtime: "go", Start: profileStart, End: profileEnd, Route: "GET /orders", Limit: 2}
}
func profileFixture(empty bool) api.ProfileResponse {
	start, _ := time.Parse(time.RFC3339, profileStart)
	end, _ := time.Parse(time.RFC3339, profileEnd)
	last := end.Add(15 * time.Minute)
	pct, unpct := 60.0, 40.0
	n := api.ProfileResponse{Query: api.ProfileQuery{DeploymentID: discoveryDeploymentID, Runtime: "go", Start: start, End: end, Route: "GET /orders"}, CPUSeconds: 3, StackCount: 4,
		Functions:  []api.ProfileFunction{{Name: "Compute " + testToken, File: "excluded-file-a", Line: 7, Source: &api.ProfileSourceLocation{URL: "https://excluded-source.example", Path: "excluded-path", Line: 7}, SelfCPUSeconds: 2, TotalCPUSeconds: 2}, {Name: "same", File: "excluded-file-b", Line: 8, SelfCPUSeconds: 1, TotalCPUSeconds: 1}, {Name: "same", File: "excluded-file-c", Line: 9, SelfCPUSeconds: 0, TotalCPUSeconds: 3}},
		Flamegraph: &api.ProfileStack{Name: "excluded-stack", CPUSeconds: 3}, Source: &api.ProfileSource{Available: true, Repository: "excluded-repository", CommitSHA: "excluded-commit"},
		Coverage:    &api.ProfileCoverage{Available: true, ReceivedProfiles: 3, ContributingCollectors: 2, LastReceivedAt: &last, WindowSeconds: 3600, CoveredSeconds: 3000, GapSeconds: 600, RecordedFailedUploads: 0, FailuresComplete: false},
		Attribution: &api.ProfileAttributionQuality{Available: true, TotalCPUSeconds: 10, AttributedCPUSeconds: 6, UnattributedCPUSeconds: 4, AttributedPercent: &pct, UnattributedPercent: &unpct, DiagnosticsComplete: false, Reasons: []api.ProfileAttributionReason{{Reason: "unlabeled", CPUSeconds: 2}, {Reason: "unknown", CPUSeconds: 2}}},
	}
	if empty {
		n.Empty = true
		n.CPUSeconds = 0
		n.StackCount = 0
		n.Functions = nil
	}
	return n
}

func TestProfilesConsentAndProjection(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "allowlist_denied"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						t.Error("profile tool mutated native state")
					}
					switch r.URL.Path {
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/deployments/" + discoveryDeploymentID:
						_ = json.NewEncoder(w).Encode(map[string]string{"id": discoveryDeploymentID, "app_id": "app-1", "scope": "staging"})
					case "/v1/apps/alpha/profiles":
						reads.Add(1)
						q := r.URL.Query()
						if len(q) != 5 || q.Get("deployment_id") != discoveryDeploymentID || q.Get("runtime") != "go" || q.Get("route") != "GET /orders" || q.Get("start") != profileStart || q.Get("end") != profileEnd {
							t.Error("profile selection changed or limit forwarded")
						}
						_ = json.NewEncoder(w).Encode(profileFixture(false))
					default:
						t.Error("unexpected profile enrichment")
						w.WriteHeader(404)
					}
				}))
				defer b.Close()
				opts := Options{APIBase: b.URL, Token: testToken}
				if mode != "local" {
					opts.Environments = &EnvironmentConsent{RequestReadApps: map[string]string{}}
					if mode == "approved" {
						opts.Environments.RequestReadApps["alpha"] = "app-1"
					}
					if mode == "replaced" {
						opts.Environments.RequestReadApps["alpha"] = "old-app"
					}
				}
				if mode == "allowlist_denied" {
					opts.AllowedApps = map[string]string{"other": "other-app"}
				}
				denied := mode == "denied" || mode == "replaced" || mode == "allowlist_denied"
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				out := callTool(t, ctx, cs, "gregale_get_profiles", profileArgs(), denied)
				if denied {
					if reads.Load() != 0 {
						t.Fatal("profile read bypassed diagnostic consent")
					}
					return
				}
				raw, _ := json.Marshal(out)
				var v profilesView
				if json.Unmarshal(raw, &v) != nil {
					t.Fatal("invalid profile view")
				}
				if reads.Load() != 1 || v.AppID != "app-1" || v.Scope != "app_wide" || v.Status != "observed" || v.CPUSeconds == nil || *v.CPUSeconds != 3 || v.NativeCount != 3 || v.ReturnedCount != 2 || !v.FunctionsTruncated || v.WindowVerification != "native_query_echo" || v.SourceCompleteness != "unknown" || !v.Functions[0].LabelsModified {
					t.Fatal("lost profile selection, uncertainty or output bounds")
				}
				if v.Coverage.Measurements == nil || v.Coverage.Measurements.GapSeconds != 600 || v.Coverage.Measurements.FailedUploads != 0 || v.Coverage.Measurements.FailuresComplete || v.Coverage.Scope != "deployment_runtime_window" || v.Attribution.Measurements == nil || v.Attribution.Measurements.TotalCPUSeconds != 10 || v.Attribution.Measurements.AttributedPercent != 60 {
					t.Fatal("confused route CPU, whole-window quality or observed zero")
				}
				for _, marker := range []string{testToken, "excluded-", `"file"`, `"line"`, `"flamegraph"`, `"source"`, `"routes"`, `"collector_id"`} {
					if strings.Contains(string(raw), marker) {
						t.Fatalf("profile projection leaked %s", marker)
					}
				}
			})
		}
	}
}

func TestProfileSelection(t *testing.T) {
	m := &management{opts: Options{Token: testToken}}
	for _, mode := range []string{"valid", "utc", "unattributed", "all_routes", "default", "no_app", "bad_deployment", "nil_deployment", "no_runtime", "bad_runtime", "long_runtime", "bad_start", "zero_start", "bad_end", "future", "reversed", "route", "url", "secret_route", "limit", "long_time"} {
		t.Run(mode, func(t *testing.T) {
			a := profileArgs()
			switch mode {
			case "utc":
				a.Start = "2026-10-16T11:00:00.123456789+01:00"
			case "unattributed":
				a.Route = api.ProfileUnattributedRoute
			case "all_routes":
				a.Route = ""
			case "default":
				a.Limit = 0
			case "no_app":
				a.App = ""
			case "bad_deployment":
				a.DeploymentID = "bad"
			case "nil_deployment":
				a.DeploymentID = "00000000-0000-0000-0000-000000000000"
			case "no_runtime":
				a.Runtime = ""
			case "bad_runtime":
				a.Runtime = "Go/unsafe"
			case "long_runtime":
				a.Runtime = strings.Repeat("a", 65)
			case "bad_start":
				a.Start = "1h"
			case "zero_start":
				a.Start = "0001-01-01T00:00:00Z"
			case "bad_end":
				a.End = "bad"
			case "future":
				a.End = time.Now().Add(time.Hour).Format(time.RFC3339)
			case "reversed":
				a.End = a.Start
			case "route":
				a.Route = "/orders"
			case "url":
				a.Route = "GET /orders?token=secret"
			case "secret_route":
				a.Route = "GET /" + testToken
			case "limit":
				a.Limit = 51
			case "long_time":
				a.Start = strings.Repeat("a", 65)
			}
			q, limit, err := m.profileSelection(a)
			good := mode == "valid" || mode == "utc" || mode == "unattributed" || mode == "all_routes" || mode == "default"
			if (err == nil) != good {
				t.Fatalf("mode %s: %v", mode, err)
			}
			if good && (limit < 1 || limit > 50 || mode == "default" && limit != 10 || mode == "utc" && q.Start.UTC().Format(time.RFC3339Nano) != "2026-10-16T10:00:00.123456789Z") {
				t.Fatal("selection normalization or defaults changed")
			}
		})
	}
}

func TestProfileProjectionValidation(t *testing.T) {
	m := &management{opts: Options{Token: testToken}}
	q, _, _ := m.profileSelection(profileArgs())
	for _, mode := range []string{"valid", "empty", "missing_quality", "unavailable_quality", "unknown_reason", "wrong_deployment", "wrong_runtime", "wrong_route", "wrong_start", "bad_count", "excess_count", "missing_self_cpu", "bad_empty", "nan", "negative", "self_gt_total", "total_gt_cpu", "unsorted", "omitted_invalid", "coverage_window", "coverage_gap", "negative_receipts", "attribution_sum", "attribution_pct", "reason_sum", "missing_pct", "attribution_overflow", "reason_overflow", "coverage_overflow", "too_many_reasons", "all_routes_mismatch"} {
		t.Run(mode, func(t *testing.T) {
			n := profileFixture(mode == "empty")
			query := q
			switch mode {
			case "missing_quality":
				n.Coverage = nil
				n.Attribution = nil
			case "unavailable_quality":
				n.Coverage.Available = false
				n.Attribution.Available = false
			case "unknown_reason":
				n.Attribution.Reasons[0].Reason = "excluded-arbitrary"
			case "wrong_deployment":
				n.Query.DeploymentID = comparisonTargetID
			case "wrong_runtime":
				n.Query.Runtime = "node"
			case "wrong_route":
				n.Query.Route = ""
			case "wrong_start":
				n.Query.Start = n.Query.Start.Add(time.Second)
			case "bad_count":
				n.StackCount = -1
			case "excess_count":
				n.StackCount = api.ProfileMaxNodes + 1
			case "missing_self_cpu":
				n.Functions[1].SelfCPUSeconds = 0
			case "bad_empty":
				n.Empty = true
			case "nan":
				n.CPUSeconds = math.NaN()
			case "negative":
				n.Functions[0].SelfCPUSeconds = -1
			case "self_gt_total":
				n.Functions[0].TotalCPUSeconds = 1
			case "total_gt_cpu":
				n.Functions[0].TotalCPUSeconds = 4
			case "unsorted":
				n.Functions[1].SelfCPUSeconds = 3
				n.Functions[1].TotalCPUSeconds = 3
			case "omitted_invalid":
				n.Functions[2].TotalCPUSeconds = math.Inf(1)
			case "coverage_window":
				n.Coverage.WindowSeconds = 10
			case "coverage_gap":
				n.Coverage.GapSeconds = 2
			case "negative_receipts":
				n.Coverage.ReceivedProfiles = -1
			case "attribution_sum":
				n.Attribution.AttributedCPUSeconds = 9
			case "attribution_pct":
				x := 90.0
				n.Attribution.AttributedPercent = &x
			case "reason_sum":
				n.Attribution.Reasons[0].CPUSeconds = 20
			case "missing_pct":
				n.Attribution.AttributedPercent = nil
			case "attribution_overflow":
				n.Attribution.TotalCPUSeconds = math.MaxFloat64
				n.Attribution.AttributedCPUSeconds = math.MaxFloat64
				n.Attribution.UnattributedCPUSeconds = math.MaxFloat64
			case "reason_overflow":
				n.Attribution.Reasons[0].CPUSeconds = math.MaxFloat64
				n.Attribution.Reasons[1].CPUSeconds = math.MaxFloat64
			case "coverage_overflow":
				n.Coverage.CoveredSeconds = math.MaxFloat64
				n.Coverage.GapSeconds = math.MaxFloat64
			case "too_many_reasons":
				n.Attribution.Reasons = make([]api.ProfileAttributionReason, 7)
			case "all_routes_mismatch":
				query.Route = ""
				n.Query.Route = ""
			}
			v, err := m.projectProfiles("alpha", "app-1", query, 2, n)
			good := mode == "valid" || mode == "empty" || mode == "missing_quality" || mode == "unavailable_quality" || mode == "unknown_reason"
			if (err == nil) != good {
				t.Fatalf("mode %s error=%v", mode, err)
			}
			if mode == "empty" && (v.Status != "no_selected_samples" || v.CPUSeconds != nil || len(v.Functions) != 0 || v.Attribution.Measurements == nil) {
				t.Fatal("empty route sample invented CPU or discarded whole-window quality")
			}
			if (mode == "missing_quality" || mode == "unavailable_quality") && (v.Coverage.Measurements != nil || v.Attribution.Measurements != nil || v.CPUSeconds == nil) {
				t.Fatal("unavailable quality invented zeros or lost valid CPU")
			}
			if mode == "unknown_reason" && v.Attribution.Measurements.Reasons[0].Code != "unrecognized" {
				t.Fatal("native free text reason exposed")
			}
		})
	}
	full, err := m.projectProfiles("alpha", "app-1", q, 3, profileFixture(false))
	if err != nil || full.Functions[1].Name != full.Functions[2].Name || full.Functions[2].SelfCPUSeconds != 0 {
		t.Fatal("native name collisions merged or zero CPU lost")
	}
}

func TestProfileNativeErrorsAndIdentity(t *testing.T) {
	for _, mode := range []string{"foreign_deployment", "app_replaced", "scope_changed", "402", "403", "404", "429", "503"} {
		t.Run(mode, func(t *testing.T) {
			var reads atomic.Int32
			var changed atomic.Bool
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/alpha":
					id := "app-1"
					if changed.Load() && mode == "app_replaced" {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
				case "/v1/deployments/" + discoveryDeploymentID:
					id, scope := "app-1", "staging"
					if mode == "foreign_deployment" {
						id = "other"
					}
					if changed.Load() && mode == "scope_changed" {
						scope = "production"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": discoveryDeploymentID, "app_id": id, "scope": scope})
				case "/v1/apps/alpha/profiles":
					reads.Add(1)
					changed.Store(true)
					if status, err := strconv.Atoi(mode); err == nil {
						api.WriteProblem(w, api.NewProblem(status, api.CodeValidation, "native denied", "excluded-private "+testToken))
						return
					}
					_ = json.NewEncoder(w).Encode(profileFixture(false))
				default:
					t.Error("unexpected profile read")
				}
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_get_profiles", profileArgs(), true)
			want := int32(1)
			if mode == "foreign_deployment" {
				want = 0
			}
			if reads.Load() != want || out["functions"] != nil {
				t.Fatal("foreign/native error leaked evidence or triggered retry")
			}
		})
	}
}

// Sanitized names remain display data and do not merge native groups.
func TestProfileLabelsAndBounds(t *testing.T) {
	m := &management{opts: Options{Token: testToken}}
	q, _, _ := m.profileSelection(profileArgs())
	n := profileFixture(false)
	n.Functions[0].Name = strings.Repeat("é", api.MCPRequestFieldMaxBytes)
	n.Functions[1].Name = ""
	v, err := m.projectProfiles("alpha", "app-1", q, 3, n)
	if err != nil || !v.TextTruncated || !v.Functions[0].LabelsModified || len(v.Functions[0].Name) > api.MCPRequestFieldMaxBytes || v.Functions[1].Name != "[unnamed]" || !v.Functions[1].LabelsModified || v.Functions[2].SelfCPUSeconds != 0 {
		t.Fatal("profile label bounds, missing names or observed zero lost")
	}
	// All-routes reads are valid when quality and selected CPU cover the same window.
	n.Query.Route = ""
	q.Route = ""
	n.CPUSeconds = 10
	n.Functions[0].SelfCPUSeconds = 9
	n.Functions[0].TotalCPUSeconds = 9
	n.Attribution.DiagnosticsComplete = true
	if _, err = m.projectProfiles("alpha", "app-1", q, 3, n); err != nil {
		t.Fatal(err)
	}
}

func TestProfileNativeReasonTolerance(t *testing.T) {
	m := &management{}
	q, _, _ := m.profileSelection(profileArgs())
	for _, reasonCPU := range []float64{0, 0.0002, 0.01} {
		n := profileFixture(false)
		a := n.Attribution
		a.TotalCPUSeconds, a.AttributedCPUSeconds, a.UnattributedCPUSeconds = 1000, 999.9999, 0.0001
		attributed, unattributed := a.AttributedCPUSeconds/a.TotalCPUSeconds*100, a.UnattributedCPUSeconds/a.TotalCPUSeconds*100
		a.AttributedPercent, a.UnattributedPercent = &attributed, &unattributed
		a.DiagnosticsComplete = true
		a.Reasons = nil
		if reasonCPU != 0 {
			a.Reasons = []api.ProfileAttributionReason{{Reason: "unknown", CPUSeconds: reasonCPU}}
		}
		_, err := m.projectProfiles("alpha", "app-1", q, 2, n)
		if (err == nil) != (reasonCPU < 0.001) {
			t.Fatalf("native reason CPU %g: %v", reasonCPU, err)
		}
	}
}
