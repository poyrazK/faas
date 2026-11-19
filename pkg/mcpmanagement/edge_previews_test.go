// adr: 20261118140000
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// edgeFixture serves edge rule, preview, bucket and saved workflow routes for
// app alpha on top of the operation fixture.
type edgeFixture struct {
	*operationFixture
	emu        sync.Mutex
	rulesSHA   string
	creates    []api.CreateEdgeRuleRequest
	deletes    []string
	destroyed  []string
	objectURL  string
	testRuns   []int64
	objectHits int
}

func newEdgeFixture(t *testing.T, capabilities []string) (*management, *edgeFixture) {
	t.Helper()
	f := &edgeFixture{operationFixture: &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}, rulesSHA: "sha-1"}
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.emu.Lock()
		f.objectHits++
		f.emu.Unlock()
		if r.Header.Get("Authorization") != "" || r.URL.Query().Get("sig") != "ok" {
			http.Error(w, "bad", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Range", "bytes 0-4/11")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(store.Close)
	f.objectURL = store.URL + "/obj?sig=ok"
	backend := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(backend.Close)
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, agentKey: true, Capabilities: capabilities, Toolsets: []string{ToolsetAll}})
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}

func (f *edgeFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.emu.Lock()
	defer f.emu.Unlock()
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	appID := f.base.app.ID
	preview := api.AppResponse{ID: "app-pr7", Slug: "alpha-pr-7", Status: "active", PreviewOfSlug: "alpha", PreviewPRNumber: 7}
	other := api.AppResponse{ID: "app-beta-pr", Slug: "beta-pr-1", Status: "active", PreviewOfSlug: "beta", PreviewPRNumber: 1}
	path := r.URL.Path
	switch {
	case path == "/v1/apps/alpha/edge-rules" && r.Method == http.MethodGet:
		encode([]api.EdgeRuleResponse{{ID: "rule-1", AppID: appID, Kind: "block"}})
	case path == "/v1/apps/alpha/edge-rules" && r.Method == http.MethodPost:
		var req api.CreateEdgeRuleRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.creates = append(f.creates, req)
		encode(api.EdgeRuleResponse{ID: "rule-new", AppID: appID, Kind: req.Kind})
	case path == "/v1/apps/alpha/edge-protection/attack-mode":
		encode(api.EdgeAttackModeStatus{})
	case path == "/v1/apps/alpha/edge-rules/versions":
		encode([]api.EdgeRuleSetVersionResponse{{Version: 3, RulesSHA256: f.rulesSHA, Current: true}, {Version: 2, RulesSHA256: "old"}})
	case path == "/v1/apps/alpha/edge-rules/stats":
		encode(api.EdgeRuleStatsResponse{Window: "24h"})
	case path == "/v1/apps/alpha/edge-rules/events":
		encode(api.EdgeRuleEventsResponse{Events: []api.EdgeRuleEventResponse{{ID: "ev", RuleID: "rule-1", Outcome: "blocked", ClientIP: "203.0.113.9", UserAgent: "curl/8"}}})
	case path == "/v1/edge-rules/rule-1" && r.Method == http.MethodGet:
		encode(api.EdgeRuleResponse{ID: "rule-1", AppID: appID, Kind: "block"})
	case path == "/v1/edge-rules/rule-attack":
		encode(api.EdgeRuleResponse{ID: "rule-attack", AppID: appID, Kind: "challenge", ManagedBy: api.EdgeRuleManagedByAttackMode})
	case path == "/v1/edge-rules/rule-other":
		encode(api.EdgeRuleResponse{ID: "rule-other", AppID: "another-app", Kind: "block"})
	case path == "/v1/edge-rules/rule-1" && r.Method == http.MethodDelete:
		f.deletes = append(f.deletes, "rule-1")
		w.WriteHeader(http.StatusNoContent)
	case path == "/v1/apps" && r.Method == http.MethodGet:
		encode([]api.AppResponse{f.base.app, preview, other})
	case path == "/v1/preview/alpha-pr-7":
		encode(api.PreviewResourceResponse{App: preview})
	case path == "/v1/preview/beta-pr-1":
		encode(api.PreviewResourceResponse{App: other})
	case path == "/v1/preview/alpha-pr-7/destroy":
		f.destroyed = append(f.destroyed, "alpha-pr-7")
		w.WriteHeader(http.StatusNoContent)
	case path == "/v1/apps/alpha/buckets":
		encode(api.ObjectBucketList{Items: []api.ObjectBucket{{Name: "uploads"}}})
	case path == "/v1/apps/alpha/buckets/uploads/objects":
		encode(api.BucketObjectPage{Items: []api.BucketObject{{Key: "a.txt", SizeBytes: 11}}})
	case path == "/v1/apps/alpha/buckets/uploads/signed-url":
		encode(api.ObjectSignedRequest{URL: f.objectURL, Method: http.MethodGet})
	case path == "/v1/apps/alpha/workflows/stored/orders":
		encode(api.StoredWorkflowResponse{Name: "orders", Version: 4, TestSuite: &api.StoredWorkflowTestSuite{Cases: []api.StoredWorkflowTestCase{{Name: "happy"}}}})
	case path == "/v1/apps/alpha/workflows/stored/orders/tests/run":
		var req api.RunStoredWorkflowTestsRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.testRuns = append(f.testRuns, req.ExpectedVersion)
		encode(api.StoredWorkflowTestReport{CheckedVersion: req.ExpectedVersion, Passed: true})
	default:
		f.emu.Unlock()
		f.serveHTTP(w, r)
		f.emu.Lock()
	}
}

