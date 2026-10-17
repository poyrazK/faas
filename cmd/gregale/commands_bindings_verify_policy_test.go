package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/bindingprobe"
)

// The actual CLI submits existing reserved commands. Execute their real S3
// probe code against an owned loopback fixture, then project recorded stages.
// This does not simulate a VM, real provider, scheduler or state-store fence.
type bindingPolicyCLIFixture struct {
	mu               sync.Mutex
	t                *testing.T
	server           *httptest.Server
	storage          *httptest.Server
	inventory        api.AppBindingInventory
	policy           api.BindingReleasePolicy
	requests         []api.CreateAppTaskRequest
	task             api.AppTaskResponse
	reads            int
	policies         int
	cancels          int
	hook             func(*bindingPolicyCLIFixture, *http.Request)
	queued           bool
	badTask          string
	failRead         bool
	ignoreDeployment bool
	outboundBindings []api.OutboundAppBinding
}

func newBindingPolicyCLIFixture(t *testing.T) *bindingPolicyCLIFixture {
	t.Helper()
	f := &bindingPolicyCLIFixture{t: t, inventory: storageReleaseCheckInventory("", "", ""), policy: storageReleaseCheckPolicy()}
	f.inventory.Bindings[0].Verification, f.inventory.Bindings[0].VerificationStatus = nil, "unknown"
	var storageMu sync.Mutex
	objects := map[string][]byte{}
	f.storage = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storageMu.Lock()
		defer storageMu.Unlock()
		if r.Header.Get("Authorization") == "" {
			t.Error("canary did not sign its S3 request")
		}
		switch r.Method {
		case http.MethodPut:
			objects[r.URL.Path], _ = io.ReadAll(r.Body)
			w.Header().Set("ETag", `"probe"`)
		case http.MethodDelete:
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
		case http.MethodGet:
			if r.URL.Query().Get("list-type") == "2" {
				_, _ = io.WriteString(w, "<ListBucketResult><Name>assets</Name><KeyCount>0</KeyCount></ListBucketResult>")
			} else if body, ok := objects[r.URL.Path]; ok {
				_, _ = w.Write(body)
			} else {
				w.WriteHeader(404)
				_, _ = io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
			}
		default:
			t.Errorf("unexpected storage request: %s", r.Method)
			w.WriteHeader(500)
		}
	}))
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() {
		f.server.Close()
		f.storage.Close()
		storageMu.Lock()
		defer storageMu.Unlock()
		if len(objects) != 0 {
			t.Errorf("CLI canary left %d disposable objects", len(objects))
		}
	})
	return f
}

func (f *bindingPolicyCLIFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.hook != nil {
		f.hook(f, r)
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/apps/api/bindings/verification-runs/review":
		var in api.ReviewBindingVerificationRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			f.t.Error(err)
			w.WriteHeader(400)
			return
		}
		i := f.inventory
		i.Bindings = append([]api.AppBindingInventoryItem(nil), i.Bindings...)
		i.Scope = f.policy.Scope
		i.RequestedDeploymentID = i.VerificationDeploymentID
		i.ServiceTargets = in.ServiceTargets
		for n := range i.Bindings {
			if i.Bindings[n].Type == api.BindingTypeService {
				i.Bindings[n].ServiceTargetDeploymentID = in.ServiceTargets[i.Bindings[n].Name]
			}
		}
		if f.ignoreDeployment {
			i.RequestedDeploymentID = ""
		}
		_ = json.NewEncoder(w).Encode(bindings.BuildVerificationReview(i, f.policy, time.Now()))
	case r.Method == "GET" && r.URL.Path == "/v1/apps/api":
		serveStorageReleaseCheckApp(w)
	case r.Method == "GET" && r.URL.Path == "/v1/apps/api/bindings":
		f.reads++
		i := f.inventory
		i.Scope = r.URL.Query().Get("scope")
		i.RequestedDeploymentID = r.URL.Query().Get("deployment_id")
		if f.ignoreDeployment {
			i.RequestedDeploymentID = ""
		}
		_ = json.NewEncoder(w).Encode(i)
	case r.Method == "GET" && r.URL.Path == "/v1/apps/api/bindings/release-policy":
		f.policies++
		if r.URL.Query().Get("scope") != "production" {
			f.t.Error("policy read did not use the selected deployment scope")
		}
		_ = json.NewEncoder(w).Encode(f.policy)
	case r.Method == "GET" && r.URL.Path == "/v1/apps/api/outbound-bindings":
		_ = json.NewEncoder(w).Encode(api.OutboundAppBindingList{Items: f.outboundBindings})
	case r.Method == "POST" && r.URL.Path == "/v1/apps/api/tasks":
		f.createTask(w, r)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/apps/api/tasks/"):
		if f.failRead {
			http.Error(w, "status unavailable", 503)
			return
		}
		if !f.queued {
			f.task.Status = api.AppTaskStatusSucceeded
		}
		_ = json.NewEncoder(w).Encode(f.task)
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/apps/api/tasks/"):
		f.cancels++
		f.task.Status = api.AppTaskStatusCancelled
		_ = json.NewEncoder(w).Encode(f.task)
	default:
		f.t.Errorf("unexpected CLI request: %s %s", r.Method, r.URL)
		w.WriteHeader(500)
	}
}

