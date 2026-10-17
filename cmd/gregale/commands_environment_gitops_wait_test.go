package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollout"
)

func gitOpsWaitStatus() api.EnvironmentGitOpsStatusResponse {
	now := time.Now().UTC()
	return api.EnvironmentGitOpsStatusResponse{Source: api.EnvironmentGitSource{ID: "source", AccountID: "account", ProjectID: "project", EnvironmentID: "env", EnvironmentSlug: "production",
		Generation: 2, IntentVersion: 3, ApprovedRevisionID: "revision", AppliedRevisionID: "old", SourceCheckedAt: &now, SourceVerifiedAt: &now,
		SourceCommitSHA: strings.Repeat("b", 40), Spec: api.EnvironmentGitSourceSpec{Provider: "github", RepositoryID: 42, InstallationID: 1, Repository: "shop/platform", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"}},
		WorkloadEvidence: &api.EnvironmentWorkloadActivationEvidence{CommitSHA: strings.Repeat("a", 40), GraphID: "graph", SourceID: "source", EnvironmentID: "env", RevisionID: "revision",
			Generation: 2, IntentVersion: 3, DefinitionDigest: strings.Repeat("c", 64), PlanHash: strings.Repeat("d", 64), GraphPhase: "prepared", ArtifactsPrepared: true, Qualified: true, Activated: true, Serving: true, Candidates: 1,
			Workloads: []api.EnvironmentWorkloadProgress{{Resource: "workload/report", AppID: "report", ExecutionMode: "job", Preparation: "complete", Qualification: "complete", Activation: "complete", Serving: "complete"}}}}
}

func TestProjectsGitOpsWaitDispatcherTextExplicitAndGlobalJSON(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, mode := range []string{"text", "json-flag", "global-json", "blocked", "failed"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				resetJSONOut(t)
				jsonOutput = mode == "global-json"
				status := gitOpsWaitStatus()
				status.Source.Spec.Provider = provider
				if provider == "gitlab" {
					status.Source.Spec.InstallationID = 0
				}
				want := 0
				if mode == "blocked" {
					status.Source.Spec.Mode = "report"
					want = 2
				}
				if mode == "failed" {
					status.WorkloadEvidence.Workloads[0].Preparation = "failed"
					want = 2
				}
				body, _ := json.Marshal(status)
				fake := authedFakeAPI(t, string(body), http.StatusOK)
				args := []string{"environments", "gitops", "wait", "shop", "production", "--commit", strings.Repeat("a", 40)}
				if mode == "json-flag" {
					args = append(args, "--json")
				}
				var stdout bytes.Buffer
				code := captureStdoutSwap(t, &stdout, func() int { return cmdProjects(args) })
				if code != want || fake.sawMethod != http.MethodGet || fake.sawPath != "/v1/projects/shop/environments/production/gitops" {
					t.Fatal(code, fake.sawMethod, fake.sawPath, stdout.String())
				}
				if mode == "json-flag" || mode == "global-json" {
					var result rollout.Result
					if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Status != "serving" || result.CommitSHA != strings.Repeat("a", 40) || !result.ReadOnly {
						t.Fatal(stdout.String())
					}
				} else if !strings.Contains(stdout.String(), "GitOps ") || !strings.Contains(stdout.String(), "shop / production") {
					t.Fatal(stdout.String())
				}
			})
		}
	}
}

func TestProjectsGitOpsWaitPollsReadOnlyAndReturnsOneFinalReceipt(t *testing.T) {
	for _, mode := range []string{"serving", "superseded", "transient"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/projects/shop/environments/production/gitops" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("unexpected request", r.Method, r.URL.Path)
				}
				count := calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if mode == "transient" && count == 1 {
					w.WriteHeader(503)
					_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Detail: "PRIVATE_UPSTREAM_ERROR"})
					return
				}
				status := gitOpsWaitStatus()
				if count == 1 {
					status.WorkloadEvidence.CommitSHA = strings.Repeat("b", 40)
					status.Source.SourceCommitSHA = strings.Repeat("a", 40)
				}
				if count == 2 {
					status.WorkloadEvidence.Serving = false
					status.WorkloadEvidence.Workloads[0].Serving = "unknown"
				}
				if mode == "superseded" && count == 3 {
					status.Source.ApprovedRevisionID = "new-revision"
					status.Source.Generation++
				}
				_ = json.NewEncoder(w).Encode(status)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			client, err := authedClient()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			var stdout bytes.Buffer
			code := captureStdoutSwap(t, &stdout, func() int {
				return runEnvironmentGitOpsWait(ctx, client, "shop", "production", strings.Repeat("a", 40), time.Millisecond, true)
			})
			var result rollout.Result
			decoder := json.NewDecoder(&stdout)
			if decoder.Decode(&result) != nil {
				t.Fatal(stdout.String())
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatal("progress contaminated JSON receipt", err)
			}
			want := 0
			if mode == "superseded" {
				want = 4
			}
			if code != want || result.ExitCode() != want || calls.Load() != 3 {
				t.Fatal(code, result, calls.Load())
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "PRIVATE") {
				t.Fatal("transport detail leaked")
			}
		})
	}
}

func TestProjectsGitOpsWaitTimeoutAndCancelReceipts(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancel"}[canceled], func(t *testing.T) {
			resetJSONOut(t)
			status := gitOpsWaitStatus()
			status.WorkloadEvidence.CommitSHA = ""
			body, _ := json.Marshal(status)
			authedFakeAPI(t, string(body), http.StatusOK)
			client, err := authedClient()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			want := 3
			if canceled {
				cancel()
				want = 130
			}
			var stdout bytes.Buffer
			code := captureStdoutSwap(t, &stdout, func() int {
				return runEnvironmentGitOpsWait(ctx, client, "shop", "production", strings.Repeat("a", 40), time.Millisecond, true)
			})
			var result rollout.Result
			if code != want || json.Unmarshal(stdout.Bytes(), &result) != nil || result.ExitCode() != want {
				t.Fatal(code, stdout.String())
			}
		})
	}
}

func TestProjectsGitOpsWaitRejectsInvalidFlagsBeforeRequests(t *testing.T) {
	for _, tail := range [][]string{{}, {"--commit", "main"}, {"--commit", strings.Repeat("A", 40)}, {"--commit", strings.Repeat("a", 40), "--timeout", "0s"}, {"--commit", strings.Repeat("a", 40), "--timeout", "25h"}, {"--commit", strings.Repeat("a", 40), "--poll-interval", "1ms"}, {"--commit", strings.Repeat("a", 40), "--poll-interval", "2m"}, {"--commit", strings.Repeat("a", 40), "--yes"}} {
		resetJSONOut(t)
		fake := authedFakeAPI(t, "{}", http.StatusOK)
		args := append([]string{"environments", "gitops", "wait", "shop", "production"}, tail...)
		if code := cmdProjects(args); code == 0 || fake.sawMethod != "" {
			t.Fatal("invalid wait reached API", tail, code)
		}
	}
}