func TestEdgeActivityRedactsEndUsersWithoutDataConsent(t *testing.T) {
	m, _ := newEdgeFixture(t, nil)
	out, err := m.getEdgeActivity(t.Context(), edgeActivityArgs{App: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "203.0.113.9") || strings.Contains(string(raw), "curl/8") || !strings.Contains(string(raw), "blocked") {
		t.Fatalf("redacted activity = %s", raw)
	}
	m, _ = newEdgeFixture(t, []string{api.ScopeDataRead})
	out, err = m.getEdgeActivity(t.Context(), edgeActivityArgs{App: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(out); !strings.Contains(string(raw), "203.0.113.9") {
		t.Fatalf("data:read activity = %s", raw)
	}
}

func TestEdgeChangePlanIsPinnedToTheRuleSet(t *testing.T) {
	m, f := newEdgeFixture(t, nil)
	create := map[string]any{"kind": "block", "match_path": "/wp-admin*", "action": map[string]any{"status": 403}}
	plan, err := m.planEdgeRuleChange(t.Context(), planEdgeArgs{App: "alpha", Op: "create", Create: create, Reason: "scanner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.creates) != 0 {
		t.Fatal("planning created a rule")
	}
	f.emu.Lock()
	f.rulesSHA = "sha-2"
	f.emu.Unlock()
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err == nil || !strings.Contains(err.Error(), "changed after this plan") {
		t.Fatalf("stale apply err = %v", err)
	}
	plan, err = m.planEdgeRuleChange(t.Context(), planEdgeArgs{App: "alpha", Op: "create", Create: create, Reason: "scanner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err != nil {
		t.Fatal(err)
	}
	if len(f.creates) != 1 || f.creates[0].MatchPath != "/wp-admin*" {
		t.Fatalf("creates = %+v", f.creates)
	}
}

func TestEdgeChangeRefusesManagedAndForeignRules(t *testing.T) {
	m, f := newEdgeFixture(t, nil)
	for _, a := range []planEdgeArgs{
		{App: "alpha", Op: "delete", RuleID: "rule-attack", Reason: "x"},
		{App: "alpha", Op: "delete", RuleID: "rule-other", Reason: "x"},
		{App: "alpha", Op: "rollback", Version: 3, Reason: "x"},
		{App: "alpha", Op: "attack_mode_on", DurationSeconds: 1, Reason: "x"},
		{App: "alpha", Op: "create", RuleID: "rule-1", Create: map[string]any{"kind": "block", "action": map[string]any{}}, Reason: "x"},
		{App: "alpha", Op: "create", Create: map[string]any{"kind": "block", "action": map[string]any{}, "webhook": "x"}, Reason: "x"},
	} {
		if _, err := m.planEdgeRuleChange(t.Context(), a); err == nil {
			t.Errorf("accepted %+v", a)
		}
	}
	plan, err := m.planEdgeRuleChange(t.Context(), planEdgeArgs{App: "alpha", Op: "delete", RuleID: "rule-1", Reason: "false positive"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 1 {
		t.Fatalf("deletes = %v", f.deletes)
	}
}

func TestRetriedAppliesReplayTheirOwnOutcome(t *testing.T) {
	m, f := newEdgeFixture(t, nil)
	create := map[string]any{"kind": "block", "match_path": "/x", "action": map[string]any{"status": 403}}
	plan, err := m.planEdgeRuleChange(t.Context(), planEdgeArgs{App: "alpha", Op: "create", Create: create, Reason: "x"})
	if err != nil {
		t.Fatal(err)
	}
	p := plan.(releasePlan)
	f.emu.Lock()
	f.rulesSHA = "sha-after-our-own-change"
	f.emu.Unlock()
	if _, err := m.runEdgeChange(t.Context(), &p, true); err != nil {
		t.Fatalf("retry refused: %v", err)
	}
	if _, err := m.runEdgeChange(t.Context(), &p, false); err == nil {
		t.Fatal("first attempt against changed rules accepted")
	}
	req, _ := json.Marshal(previewChangeRequest{Op: "destroy", Preview: "alpha-pr-gone"})
	gone := releasePlan{ID: "p1", App: "alpha", Operation: &operationPlan{Kind: "preview_change", Request: req}}
	if _, err := m.runPreviewChange(t.Context(), &gone, true); err != nil {
		t.Fatalf("retried destroy of a destroyed preview: %v", err)
	}
	if _, err := m.runPreviewChange(t.Context(), &gone, false); err == nil {
		t.Fatal("first destroy of a missing preview accepted")
	}
}

func TestPreviewsStayUnderTheirParent(t *testing.T) {
	m, f := newEdgeFixture(t, nil)
	out, err := m.listPreviews(t.Context(), appArgs{App: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(out); !strings.Contains(string(raw), "alpha-pr-7") || strings.Contains(string(raw), "beta-pr-1") {
		t.Fatalf("previews = %s", raw)
	}
	if _, err := m.planPreviewChange(t.Context(), planPreviewArgs{App: "alpha", Op: "destroy", Preview: "beta-pr-1", Reason: "x"}); err == nil {
		t.Fatal("destroying another app's preview accepted")
	}
	plan, err := m.planPreviewChange(t.Context(), planPreviewArgs{App: "alpha", Op: "destroy", Preview: "alpha-pr-7", Reason: "merged"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err != nil {
		t.Fatal(err)
	}
	if len(f.destroyed) != 1 {
		t.Fatalf("destroyed = %v", f.destroyed)
	}
}

func TestReadObjectNeedsDataConsentAndHidesTheLink(t *testing.T) {
	m, f := newEdgeFixture(t, nil)
	if _, err := m.readObject(t.Context(), objectReadArgs{App: "alpha", Bucket: "uploads", Key: "a.txt"}); err == nil || !strings.Contains(err.Error(), "data:read") {
		t.Fatalf("no-consent err = %v", err)
	}
	if f.objectHits != 0 {
		t.Fatal("object fetched without consent")
	}
	m, _ = newEdgeFixture(t, []string{api.ScopeDataRead})
	if _, err := m.readObject(t.Context(), objectReadArgs{App: "alpha", Bucket: "private", Key: "a.txt"}); err == nil {
		t.Fatal("bucket outside the app accepted")
	}
	out, err := m.readObject(t.Context(), objectReadArgs{App: "alpha", Bucket: "uploads", Key: "a.txt", MaxBytes: 5})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"text":"hello"`) || !strings.Contains(string(raw), `"truncated":true`) || strings.Contains(string(raw), "sig=ok") {
		t.Fatalf("object = %s", raw)
	}
	if _, err := m.listObjects(t.Context(), objectListArgs{App: "alpha", Bucket: "uploads", Prefix: "a\x00"}); err == nil {
		t.Fatal("control character in prefix accepted")
	}
}

func TestRunWorkflowTestsPinsTheDraftVersion(t *testing.T) {
	m, f := newEdgeFixture(t, nil)
	if _, err := m.runWorkflowTests(t.Context(), workflowTestsArgs{App: "alpha", Workflow: "orders"}); err != nil {
		t.Fatal(err)
	}
	if len(f.testRuns) != 1 || f.testRuns[0] != 4 {
		t.Fatalf("runs = %v", f.testRuns)
	}
}
