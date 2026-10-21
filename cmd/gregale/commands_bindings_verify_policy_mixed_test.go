package main_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func newMixedBindingPolicyFixture(t *testing.T) *bindingPolicyCLIFixture {
	t.Helper()
	f := newBindingPolicyCLIFixture(t)
	pending, configured, production, staging := false, true, "production", "staging"
	generation, observed := int64(3), time.Now().UTC()
	probe := api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200}
	f.inventory.Bindings = append(f.inventory.Bindings,
		api.AppBindingInventoryItem{Type: api.BindingTypeService, Name: "billing", Binding: "BILLING", Scope: "app", State: "enforced", VerificationStatus: "unknown"},
		api.AppBindingInventoryItem{Type: api.BindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: production, Access: "read_write", State: "ready", CredentialGeneration: &generation, RotationPending: &pending, VerificationStatus: "unknown"},
		api.AppBindingInventoryItem{Type: api.BindingTypeOutbound, Name: "payments", Scope: "app", State: "enabled", CredentialConfigured: &configured, OutboundProbe: &probe, VerificationStatus: "unknown"},
		api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "events", Binding: "orders", Scope: production, QueueEnvironment: &production, Access: "push", State: "enabled", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &observed},
		api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "staging-events", Binding: "staging-orders", Scope: staging, QueueEnvironment: &staging, Access: "push", State: "enabled"},
		api.AppBindingInventoryItem{Type: api.BindingTypePostgres, Name: "primary", Binding: "MIGRATION_DATABASE_URL", Scope: production, Access: "migration", State: "provisioning"},
	)
	f.policy.QueueRequirements = map[string]string{"orders": api.QueueEvidenceConsumerReadiness}
	f.outboundBindings = []api.OutboundAppBinding{{AppID: releaseCheckAppID, Integration: api.OutboundIntegrationOffer{ID: uuid.NewString(), Name: "payments", Origin: "https://private-provider.invalid", Enabled: true, CredentialConfigured: true, CredentialSource: "customer_sealed"}}}
	return f
}

// Non-storage tasks supply controlled wire receipts; this does not emulate VM,
// PostgreSQL, service or outbound connectivity. S3 still uses the real fixture.
func (f *bindingPolicyCLIFixture) createNonStoragePolicyTask(w http.ResponseWriter, req api.CreateAppTaskRequest) {
	var payload any
	var item *api.AppBindingInventoryItem
	for index := range f.inventory.Bindings {
		candidate := &f.inventory.Bindings[index]
		if req.Command[0] == api.AppTaskServiceBindingProbeCommand && candidate.Type == api.BindingTypeService && candidate.Name == req.Command[1] ||
			req.Command[0] == api.AppTaskPostgresBindingProbeCommand && candidate.Type == api.BindingTypePostgres && candidate.Binding == req.Command[1] ||
			req.Command[0] == api.AppTaskOutboundBindingProbeCommand && candidate.Type == api.BindingTypeOutbound && req.Command[1] == f.outboundBindings[0].Integration.ID {
			item = candidate
			break
		}
	}
	if item == nil {
		f.t.Errorf("unexpected canary selection: %v", req.Command)
		w.WriteHeader(400)
		return
	}
	switch item.Type {
	case api.BindingTypeService:
		check := api.ServiceBindingProbeCheck{Status: "passed", Detail: "PRIVATE_DETAIL"}
		payload = api.ServiceBindingProbeReport{Service: item.Name, URL: "https://PRIVATE_PROVIDER.invalid", DNS: check, TLS: check, Authorization: check, Routing: check}
	case api.BindingTypePostgres:
		check := api.PostgresBindingProbeCheck{Status: "passed", Detail: "PRIVATE_DETAIL"}
		payload = api.PostgresBindingProbeReport{EnvironmentKey: item.Binding, Environment: check, Configuration: check, Connection: check, Query: check}
	case api.BindingTypeOutbound:
		check := api.OutboundBindingProbeCheck{Status: "passed", Detail: "PRIVATE_DETAIL"}
		payload = api.OutboundBindingProbeReport{IntegrationID: req.Command[1], Configuration: check, Identity: check, Gateway: check, Response: check}
	}
	if f.badTask == "non-storage failure" {
		payload = map[string]any{"service": item.Name, "environment_key": item.Binding, "integration_id": req.Command[1], "error": "PRIVATE_PROVIDER_ERROR"}
	}
	body, _ := json.Marshal(payload)
	f.task = api.AppTaskResponse{ID: uuid.NewString(), AppID: releaseCheckAppID, DeploymentID: releaseCheckDeploymentID, DeploymentScope: "production", Command: req.Command,
		Status: api.AppTaskStatusSucceeded, StdoutTail: string(body), ExitCode: new(int)}
	if f.queued {
		f.task.Status = api.AppTaskStatusQueued
	}
	if f.badTask == "missing exit" {
		f.task.ExitCode = nil
	}
	setBindingPolicyFixtureEvidence(item)
	_ = json.NewEncoder(w).Encode(f.task)
}

