package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

func TestPreviewRoutePolicyDriftReportsConfigurationChangesWithoutValues(t *testing.T) {
	route := *main.NewPreviewReportRoute("GET", "/users/{id}")
	baseline := []api.EdgeRuleResponse{{
		ID: "baseline-rule", Kind: "jwt", Enabled: true, Priority: 2, MatchPath: "/users/*", MatchMethods: []string{"get"},
		MatchHost: "*.gregale.dev", MatchHeaders: map[string]string{"Authorization": "Bearer header-secret"},
		Action: json.RawMessage(`{"jwt":{"token":"before-secret"}}`),
	}}
	candidate := []api.EdgeRuleResponse{{
		ID: "candidate-rule", Kind: "jwt", Enabled: false, Priority: 3, MatchPath: "/users/*", MatchMethods: []string{"GET"},
		MatchHost: "*.gregale.dev", MatchHeaders: map[string]string{"authorization": "Bearer header-secret"},
		Action: json.RawMessage(`{"jwt":{"token":"after-secret"}}`),
	}}

	changes, complete := main.DiffPreviewRoutePolicy(&route, baseline, candidate)
	if !complete || len(changes) != 1 || changes[0].Change != "modified" || !main.ContainsString(changes[0].ChangedFields, "action") || !main.ContainsString(changes[0].ChangedFields, "enabled") || !main.ContainsString(changes[0].ChangedFields, "priority") {
		t.Fatalf("changes=%+v complete=%t", changes, complete)
	}
	report := main.PreviewRouteReport{Contract: main.PreviewReportEvidence{Status: "available"}, Routes: []main.PreviewReportRoute{route}}
	main.AttachPreviewRoutePolicyDrift(&report, baseline, candidate, nil, nil)
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"before-secret", "after-secret", "header-secret"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("report leaked %q: %s", secret, body)
		}
	}
	if report.PolicyDrift.Status != "available" || report.PolicyDrift.ChangedRoutes != 1 || report.Routes[0].PolicyDrift.Status != "changed" {
		t.Fatalf("drift evidence=%+v route=%+v", report.PolicyDrift, report.Routes[0].PolicyDrift)
	}
}

func TestPreviewRoutePolicyDriftIgnoresRuleIDsAndRejectsUncomparableRules(t *testing.T) {
	route := *main.NewPreviewReportRoute("GET", "/users/{id}")
	baseline := api.EdgeRuleResponse{ID: "old-id", Kind: "jwt", Enabled: true, MatchPath: "/users/*", Action: json.RawMessage(`{"limit":9007199254740992}`)}
	candidate := baseline
	candidate.ID = "new-id"
	if changes, complete := main.DiffPreviewRoutePolicy(&route, []api.EdgeRuleResponse{baseline}, []api.EdgeRuleResponse{candidate}); !complete || len(changes) != 0 {
		t.Fatalf("ID-only replacement became policy drift: %+v complete=%t", changes, complete)
	}
	candidate.Action = json.RawMessage(`{"limit":9007199254740993}`)
	if changes, complete := main.DiffPreviewRoutePolicy(&route, []api.EdgeRuleResponse{baseline}, []api.EdgeRuleResponse{candidate}); !complete || len(changes) != 1 || !main.ContainsString(changes[0].ChangedFields, "action") {
		t.Fatalf("large numeric action change was lost: %+v complete=%t", changes, complete)
	}
	candidate.Action = json.RawMessage(`{invalid`)
	if _, complete := main.DiffPreviewRoutePolicy(&route, []api.EdgeRuleResponse{baseline}, []api.EdgeRuleResponse{candidate}); complete {
		t.Fatal("malformed action was treated as comparable")
	}
	candidate = baseline
	candidate.MatchPath = "[invalid"
	if _, complete := main.DiffPreviewRoutePolicy(&route, []api.EdgeRuleResponse{candidate}, nil); complete {
		t.Fatal("malformed path selector was silently ignored")
	}
}

