// adr: 20261119190000
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

// expFixture serves a project environment with experiments and the event
// schema registry.
type expFixture struct {
	mu        sync.Mutex
	creates   []api.FlagExperimentCreateRequest
	ships     []string
	keys      []string
	schemas   []api.RegisterEventSchemaRequest
	approvals bool
}

func newExpFixture(t *testing.T, allowed map[string]string) (*management, *expFixture) {
	t.Helper()
	f := &expFixture{}
	b := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(b.Close)
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL, Token: testToken, AllowWrites: true, AllowedApps: allowed, Toolsets: []string{ToolsetAll}})
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}

func (f *expFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	const base = "/v1/projects/shop/environments/production"
	switch path := r.URL.Path; {
	case path == "/v1/account":
		encode(api.AccountResponse{ID: "acct-1"})
	case path == "/v1/projects/shop":
		encode(map[string]any{"id": "proj-1", "slug": "shop", "workloads": []any{}})
	case path == base:
		encode(api.ProjectEnvironmentResponse{ID: "env-1", ProjectID: "proj-1", Slug: "production"})
	case path == base+"/experiments" && r.Method == http.MethodPost:
		var req api.FlagExperimentCreateRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.creates = append(f.creates, req)
		encode(api.FlagExperiment{Key: req.Key})
	case path == base+"/experiments/exp-1":
		encode(api.FlagExperiment{Key: "exp-1", Flag: "checkout"})
	case path == base+"/experiments/exp-1/results":
		encode(api.FlagExperimentResults{})
	case path == base+"/experiments/exp-1/ship":
		if f.approvals {
			api.WriteProblem(w, api.NewProblem(http.StatusPreconditionRequired, "approval_required", "Approval required", "ask a person"))
			return
		}
		var req api.FlagExperimentShipRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.ships = append(f.ships, req.Variant)
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		encode(api.FlagExperimentShipResponse{})
	case path == "/v1/event-schemas:preview-rollout":
		var req api.EventSchemaRolloutRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := api.EventSchemaRolloutResponse{Source: req.Source, Type: req.Type, Version: req.Version}
		for _, s := range req.Samples {
			if strings.Contains(string(s), "bad") {
				out.SampleInvalidCount++
			} else {
				out.SampleValidCount++
			}
		}
		encode(out)
	case path == "/v1/event-schemas" && r.Method == http.MethodPost:
		var req api.RegisterEventSchemaRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.schemas = append(f.schemas, req)
		encode(api.RegisterEventSchemaResponse{})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestExperimentCreateNeverStartsAndShipIsKeyed(t *testing.T) {
	m, f := newExpFixture(t, nil)
	create := map[string]any{"key": "exp-2", "flag": "checkout", "rule_id": "r1", "control": "off", "start": true}
	plan, err := m.planExperimentChange(t.Context(), planExperimentArgs{Project: "shop", Environment: "production", Op: "create", Create: create, Reason: "test new checkout"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyReviewedChange(t.Context(), applyArgs{PlanID: plan.(reviewedPlan).ID}); err != nil {
		t.Fatal(err)
	}
	if len(f.creates) != 1 || f.creates[0].Start {
		t.Fatalf("creates = %+v", f.creates)
	}
	if _, err := m.planExperimentChange(t.Context(), planExperimentArgs{Project: "shop", Environment: "production", Op: "create", Create: map[string]any{"key": "x", "webhook": "y"}, Reason: "x"}); err == nil {
		t.Fatal("unknown create field accepted")
	}
	plan, err = m.planExperimentChange(t.Context(), planExperimentArgs{Project: "shop", Environment: "production", Op: "ship", Key: "exp-1", Variant: "on", Reason: "winner"})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(reviewedPlan).ID
	f.mu.Lock()
	f.approvals = true
	f.mu.Unlock()
	if _, err := m.applyReviewedChange(t.Context(), applyArgs{PlanID: id}); err == nil || !strings.Contains(err.Error(), "approval") {
		t.Fatalf("ship without approval: %v", err)
	}
	f.mu.Lock()
	f.approvals = false
	f.mu.Unlock()
	for range 2 {
		if _, err := m.applyReviewedChange(t.Context(), applyArgs{PlanID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.ships) != 1 || f.ships[0] != "on" || f.keys[0] != "mcp-reviewed-"+id {
		t.Fatalf("ships = %v keys = %v", f.ships, f.keys)
	}
}

func TestSchemaRegistrationNeedsValidSamplesAndEveryApp(t *testing.T) {
	m, f := newExpFixture(t, nil)
	schema := map[string]any{"type": "object"}
	if _, err := m.planEventSchemaRegistration(t.Context(), planSchemaArgs{Source: "app:orders", Type: "order.created", Version: "2", Schema: schema, Samples: []map[string]any{{"id": "bad"}}, Reason: "v2"}); err == nil {
		t.Fatal("invalid sample accepted")
	}
	plan, err := m.planEventSchemaRegistration(t.Context(), planSchemaArgs{Source: "app:orders", Type: "order.created", Version: "2", Schema: schema, Samples: []map[string]any{{"id": "ok"}}, Reason: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.schemas) != 0 {
		t.Fatal("planning registered")
	}
	if _, err := m.applyReviewedChange(t.Context(), applyArgs{PlanID: plan.(reviewedPlan).ID}); err != nil {
		t.Fatal(err)
	}
	if len(f.schemas) != 1 || f.schemas[0].Version != "2" {
		t.Fatalf("schemas = %+v", f.schemas)
	}
	limited, _ := newExpFixture(t, map[string]string{"alpha": "app-one"})
	if _, err := limited.planEventSchemaRegistration(t.Context(), planSchemaArgs{Source: "app:orders", Type: "t", Version: "3", Schema: schema, Reason: "x"}); err == nil {
		t.Fatal("registration on a limited connection accepted")
	}
}

// workflowFixture serves a saved workflow, its publish route, subscription
// schema versions and a custom domain for app alpha.
type workflowFixture struct {
	*operationFixture
	wmu        sync.Mutex
	report     *api.StoredWorkflowTestReport
	publishes  []int64
	drafts     int
	versionSet [][]string
}

func newWorkflowFixture(t *testing.T) (*management, *workflowFixture) {
	t.Helper()
	f := &workflowFixture{operationFixture: &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}}
	b := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(b.Close)
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL, Token: testToken, AllowWrites: true, agentKey: true, Toolsets: []string{ToolsetAll}})
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}

func (f *workflowFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch path := r.URL.Path; {
	case path == "/v1/apps/alpha/workflows/stored/orders" && r.Method == http.MethodGet:
		encode(api.StoredWorkflowResponse{Name: "orders", Version: 5, PublishedVersion: 4, TestSuite: &api.StoredWorkflowTestSuite{Cases: []api.StoredWorkflowTestCase{{Name: "happy"}}}, TestReport: f.report})
	case path == "/v1/apps/alpha/workflows/stored/orders" && r.Method == http.MethodPut:
		f.drafts++
		encode(api.StoredWorkflowResponse{Name: "orders", Version: 6, PublishedVersion: 4})
	case path == "/v1/apps/alpha/workflows/stored/orders/publish":
		var req api.PublishStoredWorkflowRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.publishes = append(f.publishes, req.ExpectedVersion)
		encode(api.StoredWorkflowResponse{Name: "orders", Version: 5, PublishedVersion: 5})
	case path == "/v1/apps/alpha/event-subscriptions":
		encode(api.EventSubscriptionListResponse{AppSlug: "alpha", Subscriptions: []api.EventSubscriptionResponse{{ID: "sub-1"}}})
	case strings.HasSuffix(path, "/event-subscriptions/sub-1/schema-versions") && r.Method == http.MethodPut:
		var body struct {
			Versions []string `json:"schema_versions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.versionSet = append(f.versionSet, body.Versions)
		encode(api.EventSubscriptionSchemaVersionsResponse{})
	case strings.HasSuffix(path, "/event-subscriptions/sub-1/schema-versions"):
		encode(api.EventSubscriptionSchemaVersionsResponse{})
	case path == "/v1/domains/shop.example.com":
		encode(api.CustomDomainResponse{Domain: "shop.example.com", AppID: f.base.app.ID})
	case path == "/v1/domains/other.example.com":
		encode(api.CustomDomainResponse{Domain: "other.example.com", AppID: "another-app"})
	case path == "/v1/domains/shop.example.com/doctor":
		encode(api.DomainDoctorReport{Domain: "shop.example.com", Checks: []api.DomainDoctorCheck{{Name: "dns_record", Status: "ok"}, {Name: "tls_certificate", Status: "fail", Remediation: "add CAA"}}})
	default:
		f.wmu.Unlock()
		f.serveHTTP(w, r)
		f.wmu.Lock()
	}
}

func TestWorkflowPublishNeedsPassingTestsOnTheDraft(t *testing.T) {
	m, f := newWorkflowFixture(t)
	if _, err := m.planWorkflowPublish(t.Context(), planWorkflowPublishArgs{App: "alpha", Workflow: "orders", Reason: "ship"}); err == nil || !strings.Contains(err.Error(), "tests") {
		t.Fatalf("publish without a report: %v", err)
	}
	f.wmu.Lock()
	f.report = &api.StoredWorkflowTestReport{CheckedVersion: 4, Passed: true}
	f.wmu.Unlock()
	if _, err := m.planWorkflowPublish(t.Context(), planWorkflowPublishArgs{App: "alpha", Workflow: "orders", Reason: "ship"}); err == nil {
		t.Fatal("report for an older version accepted")
	}
	f.wmu.Lock()
	f.report = &api.StoredWorkflowTestReport{CheckedVersion: 5, Passed: true}
	f.wmu.Unlock()
	plan, err := m.planWorkflowPublish(t.Context(), planWorkflowPublishArgs{App: "alpha", Workflow: "orders", Reason: "ship"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err != nil {
		t.Fatal(err)
	}
	if len(f.publishes) != 1 || f.publishes[0] != 5 {
		t.Fatalf("publishes = %v", f.publishes)
	}
	if _, err := m.saveWorkflowDraft(t.Context(), saveWorkflowDraftArgs{App: "alpha", Workflow: "orders", ExpectedVersion: 5, Definition: map[string]any{"not_a_field": 1}}); err == nil {
		t.Fatal("unknown definition field accepted")
	}
}

func TestSubscriptionSchemaVersionsAndDomainDiagnosis(t *testing.T) {
	m, f := newWorkflowFixture(t)
	plan, err := m.planEventChange(t.Context(), planEventArgs{App: "alpha", Op: "set_schema_versions", Subscription: "sub-1", Versions: []string{"2"}, Reason: "v2 ready"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err != nil {
		t.Fatal(err)
	}
	if len(f.versionSet) != 1 || f.versionSet[0][0] != "2" {
		t.Fatalf("versions = %v", f.versionSet)
	}
	if _, err := m.planEventChange(t.Context(), planEventArgs{App: "alpha", Op: "reset_schema_versions", Subscription: "sub-1", Versions: []string{"2"}, Reason: "x"}); err == nil {
		t.Fatal("reset with versions accepted")
	}
	out, err := m.diagnoseDomain(t.Context(), domainArgs{Domain: "Shop.Example.com."})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(out); !strings.Contains(string(raw), `"needs_attention":[{"name":"tls_certificate"`) {
		t.Fatalf("diagnosis = %s", raw)
	}
	m.opts.AllowedApps = map[string]string{"alpha": f.base.app.ID}
	if _, err := m.diagnoseDomain(t.Context(), domainArgs{Domain: "other.example.com"}); err == nil {
		t.Fatal("another app's domain accepted")
	}
	if _, err := m.diagnoseDomain(t.Context(), domainArgs{Domain: "bad domain"}); err == nil {
		t.Fatal("invalid domain accepted")
	}
}
