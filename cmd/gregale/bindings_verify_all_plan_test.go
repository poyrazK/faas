package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const allPreviewOutboundID = "0805c931-a506-4396-95b9-72c0a6b15099"

type allProbePreviewFixture struct {
	t               *testing.T
	server          *httptest.Server
	mu              sync.Mutex
	inventory       api.AppBindingInventory
	outboundItems   []api.OutboundAppBinding
	outboundFailure bool
	allowExecution  bool
	calls           []string
	actions         []string
}

func newAllProbePreviewFixture(t *testing.T) *allProbePreviewFixture {
	t.Helper()
	f := &allProbePreviewFixture{t: t, inventory: api.AppBindingInventory{App: "api", Complete: true, VerificationDeploymentID: pinnedBindingDeployment, RequestedDeploymentID: pinnedBindingDeployment, VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{
		{Type: api.BindingTypeService, Name: "billing", Binding: "GREGALE_SERVICE_BILLING_URL", Scope: "app", HTTPURL: "http://PRIVATE_ORIGIN/"},
		{Type: api.BindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: "production"},
		{Type: api.BindingTypeObjectStorage, Name: "read", Binding: "READ", Scope: "production", Access: "read"},
		{Type: api.BindingTypeObjectStorage, Name: "write", Binding: "WRITE", Scope: "production", Access: "write"},
		{Type: api.BindingTypeObjectStorage, Name: "both", Binding: "BOTH", Scope: "production", Access: "read_write"},
		{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: "app", Access: "push", State: "enabled", ConsumerState: "active", QueueCanarySupported: true},
		{Type: api.BindingTypeOutbound, Name: "payments", Scope: "app", OutboundProbe: &api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200}},
		{Type: api.BindingTypeQueue, Name: "disabled", Binding: "disabled", Scope: "app", State: "disabled"},
		{Type: api.BindingTypePostgres, Name: "staging", Binding: "STAGING_URL", Scope: "staging"},
		{Type: api.BindingTypePostgres, Name: "migration", Binding: "MIGRATE_URL", Scope: "production", Access: "migration"},
	}}, outboundItems: []api.OutboundAppBinding{{Integration: api.OutboundIntegrationOffer{ID: allPreviewOutboundID, Name: "payments"}}}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *allProbePreviewFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	allowed := f.allowExecution
	inventory, items, lookupFailure := f.inventory, f.outboundItems, f.outboundFailure
	f.mu.Unlock()
	if r.Method != http.MethodGet && !allowed {
		f.t.Errorf("preview attempted mutation: %s %s", r.Method, r.URL)
		http.Error(w, "preview write", 403)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
		if r.URL.Query().Get("deployment_id") != pinnedBindingDeployment {
			f.t.Errorf("unpinned inventory %s", r.URL)
		}
		writeJSONTest(w, inventory)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/outbound-bindings":
		if lookupFailure {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"title":"PRIVATE_LOOKUP_DETAIL","status":403}`))
			return
		}
		writeJSONTest(w, api.OutboundAppBindingList{Items: items})
	case allowed && r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
		var request api.CreateAppTaskRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			f.t.Error(err)
			http.Error(w, "bad task", 400)
			return
		}
		if request.VerificationDeploymentID != pinnedBindingDeployment || len(request.Command) < 2 {
			f.t.Errorf("bad task %+v", request)
			http.Error(w, "bad task", 400)
			return
		}
		var kind, name, mode string
		var report any
		switch request.Command[0] {
		case api.AppTaskServiceBindingProbeCommand:
			kind, name, mode = api.BindingTypeService, "billing", "https_route"
			report = json.RawMessage(pinnedBindingProbeOutput(request.Command[0], request.Command[1]))
		case api.AppTaskPostgresBindingProbeCommand:
			kind, name, mode = api.BindingTypePostgres, "primary", "query"
			report = json.RawMessage(pinnedBindingProbeOutput(request.Command[0], request.Command[1]))
		case api.AppTaskOutboundBindingProbeCommand:
			if request.Command[1] != allPreviewOutboundID {
				f.t.Errorf("wrong outbound identity: %v", request.Command)
			}
			kind, name, mode = api.BindingTypeOutbound, "payments", "configured_request"
			check := api.OutboundBindingProbeCheck{Status: "passed"}
			report = api.OutboundBindingProbeReport{IntegrationID: allPreviewOutboundID, Configuration: check, Identity: check, Gateway: check, Response: check}
		case api.AppTaskObjectStorageBindingProbeCommand, api.AppTaskObjectStorageWriteProbeCommand:
			kind, name, mode = api.BindingTypeObjectStorage, map[string]string{"READ": "read", "WRITE": "write", "BOTH": "both"}[request.Command[1]], "read"
			storage := passedObjectStorageCLIReport(request.Command[1])
			if len(request.Command) == 3 {
				mode = request.Command[2]
				storage.Mode = mode
				check := api.ObjectStorageBindingProbeCheck{Status: "passed"}
				storage.Upload = &check
				storage.Cleanup = &check
				download := check
				if mode == "write" {
					download.Status = "not_checked"
				}
				storage.Download = &download
			}
			report = storage
		default:
			f.t.Errorf("unexpected command %v", request.Command)
			http.Error(w, "bad task", 400)
			return
		}
		f.mu.Lock()
		f.actions = append(f.actions, kind+"/"+name+"/"+mode)
		f.mu.Unlock()
		raw, _ := json.Marshal(report)
		writeJSONTest(w, api.AppTaskResponse{ID: uuid.NewString(), DeploymentID: pinnedBindingDeployment, DeploymentScope: "production", Status: api.AppTaskStatusSucceeded, ExitCode: new(int), StdoutTail: string(raw)})
	case allowed && r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/bindings/queue-canaries":
		var request api.CreateQueueBindingCanaryRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			f.t.Error(err)
		}
		if !request.PublishConsent || request.Binding != "orders" || request.DeploymentID != pinnedBindingDeployment {
			f.t.Errorf("wrong canary request %+v", request)
		}
		f.mu.Lock()
		f.actions = append(f.actions, "queue/orders/handler_completion")
		f.mu.Unlock()
		now := time.Now().UTC().Add(-time.Second)
		writeJSONTest(w, api.QueueBindingCanary{ID: uuid.NewString(), Binding: "orders", DeploymentID: pinnedBindingDeployment, Scope: "production", Status: "succeeded", PublishedAt: now, ExpiresAt: now.Add(time.Minute), DeliveredAt: &now, CompletedAt: &now})
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

func allPreviewArgs(consent, strict, asJSON bool) []string {
	args := []string{"bindings", "verify", "api", "--all", "--deployment", pinnedBindingDeployment, "--dry-run"}
	if consent {
		args = append(args, "--queue-publish", "--object-storage-write")
	}
	if strict {
		args = append(args, "--require-complete")
	}
	if asJSON {
		args = append(args, "--json")
	}
	return args
}

func TestAllBindingPreviewReadOnlyAndReadiness(t *testing.T) {
	for _, scenario := range []string{"complete", "missing consent", "partial default", "queue unsupported", "storage unknown", "duplicate queue", "duplicate storage", "incomplete inventory", "inventory error", "inventory warning", "unknown type", "missing outbound identity", "outbound read failure", "no eligible", "human"} {
		t.Run(scenario, func(t *testing.T) {
			f := newAllProbePreviewFixture(t)
			f.mu.Lock()
			consent, strict := true, true
			ready, complete, coverage := true, true, "complete"
			reason := ""
			switch scenario {
			case "missing consent":
				consent = false
				ready, complete, coverage = false, false, "partial"
				reason = "queue_publish_consent_required"
			case "partial default":
				consent, strict = false, false
				complete, coverage = false, "partial"
				reason = "write_probe_requires_opt_in"
			case "queue unsupported":
				f.inventory.Bindings[5].QueueCanarySupported = false
				ready, complete, coverage = false, false, "partial"
				reason = "queue_handler_unsupported"
			case "storage unknown":
				f.inventory.Bindings[4].Access = "future"
				ready, complete, coverage = false, false, "partial"
				reason = "object_storage_access_unsupported"
			case "duplicate queue":
				f.inventory.Bindings = append(f.inventory.Bindings, f.inventory.Bindings[5])
				ready, complete, coverage = false, false, "partial"
				reason = "queue_binding_ambiguous"
			case "duplicate storage":
				f.inventory.Bindings = append(f.inventory.Bindings, f.inventory.Bindings[4])
				ready, complete, coverage = false, false, "partial"
				reason = "object_storage_binding_ambiguous"
			case "incomplete inventory":
				f.inventory.Complete = false
				ready, complete, coverage = false, false, "partial"
				reason = "inventory_incomplete"
			case "inventory error":
				f.inventory.Issues = []api.BindingInventoryIssue{{Type: api.BindingTypePostgres, Severity: "error", Code: "query_failed"}}
				ready, complete, coverage = false, false, "partial"
				reason = "inventory_issue"
			case "inventory warning":
				f.inventory.Issues = []api.BindingInventoryIssue{{Severity: "warning", Code: "observation_missing"}}
			case "unknown type":
				strict = false
				f.inventory.Bindings = append(f.inventory.Bindings, api.AppBindingInventoryItem{Type: "future", Name: "new", Scope: "app"})
				ready, complete, coverage = false, false, "partial"
				reason = "unknown_binding_type"
			case "missing outbound identity":
				f.outboundItems = nil
				ready, complete, coverage = false, false, "partial"
				reason = "probe_not_checked"
			case "outbound read failure":
				f.outboundFailure = true
				ready, complete, coverage = false, false, "partial"
				reason = "inventory_issue"
			case "no eligible":
				f.inventory.Bindings = f.inventory.Bindings[7:]
				ready, complete, coverage = false, false, "none"
				reason = "no_eligible_bindings"
			}
			f.mu.Unlock()
			code, out, errs := captureBindingCLI(t, f.server.URL, allPreviewArgs(consent, strict, scenario != "human")...)
			wantCode := 0
			if !ready {
				wantCode = 1
			}
			if code != wantCode {
				t.Fatalf("code=%d want=%d output=%s errors=%s", code, wantCode, out, errs)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, call := range f.calls {
				if !strings.HasPrefix(call, "GET ") {
					t.Fatalf("mutation %s", call)
				}
			}
			if len(f.actions) != 0 {
				t.Fatalf("preview ran probes: %v", f.actions)
			}
			if scenario == "human" {
				for _, text := range []string{"read-only", "Plan ready: true", "mode=read_write", "--queue-publish provided=true", "--object-storage-write provided=true"} {
					if !strings.Contains(out, text) {
						t.Fatalf("missing %s: %s", text, out)
					}
				}
				if strings.Contains(out, "gate: passed") {
					t.Fatal("preview claimed verification")
				}
				return
			}
			for _, private := range []string{"PRIVATE_ORIGIN", "PRIVATE_LOOKUP_DETAIL"} {
				if strings.Contains(out, private) {
					t.Fatalf("private metadata leaked: %s", out)
				}
			}
			var report allBindingProbePreview
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatal(err)
			}
			if !report.DryRun || report.PlanReady != ready || report.CompleteCoveragePossible != complete || report.PlannedCoverage != coverage || report.RequireComplete != strict || report.Total != len(report.Plan) || report.Excluded != 3 {
				t.Fatalf("report=%s", out)
			}
			if reason != "" {
				found := false
				for _, blocker := range report.CoverageBlockers {
					if blocker.Code == reason {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing %s: %s", reason, out)
				}
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &raw); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"passed", "checked", "gate", "bindings", "verification"} {
				if _, exists := raw[field]; exists {
					t.Fatalf("preview includes execution field %s", field)
				}
			}
			for _, step := range report.Plan {
				if step.Status == "passed" || step.Status == "failed" {
					t.Fatalf("preview claimed probe outcome: %+v", step)
				}
			}
			if scenario == "complete" {
				if report.Planned != 7 || report.Blocked != 0 || len(report.CoverageBlockers) != 0 {
					t.Fatalf("report=%s", out)
				}
				modes := map[string]string{}
				for _, step := range report.Plan {
					modes[step.Binding] = step.ProbeMode
					if step.Name == "payments" && (step.Method != "GET" || step.Path != "/health" || step.ExpectedStatus != 200) {
						t.Fatalf("missing outbound request: %+v", step)
					}
				}
				if modes["READ"] != "read" || modes["WRITE"] != "write" || modes["BOTH"] != "read_write" {
					t.Fatalf("modes=%v", modes)
				}
			}
			if scenario == "missing consent" || scenario == "partial default" {
				missing := map[string]bool{}
				for _, step := range report.Plan {
					if step.Consent != nil && !step.Consent.Provided {
						missing[step.Consent.Flag] = true
					}
					if step.Binding == "BOTH" && step.ProbeMode != "read" {
						t.Fatalf("implicit write plan: %+v", step)
					}
				}
				if !missing["--queue-publish"] || !missing["--object-storage-write"] {
					t.Fatalf("missing consent requirements: %s", out)
				}
			}
		})
	}
}

func TestAllBindingPreviewMatchesExecution(t *testing.T) {
	for _, consent := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "complete"}[consent], func(t *testing.T) {
			f := newAllProbePreviewFixture(t)
			args := allPreviewArgs(consent, consent, true)
			code, out, errs := captureBindingCLI(t, f.server.URL, args...)
			if code != 0 {
				t.Fatalf("preview code=%d output=%s errors=%s", code, out, errs)
			}
			var preview allBindingProbePreview
			if err := json.Unmarshal([]byte(out), &preview); err != nil {
				t.Fatal(err)
			}
			expected := []string{}
			for _, step := range preview.Plan {
				if step.Status == "planned" {
					expected = append(expected, step.Type+"/"+step.Name+"/"+step.ProbeMode)
				}
			}
			f.mu.Lock()
			if len(f.actions) != 0 {
				t.Fatal("preview executed probes")
			}
			f.allowExecution = true
			f.mu.Unlock()
			executeArgs := []string{}
			for _, arg := range args {
				if arg != "--dry-run" {
					executeArgs = append(executeArgs, arg)
				}
			}
			code, out, errs = captureBindingCLI(t, f.server.URL, executeArgs...)
			if code != 0 {
				t.Fatalf("execution code=%d output=%s errors=%s", code, out, errs)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if !reflect.DeepEqual(f.actions, expected) {
				t.Fatalf("preview actions=%v execution=%v", expected, f.actions)
			}
			var batch serviceBindingProbeBatchReport
			if err := json.Unmarshal([]byte(out), &batch); err != nil {
				t.Fatal(err)
			}
			if batch.Coverage != preview.PlannedCoverage || batch.Passed != preview.Planned {
				t.Fatalf("preview=%+v execution=%+v", preview, batch)
			}
		})
	}
}

func TestAllBindingPreviewRejectsInvalidModesBeforeRequests(t *testing.T) {
	f := newAllProbePreviewFixture(t)
	for _, args := range [][]string{
		{"bindings", "verify", "api", "billing", "--dry-run"},
		{"bindings", "verify", "api", "--queue", "orders", "--deployment", pinnedBindingDeployment, "--dry-run"},
		{"bindings", "verify", "api", "--all", "--durable", "--dry-run"},
		{"bindings", "verify", "api", "--all", "--queue-publish", "--dry-run"},
		{"bindings", "verify", "api", "--all", "--object-storage-write", "--dry-run"},
	} {
		if code, out, _ := captureBindingCLI(t, f.server.URL, args...); code == 0 {
			t.Fatalf("invalid mode accepted: %v %s", args, out)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 {
		t.Fatalf("invalid mode made API requests: %v", f.calls)
	}
}

func TestAllBindingPreviewCannotSatisfyExecutionGate(t *testing.T) {
	batch := serviceBindingProbeBatchReport{Bindings: []serviceBindingProbeBatchItem{{Type: api.BindingTypeService, Name: "billing", Status: "planned"}}}
	if gate := evaluateAllBindingCoverageGate(batch, true); gate.Passed {
		t.Fatal("planned probes satisfied the execution gate")
	}
	batch.Bindings[0].Status = "passed"
	if potential := evaluateAllBindingCoverage(batch, true, "planned"); potential.Passed {
		t.Fatal("preview accepted execution statuses as planned work")
	}
}
