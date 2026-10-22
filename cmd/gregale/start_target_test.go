package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestStartNameConflictOffersRenameUpdateAndFinish(t *testing.T) {
	for _, tc := range []struct {
		name, input, slug string
		deploy            bool
	}{
		{name: "rename", input: "1\nBad name\nnew-app\ny\n", slug: "new-app", deploy: true},
		{name: "update", input: "2\ny\n", slug: "first-app", deploy: true},
		{name: "finish", input: "3\n", slug: "first-app"},
		{name: "EOF after choosing rename", input: "1\n", slug: "first-app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := main.StartTestEnvironment(t)
			main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("target selection changed remote state")
				}
				if r.URL.Path == "/v1/apps/first-app" {
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
				} else {
					api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No app"))
				}
			})
			runner := main.StartTestRunner(t, main.StartTestSource(t), tc.input)
			deploys := 0
			(*runner.Deploy()) = func(ctx context.Context, args []string, _ bool, executions ...main.DeployExecution) int {
				deploys++
				if !strings.Contains(strings.Join(args, " "), "--name "+tc.slug) || (*executions[0].ResolveApp()) == nil {
					t.Error("deployment did not preserve the reviewed app selection")
				}
				executions[0].NotifyQueued(api.DeploymentResponse{ID: "d1", AppID: "app-1", Status: "pending"})
				return (*executions[0].OnTerminal())(api.DeploymentResponse{ID: "d1", AppID: "app-1", Status: "live"})
			}
			code, submitted := runner.ReviewAndDeploy()
			if code != 0 || submitted != tc.deploy || (deploys == 1) != tc.deploy || (*runner.Session()).AppSlug != tc.slug {
				t.Fatalf("code=%d submitted=%t deploys=%d slug=%s stderr=%s stdout=%s", code, submitted, deploys, (*runner.Session()).AppSlug, stderr(), stdout)
			}
			if !strings.Contains(stdout.String(), "Create an app with another name") {
				t.Fatal("app-name collision had no alternative")
			}
		})
	}
}

func TestStartReviewedTargetCannotBeReplacedBeforeDeployment(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "create conflict", true: "existing app replaced"}[existing], func(t *testing.T) {
			stdout, stderr := main.StartTestEnvironment(t)
			source := main.StartTestSource(t)
			main.WithCwd(t, source)
			reads, creates, otherWrites := 0, 0, 0
			main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/first-app":
					reads++
					if !existing && reads == 1 {
						api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No app"))
						return
					}
					id := "unrelated-app"
					if reads == 1 {
						id = "reviewed-app"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "first-app"})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
					creates++
					api.WriteProblem(w, api.NewProblem(409, "conflict", "Conflict", "Name was taken"))
				default:
					if r.Method != http.MethodGet {
						otherWrites++
					}
					http.NotFound(w, r)
				}
			})
			input := "y\n"
			if existing {
				input = "2\ny\n"
			}
			main.PipeStdin(t, input)
			if code := main.CmdStart(nil); code == 0 || otherWrites != 0 || (creates == 1) == existing {
				t.Fatalf("replaced target reached a write: creates=%d otherWrites=%d stderr=%s stdout=%s", creates, otherWrites, stderr(), stdout)
			}
		})
	}
}

func TestStartConnectionChangeAfterReviewStopsCreation(t *testing.T) {
	for _, changed := range []string{"API", "account"} {
		t.Run(changed, func(t *testing.T) {
			main.StartTestEnvironment(t)
			account := "acct-1"
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
				}
				if r.URL.Path == "/v1/account" {
					_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: account})
				} else {
					api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No app"))
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			runner := main.StartTestRunner(t, main.StartTestSource(t), "")
			if err := runner.ResolveLaunchTarget(); err != nil {
				t.Fatal(err)
			}
			if changed == "API" {
				t.Setenv("FAAS_API", "https://other.example.test")
			} else {
				account = "another-account"
			}
			if _, err := runner.ResolveDeployTarget(context.Background(), (*runner.Client()), api.CreateAppRequest{Slug: "first-app"}); err == nil || writes != 0 {
				t.Fatalf("connection change caused a mutation: writes=%d err=%v", writes, err)
			}
		})
	}
}