func (f *bindingPolicyCLIFixture) createTask(w http.ResponseWriter, r *http.Request) {
	var req api.CreateAppTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Command) < 2 || req.VerificationDeploymentID != releaseCheckDeploymentID || req.CommandShell {
		f.t.Error("unpinned or invalid policy verification admission")
		w.WriteHeader(400)
		return
	}
	f.requests = append(f.requests, req)
	if req.Command[0] != api.AppTaskObjectStorageBindingProbeCommand && req.Command[0] != api.AppTaskObjectStorageWriteProbeCommand {
		f.createNonStoragePolicyTask(w, req)
		return
	}
	prefix, mode := req.Command[1], ""
	if len(req.Command) == 3 {
		mode = req.Command[2]
	}
	env := map[string]string{prefix + "_ENDPOINT": f.storage.URL, prefix + "_REGION": "auto", prefix + "_BUCKET": "assets", prefix + "_ACCESS_KEY_ID": "private-access", prefix + "_SECRET_ACCESS_KEY": "private-secret", prefix + "_ADDRESSING_STYLE": "path"}
	getenv := func(k string) string { return env[k] }
	var probe api.ObjectStorageBindingProbeReport
	if mode == "" {
		probe = bindingprobe.ObjectStorage(r.Context(), prefix, getenv)
	} else {
		probe = bindingprobe.ObjectStorageWrite(r.Context(), prefix, mode, getenv)
	}
	if f.badTask == "cleanup" {
		probe.Cleanup.Status = "failed" // Fixture has cleaned up; receipt did not prove it.
	}
	if f.badTask == "legacy" {
		probe = passedObjectStorageCLIReport(prefix)
	}
	body, _ := json.Marshal(probe)
	f.task = api.AppTaskResponse{ID: uuid.NewString(), AppID: releaseCheckAppID, DeploymentID: releaseCheckDeploymentID, DeploymentScope: "production",
		Command: req.Command, Status: api.AppTaskStatusSucceeded, StdoutTail: string(body), ExitCode: new(int)}
	if f.queued {
		f.task.Status = api.AppTaskStatusQueued
	}
	if f.badTask == "deployment" {
		f.task.DeploymentID = uuid.NewString()
	}
	if f.badTask == "app" {
		f.task.AppID = uuid.NewString()
	}
	if f.badTask == "command" {
		f.task.Command = []string{api.AppTaskObjectStorageBindingProbeCommand, "OTHER"}
	}
	if f.badTask == "truncated" {
		f.task.OutputTruncated = true
	}
	checked := time.Now().UTC()
	for n := range f.inventory.Bindings {
		item := &f.inventory.Bindings[n]
		if item.Binding == prefix {
			item.VerificationStatus = "passed"
			item.Verification = &api.BindingVerification{Result: "passed", Source: "task_guest", DeploymentID: releaseCheckDeploymentID, Scope: "production", ObjectStorageMode: mode, CheckedAt: &checked,
				Checks: []api.BindingVerificationCheck{{Name: "environment", Status: "passed"}, {Name: "configuration", Status: "passed"}, {Name: "connection", Status: "passed"}, {Name: "authorization", Status: "passed"}, {Name: "bucket_access", Status: "passed"}}}
			if mode != "" {
				item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: "upload", Status: "passed"}, api.BindingVerificationCheck{Name: "cleanup", Status: "passed"})
			}
			if mode == "read_write" {
				item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: "download", Status: "passed"})
			}
		}
	}
	_ = json.NewEncoder(w).Encode(f.task)
}

