package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBindingCreationReconcilesAndContinuesCLI(t *testing.T) {
	raw, err := os.ReadFile("../../sdk/binding-runner-tests/creation.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, graph := range []bool{false, true} {
		for _, mode := range []string{"committed", "absent", "drift", "forbidden"} {
			t.Run(mode+map[bool]string{true: "/graph", false: "/app"}[graph], func(t *testing.T) {
				var f struct {
					AppReview   api.BindingVerificationReview        `json:"app_review"`
					AppRun      api.BindingVerificationRun           `json:"app_run"`
					GraphReview api.ProjectBindingVerificationReview `json:"graph_review"`
					GraphRun    api.ProjectBindingVerificationRun    `json:"graph_run"`
				}
				if err := json.Unmarshal(raw, &f); err != nil {
					t.Fatal(err)
				}
				created := time.Now().UTC().Add(-time.Second)
				expiry := created.Add(time.Minute)
				stamp := created.Add(-time.Second)
				update := func(r api.BindingVerificationRun, v api.BindingVerificationReview) (api.BindingVerificationRun, api.BindingVerificationReview) {
					v.Policy.UpdatedAt = &stamp
					r.Policy = v.Policy
					r.CreatedAt = created
					r.ExpiresAt = expiry
					v.ReviewedAt = created
					v.Inventory.Scope = r.Scope
					v.Inventory.VerificationScope = r.Scope
					v.Inventory.RequestedDeploymentID = r.DeploymentID
					v.Inventory.RuntimeFreshness = &api.BindingRuntimeFreshness{Source: "instance_started_at", ObservedAt: created, ConfigChangedAt: &stamp}
					r.Inventory = v.Inventory
					for i := range r.Steps {
						r.Steps[i].Plan.Action = "observe"
						v.Plan[i] = r.Steps[i].Plan
					}
					return r, v
				}
				f.AppRun, f.AppReview = update(f.AppRun, f.AppReview)
				f.GraphRun.CreatedAt = created
				f.GraphRun.ExpiresAt = expiry
				f.GraphReview.ReviewedAt = created
				for n, m := range f.GraphRun.Members {
					v := f.GraphReview.Members[n]
					m, v = update(m, v)
					f.GraphRun.Members[n] = m
					f.GraphReview.Members[n] = v
				}
				id := f.AppRun.ID
				if graph {
					id = f.GraphRun.ID
				}
				creates, reads, resumes, completes, other := 0, 0, 0, 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.HasSuffix(r.URL.Path, "/review"):
						if graph {
							_ = json.NewEncoder(w).Encode(f.GraphReview)
						} else {
							_ = json.NewEncoder(w).Encode(f.AppReview)
						}
						return
					case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/verification-runs"):
						creates++
						var request struct {
							ID string `json:"id"`
						}
						_ = json.NewDecoder(r.Body).Decode(&request)
						if request.ID != id {
							t.Error("creation changed UUID")
						}
						status := 503
						if mode == "forbidden" {
							status = 403
						}
						w.WriteHeader(status)
						_ = json.NewEncoder(w).Encode(api.Problem{Status: status})
						return
					case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+id):
						reads++
						if mode == "absent" {
							w.WriteHeader(404)
							_ = json.NewEncoder(w).Encode(api.Problem{Status: 404})
							return
						}
						if mode == "drift" {
							f.AppRun.ReviewDigest = "changed"
							f.GraphRun.ReviewDigest = "changed"
						}
					case strings.HasSuffix(r.URL.Path, "/resume"):
						resumes++
					case strings.HasSuffix(r.URL.Path, "/complete"):
						completes++
						f.AppRun.Status = "passed"
						f.GraphRun.Status = "passed"
						for n, m := range f.GraphRun.Members {
							m.Status = "passed"
							f.GraphRun.Members[n] = m
						}
					default:
						other++
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
						w.WriteHeader(500)
						return
					}
					if graph {
						_ = json.NewEncoder(w).Encode(f.GraphRun)
					} else {
						_ = json.NewEncoder(w).Encode(f.AppRun)
					}
				}))
				defer server.Close()
				args := []string{"bindings", "verify", "api", "--release-policy", "--durable", "--run-id", id}
				if graph {
					manifest := filepath.Join(t.TempDir(), "manifest.json")
					data, _ := json.Marshal(f.GraphReview.Manifest)
					if err := os.WriteFile(manifest, data, 0600); err != nil {
						t.Fatal(err)
					}
					args = []string{"projects", "environments", "release-sets", "verify", "project", "production", "--durable", "--run-id", id, "--file", manifest}
				}
				args = append(args, "--queue-publish", "--wait-timeout", "60s", "--json", "--progress", "ndjson", "--status-read-retries", "0", "--poll-interval", "1ms")
				code, out, stderr := main.CaptureBindingCLI(t, server.URL, args...)
				if creates != 1 || other != 0 {
					t.Fatalf("POST count=%d unexpected=%d %s %s", creates, other, out, stderr)
				}
				if mode == "committed" {
					if code != 0 || reads != 1 || resumes != 1 || completes != 1 {
						t.Fatalf("did not continue same receipt: code=%d reads=%d resumes=%d completes=%d %s %s", code, reads, resumes, completes, out, stderr)
					}
				} else {
					if code == 0 || resumes != 0 || completes != 0 {
						t.Fatalf("unproved receipt continued: %d %s", code, out)
					}
				}
				creationEvents := 0
				for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
					var p struct {
						Status, Operation string
						RunID             string `json:"run_id"`
					}
					if err := json.Unmarshal([]byte(line), &p); err != nil {
						t.Fatalf("non-JSON progress: %s", line)
					}
					if p.RunID != id {
						t.Fatal("progress changed UUID")
					}
					if p.Status == "reconciling" && p.Operation == "creation" {
						creationEvents++
					}
				}
				want := 1
				if mode == "forbidden" {
					want = 0
					if reads != 0 {
						t.Fatal("permanent failure reconciled")
					}
				}
				if creationEvents != want {
					t.Fatalf("creation progress=%d: %s", creationEvents, stderr)
				}
				if !strings.Contains(out, id) {
					t.Fatalf("final result lost UUID: %s", out)
				}
			})
		}
	}
}