func setBindingPolicyFixtureEvidence(item *api.AppBindingInventoryItem) {
	checked := time.Now().UTC()
	var stages []string
	switch item.Type {
	case api.BindingTypeService:
		stages = []string{"dns", "tls", "authorization", "routing"}
	case api.BindingTypePostgres:
		if item.Access == "migration" {
			return
		}
		stages = []string{"environment", "configuration", "connection", "query"}
	case api.BindingTypeObjectStorage:
		stages = []string{"environment", "configuration", "connection", "authorization", "bucket_access", "upload", "cleanup", "download"}
	case api.BindingTypeOutbound:
		stages = []string{"configuration", "identity", "gateway", "response"}
	default:
		return
	}
	item.VerificationStatus = "passed"
	item.Verification = &api.BindingVerification{Result: "passed", Source: "task_guest", DeploymentID: releaseCheckDeploymentID, Scope: "production", CheckedAt: &checked, CredentialGeneration: item.CredentialGeneration}
	if item.Type == api.BindingTypeObjectStorage {
		item.Verification.ObjectStorageMode = "read_write"
	}
	for _, name := range stages {
		item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: name, Status: "passed"})
	}
}

func warmBindingPolicyFixture(f *bindingPolicyCLIFixture) {
	for index := range f.inventory.Bindings {
		setBindingPolicyFixtureEvidence(&f.inventory.Bindings[index])
	}
}

func TestBindingsVerifyReleasePolicyMixedJourneyAndReuse(t *testing.T) {
	f := newMixedBindingPolicyFixture(t)
	queue := &f.inventory.Bindings[4]
	old := time.Now().Add(-time.Minute)
	queue.ObservedAt, queue.ConsumerLiveness = &old, "stale"
	f.hook = func(f *bindingPolicyCLIFixture, r *http.Request) {
		if len(f.requests) == 4 && r.URL.Path == "/v1/apps/api/bindings" {
			at := time.Now().UTC()
			queue.ObservedAt, queue.ConsumerLiveness = &at, "healthy"
		}
	}
	code, report, stderr := f.run("--object-storage-write")
	if code != 0 || !report.Passed || len(f.requests) != 4 || len(report.Probes) != 4 || report.Preflight == nil || report.Preflight.Coverage != "partial" || !report.Preflight.ReleasePolicyConfirmed || stderr != "" {
		t.Fatalf("mixed candidate did not qualify: exit=%d report=%+v tasks=%d stderr=%s", code, report, len(f.requests), stderr)
	}
	commands := map[string]int{}
	for _, probe := range report.Probes {
		if probe.Scope != report.Scope || probe.TaskID == "" {
			t.Fatalf("canary did not report its actual task scope: %+v", probe)
		}
	}
	for _, req := range f.requests {
		commands[req.Command[0]]++
	}
	for _, command := range []string{api.AppTaskObjectStorageWriteProbeCommand, api.AppTaskPostgresBindingProbeCommand, api.AppTaskServiceBindingProbeCommand, api.AppTaskOutboundBindingProbeCommand} {
		if commands[command] != 1 {
			t.Fatalf("missing or repeated canary: %v", commands)
		}
	}
	for _, step := range report.Plan {
		if step.Type == api.BindingTypeQueue && step.Binding == "orders" && (step.Action != "observe" || step.RequiredEvidence != api.QueueEvidenceConsumerReadiness) ||
			step.Binding == "staging-orders" && step.Action != "skip" || step.Binding == "MIGRATION_DATABASE_URL" && (step.Action != "skip" || step.Reason != "release_only") {
			t.Fatalf("incorrect audience or readiness action: %+v", step)
		}
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "PRIVATE_") || strings.Contains(string(encoded), "private-provider.invalid") {
		t.Fatal("policy report leaked guest details or outbound origin")
	}
	code, report, stderr = f.run() // Reused write evidence creates no new write.
	if code != 0 || !report.Passed || len(f.requests) != 4 || len(report.Probes) != 0 || report.WriteConsentRequired || stderr != "" {
		t.Fatalf("fresh evidence was not reused: exit=%d report=%+v tasks=%d stderr=%s", code, report, len(f.requests), stderr)
	}
	for _, step := range report.Plan {
		if step.Action != "observe" && step.Action != "skip" && step.Action != "reuse" {
			t.Fatalf("fresh evidence scheduled a task: %+v", step)
		}
	}
}

