package main

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBindingStatusReadRetryCLI(t *testing.T) {
	raw, err := os.ReadFile("../../sdk/binding-runner-tests/creation.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, graph := range []bool{false, true} {
		for _, retry := range []string{"0", "1"} {
			t.Run(map[bool]string{false: "app", true: "graph"}[graph]+retry, func(t *testing.T) {
				var fixture struct {
					App   api.BindingVerificationRun        `json:"app_run"`
					Graph api.ProjectBindingVerificationRun `json:"graph_run"`
				}
				if e := json.Unmarshal(raw, &fixture); e != nil {
					t.Fatal(e)
				}
				created := time.Now().UTC().Add(-time.Second)
				expiry := created.Add(time.Minute)
				update := func(r api.BindingVerificationRun) api.BindingVerificationRun {
					r.CreatedAt = created
					r.ExpiresAt = expiry
					for i := range r.Steps {
						r.Steps[i].ResourceID = "44444444-4444-4444-8444-000000000001"
						r.Steps[i].ResourceKind = "task"
						r.Steps[i].Status = "queued"
						r.Steps[i].Owned = true
						r.Steps[i].Plan.Action = "probe"
						if r.Steps[i].Plan.Type == "queue" {
							r.Steps[i].Plan.Action = "queue-canary"
							r.Steps[i].ResourceID = "44444444-4444-4444-8444-000000000002"
							r.Steps[i].ResourceKind = "queue"
							r.Steps[i].Status = "published"
						}
					}
					return r
				}
				fixture.App = update(fixture.App)
				fixture.Graph.CreatedAt = created
				fixture.Graph.ExpiresAt = expiry
				for n, m := range fixture.Graph.Members {
					fixture.Graph.Members[n] = update(m)
				}
				id := fixture.App.ID
				if graph {
					id = fixture.Graph.ID
				}
				reads, resumes, completes := 0, 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.HasSuffix(r.URL.Path, "/diagnostics"):
						w.WriteHeader(404)
						return
					case strings.HasSuffix(r.URL.Path, "/resume"):
						resumes++
					case strings.HasSuffix(r.URL.Path, "/complete"):
						completes++
						fixture.App.Status = "passed"
						fixture.Graph.Status = "passed"
						for n, m := range fixture.Graph.Members {
							m.Status = "passed"
							fixture.Graph.Members[n] = m
						}
					case r.Method == http.MethodGet:
						reads++
						if reads == 1 {
							w.WriteHeader(503)
							_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "unavailable"})
							return
						}
						for i := range fixture.App.Steps {
							fixture.App.Steps[i].Status = "succeeded"
						}
						for n, m := range fixture.Graph.Members {
							for i := range m.Steps {
								m.Steps[i].Status = "succeeded"
							}
							fixture.Graph.Members[n] = m
						}
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(500)
						return
					}
					if graph {
						_ = json.NewEncoder(w).Encode(fixture.Graph)
					} else {
						_ = json.NewEncoder(w).Encode(fixture.App)
					}
				}))
				defer server.Close()
				args := []string{"bindings", "verification-runs", "resume", "api", id}
				if graph {
					args = []string{"projects", "environments", "release-sets", "verification-runs", "resume", "project", "production", id}
				}
				args = append(args, "--status-read-retries", retry, "--poll-interval", "1ms", "--json")
				code, out, stderr := captureBindingCLI(t, server.URL, args...)
				expectedReads, expectedCode, expectedCompletes := 1, 1, 0
				if retry == "1" {
					expectedReads, expectedCode, expectedCompletes = 2, 0, 1
				}
				if code != expectedCode || reads != expectedReads || resumes != 1 || completes != expectedCompletes {
					t.Fatalf("retry routing code=%d reads=%d resumes=%d completes=%d %s %s", code, reads, resumes, completes, out, stderr)
				}
			})
		}
	}
}