func (f *bindingPolicyCLIFixture) run(args ...string) (int, bindingPolicyVerificationReport, string) {
	f.t.Helper()
	command := append([]string{"bindings", "verify", "api", "--release-policy", "--json", "--poll-interval", "1ms", "--wait-timeout", "3s"}, args...)
	code, output, stderr := captureBindingCLI(f.t, f.server.URL, command...)
	var report bindingPolicyVerificationReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		f.t.Fatalf("expected one JSON report: exit=%d error=%v out=%s stderr=%s", code, err, output, stderr)
	}
	if strings.Contains(output, "private-secret") || strings.Contains(output, f.storage.URL) {
		f.t.Fatal("report leaked provider details or credential material")
	}
	return code, report, stderr
}

func TestBindingsVerifyReleasePolicyCLIJourney(t *testing.T) {
	for _, permission := range []string{"read", "write", "read_write"} {
		t.Run(permission, func(t *testing.T) {
			f := newBindingPolicyCLIFixture(t)
			f.inventory.Bindings[0].Access = permission
			f.policy.ObjectStorageRequirements["ASSETS"] = permission
			args := []string{}
			if permission != "read" {
				args = append(args, "--object-storage-write")
			}
			// Also exercise implicit deployment selection: every admitted task
			// must still supply an explicitly reconfirmed deployment UUID.
			code, report, stderr := f.run(args...)
			if code != 0 || !report.Passed || report.Status != "passed" || !report.SnapshotConfirmed || report.Preflight == nil || !report.Preflight.ReleasePolicyConfirmed || stderr != "" || len(f.requests) != 1 {
				t.Fatalf("exit=%d report=%+v stderr=%s tasks=%d", code, report, stderr, len(f.requests))
			}
		})
	}
}

func TestBindingsVerifyReleasePolicyWholePlanConsentAndPreview(t *testing.T) {
	for _, mode := range []string{"preview", "missing consent", "execute"} {
		t.Run(mode, func(t *testing.T) {
			f := newBindingPolicyCLIFixture(t)
			f.policy.ObjectStorageRequirements = map[string]string{"A_READ": "read", "B_WRITE": "write", "C_COMBINED": "write"}
			base := f.inventory.Bindings[0]
			f.inventory.Bindings = nil
			for _, prefix := range []string{"C_COMBINED", "B_WRITE", "A_READ"} {
				item := base
				item.Binding, item.Access = prefix, map[string]string{"A_READ": "read", "B_WRITE": "write", "C_COMBINED": "read_write"}[prefix]
				f.inventory.Bindings = append(f.inventory.Bindings, item)
			}
			args := []string{"--deployment", releaseCheckDeploymentID}
			if mode == "preview" {
				args = append(args, "--dry-run")
			}
			if mode == "execute" {
				args = append(args, "--object-storage-write")
			}
			code, report, stderr := f.run(args...)
			if len(report.Plan) != 3 || report.Plan[0].Prefix != "A_READ" || report.Plan[1].ProbeMode != "write" || report.Plan[2].ProbeMode != "read_write" || !slices.Equal(report.Plan[2].Operations, []string{"upload", "download", "cleanup"}) || !report.WriteConsentRequired || !report.PlanReady || stderr != "" {
				t.Fatalf("incorrect plan: %+v stderr=%s", report, stderr)
			}
			switch mode {
			case "preview":
				if code != 0 || report.Status != "planned" || report.Passed || len(f.requests) != 0 || len(report.Probes) != 0 || report.Preflight != nil {
					t.Fatal("preview submitted tasks or claimed verification")
				}
			case "missing consent":
				if code != 1 || report.ErrorCode != "write_consent_required" || report.Passed || len(f.requests) != 0 {
					t.Fatal("a partial plan ran without consent to every required write")
				}
			case "execute":
				if code != 0 || !report.Passed || len(f.requests) != 3 || len(report.Probes) != 3 {
					t.Fatalf("batch did not qualify: %+v tasks=%d", report, len(f.requests))
				}
			}
		})
	}
}