func TestBindingsVerifyReleasePolicyMixedPreviewAndConsent(t *testing.T) {
	for _, preview := range []bool{true, false} {
		t.Run(map[bool]string{true: "preview", false: "missing consent"}[preview], func(t *testing.T) {
			f := newMixedBindingPolicyFixture(t)
			args := []string{}
			if preview {
				args = append(args, "--dry-run")
			}
			code, report, _ := f.run(args...)
			if report.Passed || !report.PlanReady || len(report.Plan) != 7 || !report.WriteConsentRequired || len(f.requests) != 0 || report.Preflight != nil || !report.SnapshotConfirmed {
				t.Fatalf("preview or consent ran part of the batch: %+v", report)
			}
			if preview && (code != 0 || report.Status != "planned") || !preview && (code != 1 || report.ErrorCode != "write_consent_required") {
				t.Fatalf("incorrect preview/consent status: %d %+v", code, report)
			}
		})
	}
}

func TestBindingsVerifyReleasePolicyWithoutStorageRequirements(t *testing.T) {
	for _, queueOnly := range []bool{false, true} {
		t.Run(map[bool]string{true: "queue only", false: "mixed default listing"}[queueOnly], func(t *testing.T) {
			f := newMixedBindingPolicyFixture(t)
			f.policy.ObjectStorageRequirements = nil
			if queueOnly {
				f.inventory.Bindings = []api.AppBindingInventoryItem{f.inventory.Bindings[4]}
			}
			code, report, stderr := f.run()
			if code != 0 || !report.Passed || report.WriteConsentRequired || stderr != "" {
				t.Fatalf("policy without storage requirements failed: %d %+v %s", code, report, stderr)
			}
			storageReads := 0
			for _, request := range f.requests {
				if request.Command[0] == api.AppTaskObjectStorageBindingProbeCommand {
					storageReads++
				}
			}
			if queueOnly && len(f.requests) != 0 || !queueOnly && (len(f.requests) != 4 || storageReads != 1) {
				t.Fatalf("incorrect default operations: %v", f.requests)
			}
		})
	}
}

func TestBindingsVerifyReleasePolicyWaitsWithoutReplacingPendingProbe(t *testing.T) {
	f := newMixedBindingPolicyFixture(t)
	warmBindingPolicyFixture(f)
	service := &f.inventory.Bindings[1]
	service.VerificationStatus, service.Verification = "unknown", &api.BindingVerification{Result: "unknown", Reason: "probe_pending", Source: "task_guest", DeploymentID: releaseCheckDeploymentID, Scope: "production"}
	polls := 0
	f.hook = func(_ *bindingPolicyCLIFixture, r *http.Request) {
		if r.URL.Path == "/v1/apps/api/bindings" && r.URL.Query().Get("scope") != "" {
			polls++
			if polls == 4 {
				setBindingPolicyFixtureEvidence(service)
			}
		}
	}
	code, report, stderr := f.run()
	if code != 0 || !report.Passed || polls < 4 || len(f.requests) != 0 || len(report.Probes) != 0 || stderr != "" {
		t.Fatalf("pending probe was replaced or not awaited: %d %+v %s polls=%d", code, report, stderr, polls)
	}
	for _, step := range report.Plan {
		if step.Type == api.BindingTypeService && step.Action != "wait" {
			t.Fatalf("pending probe action: %+v", step)
		}
	}
}

