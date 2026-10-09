package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestLifecycleApprovalCompatibility(t *testing.T) {
	source := `{"openapi":"3.1.0","info":{"title":"routes","version":"1"},"paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	target := strings.ReplaceAll(source, "/old", "/new")
	s := &server{domain: "gregale.dev"}
	snapshot := state.RoutePolicySnapshot{Account: state.Account{Plan: api.PlanPro}, App: state.App{Slug: "api"}}
	mapping := []api.RouteLifecycleMapping{{Method: "GET", Path: "/old", SuccessorMethod: "GET", SuccessorPath: "/new", SuccessorURL: "https://api.gregale.dev/new"}}
	for _, tc := range []struct {
		name, doc string
		pass      bool
	}{
		{"compatible", target, true},
		{"required_query", strings.Replace(target, `"get":{`, `"get":{"parameters":[{"name":"new","in":"query","required":true,"schema":{"type":"string"}}],`, 1), false},
		{"missing_response", strings.Replace(target, `"responses":{"200":{"description":"ok"}}`, `"responses":{}`, 1), false},
		{"missing_operation", strings.ReplaceAll(target, "/new", "/other"), false},
		{"unsupported_reference", `{"openapi":"3.1.0","paths":{"/new":{"$ref":"#/components/pathItems/New"}}}`, false},
		{"non_root_server", strings.Replace(target, `"paths":`, `"servers":[{"url":"https://api.gregale.dev/prefix"}],"paths":`, 1), false},
		{"stronger_security", strings.Replace(target, `"get":{`, `"get":{"security":[{"token":[]}],`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := s.validateRouteLifecycleCompatibility(snapshot, &state.RoutePolicyContract{DeploymentID: "old", SHA256: strings.Repeat("a", 64), Doc: []byte(source)}, &state.RoutePolicyContract{DeploymentID: "new", SHA256: strings.Repeat("b", 64), Doc: []byte(tc.doc)}, mapping)
			if (err == nil) != tc.pass {
				t.Fatalf("pass=%v: %v", tc.pass, err)
			}
		})
	}
	mapping[0].SuccessorURL = "https://external.example/new"
	if err := s.validateRouteLifecycleCompatibility(snapshot, &state.RoutePolicyContract{Doc: []byte(source)}, &state.RoutePolicyContract{Doc: []byte(target)}, mapping); err == nil {
		t.Fatal("external URL accepted")
	}
}
func TestLifecycleApprovalAPI(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "lifecycle-approval")
	baseline, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:baseline"})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkDeploymentLive(t.Context(), baseline.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", CanaryPreset: "balanced", CanaryStep: 0, CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	intent := api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: api.RouteRequirementsConfig{Version: 2, Public: []api.RoutePublicException{{Method: "GET", Path: "/health", Reason: "public health"}, {Method: "GET", Path: "/new", Reason: "public successor"}}}}
	if rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/route-requirements", intent, nil); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	gate, err := e.store.SetCanaryRouteGate(t.Context(), e.acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero})
	if err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`{"openapi":"3.1.0","info":{"title":"routes","version":"1"},"paths":{"/health":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2099-01-01T00:00:00Z","x-gregale-successor":"https://%s.gregale.dev/previous","responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`, app.Slug)
	target := strings.ReplaceAll(source, ".gregale.dev/previous", ".gregale.dev/new")
	if err = e.store.UpsertDeploymentOpenAPIDoc(t.Context(), baseline.ID, e.acct.ID, app.ID, []byte(source), "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	if err = e.store.UpsertDeploymentOpenAPIDoc(t.Context(), candidate.ID, e.acct.ID, app.ID, []byte(target), "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	if count, err := e.s.drainAutomaticRouteChecks(t.Context()); err != nil || count != 2 {
		t.Fatal("automatic checks", count, err)
	}
	checkRec := e.do(t, "POST", "/v1/apps/"+app.Slug+"/route-requirements/check", api.CheckRouteRequirementsRequest{DeploymentID: candidate.ID}, nil)
	var check api.RouteRequirementsCheck
	if checkRec.Code != 200 || json.Unmarshal(checkRec.Body.Bytes(), &check) != nil {
		t.Fatal(checkRec.Code, checkRec.Body.String())
	}
	_, bm, _ := e.store.GetDeploymentOpenAPIDoc(t.Context(), baseline.ID, e.acct.ID)
	_, cm, _ := e.store.GetDeploymentOpenAPIDoc(t.Context(), candidate.ID, e.acct.ID)
	request := api.ApproveRouteLifecycleRequest{ExpectedGateRevision: &gate.Revision, ExpectedRequirementsRevision: &check.RequirementsRevision, ExpectedRemovalPolicyRevision: &zero, ConfigurationSHA256: check.ConfigurationSHA256, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: fmt.Sprintf("%x", bm.DocSHA256), CandidateContractSHA256: fmt.Sprintf("%x", cm.DocSHA256), Mappings: []api.RouteLifecycleMapping{{Method: "GET", Path: "/health", SuccessorMethod: "GET", SuccessorPath: "/new", SuccessorURL: "https://" + app.Slug + ".gregale.dev/new"}}}
	advancePath := "/v1/deployments/" + candidate.ID + "/canary/advance"
	if rec := e.do(t, "POST", advancePath, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), "lifecycle_successor_changed_requires_review") {
		t.Fatal("unreviewed", rec.Code, rec.Body.String())
	}
	approvalPath := "/v1/apps/" + app.Slug + "/route-lifecycle/approvals"
	rec := e.do(t, "POST", approvalPath, request, nil)
	var receipt api.RouteLifecycleApproval
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &receipt) != nil || receipt.ApprovedBy == "" || receipt.Compatibility != "no_supported_breaks" {
		t.Fatal("approval", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "GET", approvalPath+"/"+receipt.ID, nil, nil); rec.Code != 200 {
		t.Fatal("receipt read", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "POST", advancePath, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), receipt.ID) {
		t.Fatal("approved advance", rec.Code, rec.Body.String())
	}
	historyPath := "/v1/apps/" + app.Slug + "/route-lifecycle/history"
	historyRec := e.do(t, "GET", historyPath+"?limit=20", nil, nil)
	var history api.RouteLifecycleHistoryPage
	if historyRec.Code != 200 || json.Unmarshal(historyRec.Body.Bytes(), &history) != nil || len(history.Entries) == 0 || history.Entries[0].Outcome != "applied" || len(history.Entries[0].Approvals) != 1 || !history.Entries[0].Approvals[0].Used {
		t.Fatal("history", historyRec.Code, historyRec.Body.String())
	}
	blockedFound := false
	for _, entry := range history.Entries {
		blockedFound = blockedFound || entry.Outcome == "blocked"
	}
	if !blockedFound {
		t.Fatal("blocked review missing", history)
	}
	for _, query := range []string{"?limit=0", "?limit=21", "?before=01", "?before=-1"} {
		if rec := e.do(t, "GET", historyPath+query, nil, nil); rec.Code != 400 {
			t.Fatal("history validation", query, rec.Code)
		}
	}
	if rec := e.do(t, "GET", historyPath+"?before=9223372036854775807", nil, nil); rec.Code != 404 {
		t.Fatal("unknown history cursor", rec.Code)
	}
	for _, forbidden := range []string{"configuration_snapshot", "successor_snapshot", "public_auth_basic_sealed", "retry_policy_json"} {
		if strings.Contains(historyRec.Body.String(), forbidden) {
			t.Fatal("private history data", forbidden)
		}
	}
	stale := request
	stale.ConfigurationSHA256 = strings.Repeat("0", 64)
	if rec := e.do(t, "POST", approvalPath, stale, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), api.CodeRouteLifecycleReviewChanged) {
		t.Fatal("stale request", rec.Code, rec.Body.String())
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read-receipts", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	if rec := e.do(t, "POST", approvalPath, request, nil); rec.Code != 403 {
		t.Fatal("read credential approved", rec.Code)
	}
	if rec := e.do(t, "GET", approvalPath+"/"+receipt.ID, nil, nil); rec.Code != 200 {
		t.Fatal("read credential", rec.Code)
	}
	if rec := e.do(t, "GET", historyPath, nil, nil); rec.Code != 200 {
		t.Fatal("read history credential", rec.Code)
	}
	mfa := setupWithMFA(t, api.PlanPro, false, false)
	mfa.generateEnrolledAccount(t)
	pending := mfa.mfaIssueWithPending(t, true)
	historyReq := httptest.NewRequest("GET", historyPath, nil)
	historyReq.AddCookie(pending)
	historyOut := httptest.NewRecorder()
	mfa.h.ServeHTTP(historyOut, historyReq)
	if historyOut.Code != http.StatusForbidden {
		t.Fatal("pending MFA history", historyOut.Code)
	}
	for _, method := range []string{"GET", "POST"} {
		path := approvalPath
		if method == "GET" {
			path += "/" + receipt.ID
		}
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(pending)
		out := httptest.NewRecorder()
		mfa.h.ServeHTTP(out, req)
		if out.Code != http.StatusForbidden {
			t.Fatal("MFA bypass", method, out.Code)
		}
	}
}

func TestLifecycleSuccessorRoutingCompatibility(t *testing.T) {
	source := `{"openapi":"3.1.0","info":{"title":"source","version":"1"},"paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	destination := strings.ReplaceAll(source, "/old", "/next")
	candidate := strings.ReplaceAll(source, "/old", "/new") // Successor lives in a different captured contract.
	s := &server{domain: "gregale.dev"}
	sourceSnapshot := state.RoutePolicySnapshot{Account: state.Account{Plan: api.PlanPro}, App: state.App{ID: "source", Slug: "source"}}
	targetSnapshot := state.RoutePolicySnapshot{Account: sourceSnapshot.Account, App: state.App{ID: "target", Slug: "target"}, Contract: &state.RoutePolicyContract{DeploymentID: "target-deployment", SHA256: strings.Repeat("c", 64), Doc: []byte(destination)}}
	for _, scenario := range []string{"canonical", "verified_domain", "unverified_domain", "tenant_claim", "rewrite", "redirect", "route", "conditional_route", "disabled_rule", "incompatible_target", "missing_target", "declared_route_gate", "stronger_app_auth", "reserved_preview_host"} {
		t.Run(scenario, func(t *testing.T) {
			snapshot := sourceSnapshot
			checker := &server{domain: s.domain}
			target := targetSnapshot
			mapping := api.RouteLifecycleMapping{Method: "GET", Path: "/old", SuccessorMethod: "GET", SuccessorPath: "/next", SuccessorURL: "https://target.gregale.dev/next", SuccessorAppID: "target", SuccessorDeploymentID: "target-deployment", SuccessorContractSHA256: strings.Repeat("c", 64)}
			entry := state.RouteLifecycleSuccessor{Snapshot: target}
			switch scenario {
			case "verified_domain", "unverified_domain":
				mapping.SuccessorURL = "https://api.example.test/next"
				entry.VerifiedDomain = scenario == "verified_domain"
			case "reserved_preview_host":
				checker.domain = "example.test"
				mapping.SuccessorURL = "https://deploy-1-target.gregale.dev/next"
				entry.VerifiedDomain = true
			case "tenant_claim":
				entry.Claimed = true
			case "declared_route_gate":
				entry.Snapshot.App.OnlyAllowDeclaredRoutes = true
			case "stronger_app_auth":
				entry.Snapshot.App.ConsumerAuthMode = api.ConsumerAuthModeRequired
			case "rewrite", "redirect", "route", "conditional_route", "disabled_rule":
				kind := scenario
				if scenario == "conditional_route" || scenario == "disabled_rule" {
					kind = "route"
				}
				entry.Snapshot.Rules = []api.EdgeRuleResponse{{Enabled: scenario != "disabled_rule", Kind: kind, MatchHost: "*", MatchPath: "*", MatchMethods: []string{"GET"}}}
				if scenario == "conditional_route" {
					entry.Snapshot.Rules[0].MatchHeaders = map[string]string{"X-Variant": "other"}
				}
			case "incompatible_target":
				copy := *target.Contract
				copy.Doc = []byte(strings.Replace(destination, `"get":{`, `"get":{"parameters":[{"name":"required","in":"query","required":true,"schema":{"type":"string"}}],`, 1))
				entry.Snapshot.Contract = &copy
			}
			snapshot.LifecycleSuccessors = map[string]state.RouteLifecycleSuccessor{"GET /old": entry}
			if scenario == "missing_target" {
				snapshot.LifecycleSuccessors = nil
			}
			err := checker.validateRouteLifecycleCompatibility(snapshot, &state.RoutePolicyContract{Doc: []byte(source)}, &state.RoutePolicyContract{Doc: []byte(candidate)}, []api.RouteLifecycleMapping{mapping})
			allowed := scenario == "canonical" || scenario == "verified_domain" || scenario == "disabled_rule"
			if (err == nil) != allowed {
				t.Fatalf("allowed=%v: %v", allowed, err)
			}
		})
	}
}

func TestLifecycleHistoryOwnership(t *testing.T) {
	e := setup(t, api.PlanPro)
	owned := seedApp(t, e, "history-owned")
	foreign, err := e.store.CreateAccount(t.Context(), "history-foreign@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.store.CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "history-foreign", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "GET", "/v1/apps/"+owned.Slug+"/route-lifecycle/history", nil, nil)
	var page api.RouteLifecycleHistoryPage
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || page.AppID != owned.ID || len(page.Entries) != 0 || page.Entries == nil {
		t.Fatal("empty history", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "GET", "/v1/apps/"+other.Slug+"/route-lifecycle/history", nil, nil); rec.Code != 404 {
		t.Fatal("foreign history", rec.Code, rec.Body.String())
	}
}