func TestPreviewRoutePolicyDriftReportsAddedAndRemovedRouteRules(t *testing.T) {
	route := *main.NewPreviewReportRoute("GET", "/users/{id}")
	baseRule := api.EdgeRuleResponse{ID: "base", Kind: "jwt", Enabled: true, MatchPath: "/users/*", MatchMethods: []string{"GET"}, Action: json.RawMessage(`{"jwt":{}}`)}
	addedRule := api.EdgeRuleResponse{ID: "added", Kind: "throttle", Enabled: true, MatchPath: "/users/*", MatchMethods: []string{"GET"}, Action: json.RawMessage(`{"requests_per_second":10}`)}
	otherMethodRule := api.EdgeRuleResponse{ID: "post-only", Kind: "headers", Enabled: true, MatchPath: "/users/*", MatchMethods: []string{"POST"}, Action: json.RawMessage(`{"headers":{}}`)}

	added, complete := main.DiffPreviewRoutePolicy(&route, []api.EdgeRuleResponse{baseRule}, []api.EdgeRuleResponse{baseRule, addedRule, otherMethodRule})
	if !complete || len(added) != 1 || added[0].Change != "added" || added[0].After.Kind != "throttle" {
		t.Fatalf("added route rules=%+v complete=%t", added, complete)
	}
	removed, complete := main.DiffPreviewRoutePolicy(&route, []api.EdgeRuleResponse{baseRule, addedRule}, []api.EdgeRuleResponse{baseRule})
	if !complete || len(removed) != 1 || removed[0].Change != "removed" || removed[0].Before.Kind != "throttle" {
		t.Fatalf("removed route rules=%+v complete=%t", removed, complete)
	}
}

func TestPreviewRoutePolicyDriftKeepsLegacySnapshotUnknown(t *testing.T) {
	row := *main.NewPreviewReportRoute("GET", "/users/{id}")
	report := main.PreviewRouteReport{Contract: main.PreviewReportEvidence{Status: "available"}, Routes: []main.PreviewReportRoute{row}}
	missing := &api.APIError{Problem: api.Problem{Status: http.StatusNotFound, Code: "deployment_route_policy_snapshot_not_found"}}
	main.AttachPreviewRoutePolicyDrift(&report, nil, nil, missing, nil)
	if report.PolicyDrift.Status != "partial" || report.PolicyDrift.Scope != "deployment_pair" || report.PolicyDrift.UnknownRoutes != 1 || report.Routes[0].PolicyDrift == nil || report.Routes[0].PolicyDrift.Status != "unknown" {
		t.Fatalf("missing historical snapshot was not kept unknown: %+v", report)
	}
}

func TestPreviewRoutePolicyDriftMarksUnreadableSnapshotsUnknown(t *testing.T) {
	row := *main.NewPreviewReportRoute("GET", "/users/{id}")
	report := main.PreviewRouteReport{Contract: main.PreviewReportEvidence{Status: "available"}, Routes: []main.PreviewReportRoute{row}}
	main.AttachPreviewRoutePolicyDrift(&report, nil, nil, errors.New("snapshot store unavailable"), nil)
	if report.PolicyDrift.Status != "unavailable" || report.PolicyDrift.UnknownRoutes != 1 || report.Routes[0].PolicyDrift == nil ||
		report.Routes[0].PolicyDrift.Status != "unknown" || report.Routes[0].PolicyDrift.Reason != "deployment_policy_snapshot_unavailable" {
		t.Fatalf("unreadable historical snapshot was not kept unknown: %+v", report)
	}
}