func TestBindingsVerifyReleasePolicyRejectsPlanBeforeAnyTask(t *testing.T) {
	for _, scenario := range []string{"off", "unknown family", "missing", "ambiguous", "inactive", "rotation", "unknown rotation", "permission", "incomplete", "stamp", "future stamp", "policy app", "policy scope", "invalid policy", "old server"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBindingPolicyCLIFixture(t)
			switch scenario {
			case "off":
				f.policy.Mode = "off"
			case "unknown family":
				f.inventory.Bindings = append(f.inventory.Bindings, api.AppBindingInventoryItem{Type: "future", Name: "unknown"})
			case "missing":
				f.inventory.Bindings = nil
			case "ambiguous":
				f.inventory.Bindings = append(f.inventory.Bindings, f.inventory.Bindings[0])
			case "inactive":
				f.inventory.Bindings[0].State = "revoked"
			case "rotation":
				pending := true
				f.inventory.Bindings[0].RotationPending = &pending
			case "unknown rotation":
				f.inventory.Bindings[0].RotationPending = nil
			case "permission":
				f.inventory.Bindings[0].Access = "read"
			case "incomplete":
				f.inventory.Complete = false
			case "stamp":
				f.inventory.RuntimeFreshness.ConfigChangedAt = nil
			case "future stamp":
				at := time.Now().Add(time.Hour)
				f.inventory.RuntimeFreshness.ConfigChangedAt = &at
			case "policy app":
				f.policy.AppID = uuid.NewString()
			case "policy scope":
				f.policy.Scope = "default"
			case "invalid policy":
				f.policy.MaxVerificationAge = "invalid"
			case "old server":
				f.ignoreDeployment = true
			}
			code, report, _ := f.run("--object-storage-write")
			if code != 1 || report.Passed || report.ErrorCode == "" || len(f.requests) != 0 {
				t.Fatalf("unsafe plan admitted: exit=%d report=%+v tasks=%d", code, report, len(f.requests))
			}
		})
	}
}

func TestBindingsVerifyReleasePolicyInterruptDuringTaskRead(t *testing.T) {
	f := newBindingPolicyCLIFixture(t)
	f.queued = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.hook = func(_ *bindingPolicyCLIFixture, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/apps/api/tasks/") {
			cancel()
			<-r.Context().Done()
		}
	}
	report, code := executeBindingPolicyVerification(ctx, api.NewClient(f.server.URL, "fp_live_test"), "api", "", false, true, time.Millisecond, time.Second)
	if code != 130 || report.Passed || report.Status != "cancelled" || len(f.requests) != 1 || f.cancels != 1 || len(report.Probes) != 1 || report.Probes[0].Report.TaskID == "" {
		t.Fatalf("interrupted read lost the admitted task: exit=%d report=%+v cancellations=%d", code, report, f.cancels)
	}
}