func TestBindingsVerifyReleasePolicyQueueWaitAndDeadline(t *testing.T) {
	for _, recoverPoll := range []bool{true, false} {
		t.Run(map[bool]string{true: "next poll", false: "deadline"}[recoverPoll], func(t *testing.T) {
			f := newMixedBindingPolicyFixture(t)
			warmBindingPolicyFixture(f)
			queue := &f.inventory.Bindings[4]
			queue.ObservedAt, queue.ConsumerLiveness = nil, "not_observed"
			polls := 0
			f.hook = func(_ *bindingPolicyCLIFixture, r *http.Request) {
				if r.URL.Path == "/v1/apps/api/bindings" && r.URL.Query().Get("scope") != "" {
					polls++
					if polls == 4 && recoverPoll {
						at := time.Now().UTC()
						queue.ObservedAt, queue.ConsumerLiveness = &at, "healthy"
					}
				}
			}
			code, report, _ := f.run("--wait-timeout", "100ms")
			// A deadline can return while the final fixture request is still
			// finishing. Read its counters under the handler's mutex.
			f.mu.Lock()
			defer f.mu.Unlock()
			if (code == 0) != recoverPoll || report.Passed != recoverPoll || len(f.requests) != 0 || len(report.Probes) != 0 || report.Preflight == nil || polls < 4 {
				t.Fatalf("queue wait failed: %d %+v polls=%d", code, report, polls)
			}
			if !recoverPoll && (report.ErrorCode != "verification_timeout" || report.SnapshotConfirmed) {
				t.Fatalf("deadline status or confirmation: %+v", report)
			}
		})
	}
}

func TestBindingsVerifyReleasePolicyPendingProbeMustCoverTargetAndOperations(t *testing.T) {
	for _, scenario := range []string{"wrong candidate", "wrong generation", "pending listing", "pending write"} {
		t.Run(scenario, func(t *testing.T) {
			f := newMixedBindingPolicyFixture(t)
			warmBindingPolicyFixture(f)
			index := 0
			if scenario == "wrong candidate" {
				index = 1
			} else if scenario == "wrong generation" {
				index = 2
			}
			item := &f.inventory.Bindings[index]
			item.VerificationStatus, item.Verification.Result, item.Verification.Reason = "unknown", "unknown", "probe_pending"
			switch scenario {
			case "wrong candidate":
				item.Verification.DeploymentID = uuid.NewString()
			case "wrong generation":
				generation := int64(2)
				item.Verification.CredentialGeneration = &generation
			case "pending listing":
				item.Verification.ObjectStorageMode = ""
			}
			polls := 0
			f.hook = func(_ *bindingPolicyCLIFixture, r *http.Request) {
				if scenario == "pending write" && r.URL.Path == "/v1/apps/api/bindings" && r.URL.Query().Get("scope") != "" {
					polls++
					if polls == 4 {
						setBindingPolicyFixtureEvidence(item)
					}
				}
			}
			code, report, _ := f.run()
			if scenario == "pending listing" {
				if code != 1 || report.ErrorCode != "write_consent_required" || !report.WriteConsentRequired || len(f.requests) != 0 || report.Plan[0].Action != "probe" {
					t.Fatalf("pending listing concealed required write consent: %d %+v", code, report)
				}
			} else if code != 0 || !report.Passed || report.WriteConsentRequired || scenario == "pending write" && (len(f.requests) != 0 || polls < 4) || scenario != "pending write" && len(f.requests) != 1 {
				t.Fatalf("pending evidence did not cover the original target: %d %+v tasks=%d polls=%d", code, report, len(f.requests), polls)
			}
		})
	}
}

func TestBindingsVerifyReleasePolicyMixedConfigurationChangesStopExecution(t *testing.T) {
	for _, scenario := range []string{"service", "postgres generation", "queue environment", "queue requirements", "outbound identity", "outbound origin"} {
		t.Run(scenario, func(t *testing.T) {
			f := newMixedBindingPolicyFixture(t)
			changed := false
			f.hook = func(f *bindingPolicyCLIFixture, _ *http.Request) {
				if changed || f.reads != 2 {
					return
				}
				changed = true
				switch scenario {
				case "service":
					f.inventory.Bindings[1].Name = "other-service"
				case "postgres generation":
					generation := int64(4)
					f.inventory.Bindings[2].CredentialGeneration = &generation
				case "queue environment":
					staging := "staging"
					f.inventory.Bindings[4].QueueEnvironment = &staging
				case "queue requirements":
					f.policy.QueueRequirements = map[string]string{}
				case "outbound identity":
					f.outboundBindings[0].Integration.ID = uuid.NewString()
				case "outbound origin":
					f.outboundBindings[0].Integration.Origin = "https://another-private-provider.invalid"
				}
			}
			code, report, _ := f.run("--object-storage-write")
			if code != 1 || report.Passed || report.SnapshotConfirmed || len(f.requests) != 0 || !changed || report.ErrorCode != "binding_configuration_changed" && report.ErrorCode != "release_policy_changed" {
				t.Fatalf("changed mixed snapshot ran a task: %d %+v changed=%t tasks=%d", code, report, changed, len(f.requests))
			}
		})
	}
}