func TestPreviewReportPolicyDriftGateFailsOnChangedRouteAndRedactsValues(t *testing.T) {
	main.ResetJSONOut(t)
	main.SetPreviewTestAuth(t)
	baselineAction := json.RawMessage(`{"jwt":{"token":"before-secret"}}`)
	candidateAction := json.RawMessage(`{"jwt":{"token":"after-secret"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("route report mutated state: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/preview/pr-42-api":
			main.WriteJSONTest(w, api.PreviewResourceResponse{
				App:                  api.AppResponse{ID: "preview-id", Slug: "pr-42-api", PreviewOfSlug: "api"},
				Parent:               &api.AppResponse{ID: "parent-id", Slug: "api"},
				LatestDeployment:     &api.DeploymentResponse{ID: "candidate", AppID: "preview-id", Status: "live"},
				ProductionDeployment: &api.DeploymentResponse{ID: "baseline", AppID: "parent-id", Status: "live"},
			})
		case "/v1/apps/api/deployments/baseline/openapi":
			main.WritePreviewReportDoc(t, w, "baseline", main.PreviewReportBefore)
		case "/v1/apps/pr-42-api/deployments/candidate/openapi":
			main.WritePreviewReportDoc(t, w, "candidate", main.PreviewReportBefore)
		case "/v1/apps/api/deployments/baseline/route-policy":
			main.WritePreviewPolicySnapshotTest(w, "baseline", "parent-id", []api.EdgeRuleResponse{{
				ID: "baseline-rule", Kind: "jwt", Enabled: true, MatchPath: "/users/*", MatchMethods: []string{"GET"},
				MatchHeaders: map[string]string{"Authorization": "Bearer header-secret"}, Action: baselineAction,
			}})
		case "/v1/apps/pr-42-api/deployments/candidate/route-policy":
			main.WritePreviewPolicySnapshotTest(w, "candidate", "preview-id", []api.EdgeRuleResponse{{
				ID: "candidate-rule", Kind: "jwt", Enabled: true, MatchPath: "/users/*", MatchMethods: []string{"GET"},
				MatchHeaders: map[string]string{"Authorization": "Bearer header-secret"}, Action: candidateAction,
			}})
		case "/v1/apps/pr-42-api/openapi/preview":
			main.WriteJSONTest(w, api.AppOpenAPIPolicyPreviewResponse{})
		case "/v1/apps/api/edge-rules":
			main.WriteJSONTest(w, []api.EdgeRuleResponse{{
				ID: "baseline-rule", Kind: "jwt", Enabled: true, MatchPath: "/users/*", MatchMethods: []string{"GET"},
				MatchHeaders: map[string]string{"Authorization": "Bearer header-secret"}, Action: baselineAction,
			}})
		case "/v1/apps/pr-42-api/edge-rules":
			main.WriteJSONTest(w, []api.EdgeRuleResponse{{
				ID: "candidate-rule", Kind: "jwt", Enabled: true, MatchPath: "/users/*", MatchMethods: []string{"GET"},
				MatchHeaders: map[string]string{"Authorization": "Bearer header-secret"}, Action: candidateAction,
			}})
		case "/v1/apps/api/analytics":
			main.WriteJSONTest(w, main.PreviewReportAnalytics("baseline", 100, 40))
		case "/v1/apps/pr-42-api/analytics":
			main.WriteJSONTest(w, main.PreviewReportAnalytics("candidate", 100, 40))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var out bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	(*main.JsonOutput) = true
	if code := main.CmdPreview([]string{"report", "pr-42-api", "--fail-on-policy-drift"}); code != 1 {
		t.Fatalf("exit=%d; want policy drift gate failure: %s", code, out.String())
	}
	var report main.PreviewRouteReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	row := main.FindPreviewReportRoute(t, report, "GET /users/{id}")
	if report.PolicyDrift.Status != "available" || report.PolicyDrift.Scope != "deployment_pair" || report.PolicyDrift.ChangedRoutes != 1 || row.PolicyDrift == nil || len(row.PolicyDrift.Changes) != 1 || !main.ContainsString(row.PolicyDrift.Changes[0].ChangedFields, "action") {
		t.Fatalf("policy drift comparison missing: %+v", report)
	}
	for _, secret := range []string{"before-secret", "after-secret", "header-secret"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("report leaked %q: %s", secret, out.String())
		}
	}
}

func TestPreviewPolicyDriftAddsRouteReviewPriority(t *testing.T) {
	row := *main.NewPreviewReportRoute("GET", "/users/{id}")
	row.RouteSource = "captured_deployment_contract"
	row.TestProfiles = []main.PreviewReportTest{{Profile: "warm", Passed: 1}}
	row.RequestCompatibility = &openapidiff.RequestRoute{Status: "no_supported_breaks", Complete: true}
	row.SecurityCompatibility = &openapidiff.SecurityRoute{Status: "unchanged", Complete: true}
	row.PolicyDrift = &main.PreviewRoutePolicyDrift{Status: "changed", Changes: []main.PreviewRoutePolicyRuleChange{{Change: "modified", ChangedFields: []string{"action"}}}}
	report := main.PreviewRouteReport{Outcome: "no_findings", Requests: main.PreviewReportEvidence{Status: "available"}, Security: main.PreviewReportEvidence{Status: "available"}, PolicyDrift: main.PreviewRoutePolicyDriftEvidence{Status: "available"}, Routes: []main.PreviewReportRoute{row}}
	main.PrioritizePreviewRouteReview(&report)
	if report.Outcome != "review_required" || len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Scope != "captured_route" || report.ReviewPriorities[0].PolicyDrift != "changed" || !main.ContainsString(report.ReviewPriorities[0].Reasons, "policy_rule_modified") {
		t.Fatalf("route policy drift was not prioritized: %+v", report)
	}
}