func TestBindingsVerifyReleasePolicyStopsOnChangedSnapshot(t *testing.T) {
	for _, phase := range []string{"before first admission", "after first canary", "final preflight"} {
		for _, change := range []string{"revision", "same revision requirements", "mode", "binding", "config stamp", "rotation", "missing metadata", "scope", "app", "policy unavailable"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				f := newBindingPolicyCLIFixture(t)
				triggered := false
				f.hook = func(f *bindingPolicyCLIFixture, r *http.Request) {
					trigger := phase == "before first admission" && f.reads == 2 || phase == "after first canary" && len(f.requests) == 1 || phase == "final preflight" && len(f.requests) == 1 && r.Method == "GET" && r.URL.Path == "/v1/apps/api/bindings"
					if !trigger || triggered {
						return
					}
					triggered = true
					switch change {
					case "revision":
						f.policy.Revision++
					case "same revision requirements":
						f.policy.ObjectStorageRequirements["ASSETS"] = "read"
					case "mode":
						f.policy.Mode = "off"
					case "binding":
						f.inventory.Bindings[0].Name = "rebound-bucket"
					case "config stamp":
						at := time.Now().Add(-time.Second)
						f.inventory.RuntimeFreshness.ConfigChangedAt = &at
					case "rotation":
						pending := true
						f.inventory.Bindings[0].RotationPending = &pending
					case "missing metadata":
						f.inventory.Complete = false
					case "scope":
						f.inventory.VerificationScope = "default"
					case "app":
						f.inventory.App = "other"
					case "policy unavailable":
						f.policy.MaxVerificationAge = "invalid"
					}
				}
				code, report, _ := f.run("--object-storage-write")
				wantTasks := 1
				if phase == "before first admission" {
					wantTasks = 0
				}
				if code != 1 || report.Passed || report.SnapshotConfirmed || len(f.requests) != wantTasks || !triggered {
					t.Fatalf("changed snapshot accepted: exit=%d report=%+v tasks=%d triggered=%t", code, report, len(f.requests), triggered)
				}
			})
		}
	}
}

func TestBindingsVerifyReleasePolicyTaskFailuresAndFinalGate(t *testing.T) {
	for _, failure := range []string{"cleanup", "legacy", "deployment", "app", "command", "truncated", "ACK", "lost status", "timeout"} {
		t.Run(failure, func(t *testing.T) {
			f := newBindingPolicyCLIFixture(t)
			f.badTask = failure
			args := []string{"--object-storage-write"}
			switch failure {
			case "ACK":
				f.policy.RequireApplicationAck = true
			case "lost status":
				f.queued, f.failRead = true, true
			case "timeout":
				f.queued = true
				args = append(args, "--wait-timeout", "100ms")
			}
			code, report, _ := f.run(args...)
			if code != 1 || report.Passed || len(f.requests) != 1 || len(report.Probes) != 1 {
				t.Fatalf("unqualified receipt accepted: exit=%d report=%+v tasks=%d", code, report, len(f.requests))
			}
			if failure == "ACK" {
				if report.Probes[0].Status != "passed" || report.Preflight == nil || report.Preflight.Passed || report.ErrorCode != "preflight_blocked" || len(report.Preflight.Blockers) == 0 {
					t.Fatalf("canary confused with release readiness: %+v", report)
				}
			}
			if (failure == "deployment" || failure == "app" || failure == "command") && f.cancels != 1 {
				t.Fatal("unconfirmed admission was not cancelled")
			}
			if failure == "timeout" && (report.Probes[0].Report.TaskID == "" || report.ErrorCode != "verification_timeout" || f.cancels != 1) {
				t.Fatalf("ordinary timeout lost task identity or cleanup: report=%+v cancels=%d", report, f.cancels)
			}
		})
	}
}

func TestBindingsVerifyReleasePolicyInvalidFlagCombinations(t *testing.T) {
	for _, args := range [][]string{{"api", "--dry-run"}, {"api", "--all", "--release-policy"}, {"api", "--release-policy", "--object-storage", "ASSETS"}, {"api", "billing", "--release-policy"}, {"api", "--release-policy", "--postgres", "DATABASE_URL"}, {"api", "--release-policy=false", "--dry-run"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			_, _, stderr := captureBindingCLI(t, "http://127.0.0.1:1", append([]string{"bindings", "verify"}, args...)...)
			if !strings.Contains(stderr, "usage:") {
				t.Fatalf("invalid combination reached API handling: %s", stderr)
			}
		})
	}
}