func TestBindingsVerifyReleasePolicyMixedStructuralBlockersPreventTasks(t *testing.T) {
	for _, scenario := range []string{"unlisted queue", "missing queue", "pull queue", "paused queue", "unsupported outbound", "missing outbound ID", "ambiguous outbound ID", "wrong outbound app", "duplicate service"} {
		t.Run(scenario, func(t *testing.T) {
			f := newMixedBindingPolicyFixture(t)
			switch scenario {
			case "unlisted queue":
				f.policy.QueueRequirements = nil
			case "missing queue":
				f.policy.QueueRequirements["missing"] = api.QueueEvidenceConsumerReadiness
			case "pull queue":
				f.inventory.Bindings[4].Access = "pull"
			case "paused queue":
				f.inventory.Bindings[4].ConsumerState = "paused"
			case "unsupported outbound":
				f.inventory.Bindings[3].OutboundProbe = nil
			case "missing outbound ID":
				f.outboundBindings = nil
			case "ambiguous outbound ID":
				f.outboundBindings = append(f.outboundBindings, f.outboundBindings[0])
			case "wrong outbound app":
				f.outboundBindings[0].AppID = uuid.NewString()
			case "duplicate service":
				f.inventory.Bindings = append(f.inventory.Bindings, f.inventory.Bindings[1])
			}
			code, report, _ := f.run("--object-storage-write")
			if code != 1 || report.Passed || report.PlanReady || len(f.requests) != 0 || report.ErrorCode == "" {
				t.Fatalf("structural blocker ran a partial plan: %d %+v", code, report)
			}
		})
	}
}

func TestBindingsVerifyReleasePolicyReuseStillRequiresFreshFinalEvidence(t *testing.T) {
	f := newMixedBindingPolicyFixture(t)
	warmBindingPolicyFixture(f)
	f.hook = func(f *bindingPolicyCLIFixture, r *http.Request) {
		if f.reads == 2 && r.URL.Path == "/v1/apps/api/bindings" {
			at := time.Now().Add(-time.Hour)
			f.inventory.Bindings[2].Verification.CheckedAt = &at
		}
	}
	code, report, _ := f.run()
	if code != 1 || report.Passed || len(f.requests) != 0 || report.ErrorCode != "preflight_blocked" || report.Preflight == nil {
		t.Fatalf("reused expired evidence qualified: %d %+v", code, report)
	}
}

func TestBindingsVerifyReleasePolicyNonStorageTaskFailuresStopBatch(t *testing.T) {
	for _, kind := range []string{api.BindingTypeService, api.BindingTypePostgres, api.BindingTypeOutbound} {
		for _, failure := range []string{"non-storage failure", "missing exit", "lost status", "changed polled command"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				f := newMixedBindingPolicyFixture(t)
				warmBindingPolicyFixture(f)
				for index := range f.inventory.Bindings {
					item := &f.inventory.Bindings[index]
					if item.Type == kind && item.Access != "migration" {
						item.VerificationStatus, item.Verification = "unknown", nil
					}
				}
				f.badTask = failure
				if failure == "lost status" {
					f.queued, f.failRead = true, true
				}
				if failure == "changed polled command" {
					f.queued = true
					f.hook = func(f *bindingPolicyCLIFixture, r *http.Request) {
						if r.Method == "GET" && strings.Contains(r.URL.Path, "/tasks/") {
							f.task.Command = []string{"changed"}
						}
					}
				}
				code, report, _ := f.run()
				if code != 1 || report.Passed || len(f.requests) != 1 || len(report.Probes) != 1 || report.Probes[0].Status != "failed" || report.Probes[0].TaskID == "" {
					t.Fatalf("failed %s task qualified or lost its ID: %d %+v", kind, code, report)
				}
				if !slices.Equal(f.requests[0].Command, f.task.Command) && failure != "changed polled command" {
					t.Fatal("task command changed unexpectedly")
				}
				encoded, _ := json.Marshal(report)
				if strings.Contains(string(encoded), "PRIVATE_") {
					t.Fatal("failed task leaked raw provider diagnostics")
				}
			})
		}
	}
}
