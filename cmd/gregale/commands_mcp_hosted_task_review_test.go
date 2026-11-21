package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
)

func hostedCLIReviewRecord(t *testing.T) api.HostedTaskSourceIntent {
	t.Helper()
	r := tasktoolsource.Review{Kind: "hosted_task_source_review", AccountID: uuid.NewString(), Sources: map[string]string{"web": strings.Repeat("a", 64), "worker": strings.Repeat("b", 64)}, TaskReleaseReview: api.TaskReleaseReview{Version: 1, Status: "review_only", Protocol: api.MCPProtocolVersion, WorkerRuntime: "node", WebApp: "web", WorkerApp: "worker", ObserverApp: "observer", ObserverMetricApp: "observer", Endpoint: "/mcp", AccessMode: "open", ToolScopes: map[string][]string{"report": {}}, RequiredSetup: tasktoolrelease.RequiredSetup(), PreviousWorkerApps: []string{}, ReadinessRoles: []string{}, TimeoutSeconds: 60, MigrationsRequired: true}}
	for _, role := range []string{"web", "worker", "observer"} {
		r.Apps = append(r.Apps, api.TaskToolReleaseApp{Role: role, App: role, Review: api.AppReleaseReview{AppID: uuid.NewString(), Scope: api.DefaultEnvScope, ConfigurationHash: strings.Repeat("c", 64), ServingDeploymentIDs: []string{}}})
	}
	r.Fingerprint = tasktoolsource.Fingerprint(r)
	out, err := tasktoolsource.NewRecord(r.AccountID, uuid.NewString(), strings.Repeat("d", 64), r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMCPHostedTaskSourceReviewCLI(t *testing.T) {
	record := hostedCLIReviewRecord(t)
	originalExpiry := record.ExpiresAt
	id, account, intentDigest, reviewDigest := record.ID, record.AccountID, record.IntentSHA256, record.ReviewSHA256
	var mu sync.Mutex
	nativeFresh := true
	decisions := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("owner review used generic HTTP response replay")
		}
		if r.URL.Path == "/v1/account" {
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: account})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/task-tool-source-intents/"+id {
			_ = json.NewEncoder(w).Encode(record)
			return
		}
		var in api.HostedTaskSourceDecision
		action := "decide"
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/check") {
			var check api.HostedTaskSourceCheck
			if err := json.NewDecoder(r.Body).Decode(&check); err != nil {
				t.Error(err)
			}
			in.ReviewSHA256, action = check.ReviewSHA256, "check"
		} else if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/decision") {
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			decisions++
		} else {
			t.Error("unexpected hosted source request", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		out, err := tasktoolsource.Change(record, action, in, nativeFresh, true, time.Now())
		if err != nil {
			api.WriteProblem(w, api.NewProblem(409, "conflict", "Conflict", "review changed"))
			return
		}
		record = out
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer backend.Close()
	t.Setenv("FAAS_API", backend.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_hosted_owner")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Setenv("PATH", t.TempDir())
	t.Cleanup(main.ResetCapabilityView)
	run := func(action, want string, wantCode int, flags ...string) {
		t.Helper()
		args := append([]string{"mcp", "server", "tasks", "source-review", action, "--id", id, "--json"}, flags...)
		code, output, stderr := runCLI(t, args...)
		var got api.HostedTaskSourceIntent
		if code != wantCode || json.Unmarshal([]byte(output), &got) != nil || got.ReviewStatus != want || got.ReadyToApply || got.BindingsVerified || !got.OwnerAuthorizationRequired || !got.ExpiresAt.Equal(originalExpiry) {
			t.Fatalf("%s: %d %s %s", action, code, output, stderr)
		}
	}
	run("get", "pending", 0)
	code, _, _ := runCLI(t, "mcp", "server", "tasks", "source-review", "approve", "--id", id, "--intent-sha256", strings.Repeat("f", 64), "--review-sha256", reviewDigest)
	mu.Lock()
	decisionCount := decisions
	mu.Unlock()
	if code == 0 || decisionCount != 0 {
		t.Fatal("CLI submitted an owner decision for a different retained digest")
	}
	flags := []string{"--intent-sha256", intentDigest, "--review-sha256", reviewDigest}
	run("approve", "approved", 0, flags...)
	mu.Lock()
	firstDecision := *record.DecidedAt
	mu.Unlock()
	run("approve", "approved", 0, flags...)
	mu.Lock()
	replayed := record.DecidedAt.Equal(firstDecision)
	mu.Unlock()
	if !replayed {
		t.Fatal("CLI retry renewed owner approval")
	}
	run("check", "approved", 0, "--review-sha256", reviewDigest)
	mu.Lock()
	nativeFresh = false
	mu.Unlock()
	run("check", "stale", 0, "--review-sha256", reviewDigest)
	mu.Lock()
	nativeFresh = true
	mu.Unlock()
	run("approve", "stale", 1, flags...)
}
