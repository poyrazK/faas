package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A failed npm install's build log, as gregale logs --deployment printed it on
// production-us (hunt #10, H10-74), trimmed to the relevant part.
var npmFailureBuildLog = []string{
	"#12 copy package.json",
	"#12 DONE 0.0s",
	"#13 npm install",
	"#13 1.007 npm warn config production Use `--omit=dev` instead.",
	"#13 1.778 npm error code E404",
	"#13 1.778 npm error 404 Not Found - GET https://registry.npmjs.org/missing-pkg - Not found",
	"#13 1.778 npm error 404",
	"#13 1.779 npm error 404  The requested resource 'missing-pkg@9.9.9' could not be found or you do not have permission to access it.",
	"#13 1.779 npm error 404",
	"#13 1.782 npm error A complete log of this run can be found in: /root/.npm/_logs/debug-0.log",
	"#13 ERROR: process \"npm install\" did not complete successfully: exit code: 1",
	"------",
	" > npm install:",
	"1.778 npm error code E404",
	"1.778 npm error 404 Not Found - GET https://registry.npmjs.org/missing-pkg - Not found",
	"github.com/moby/buildkit/solver/llbsolver/ops.(*ExecOp).Exec",
	"\tgithub.com/moby/buildkit/solver/llbsolver/ops/exec.go:555",
	`time="2026-10-14T19:13:45Z" level=debug msg="session finished: <nil>"`,
	"FAILED: build exited 1",
}

func TestBuildErrorExcerptShowsTheFailingCommand(t *testing.T) {
	got := main.BuildErrorExcerpt(npmFailureBuildLog)
	want := []string{
		"npm error code E404",
		"npm error 404 Not Found - GET https://registry.npmjs.org/missing-pkg - Not found",
		"npm error 404",
		"npm error 404  The requested resource 'missing-pkg@9.9.9' could not be found or you do not have permission to access it.",
		"ERROR: process \"npm install\" did not complete successfully: exit code: 1",
		"> npm install:",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("excerpt =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if main.BuildErrorExcerpt([]string{"#1 DONE 0.1s", "all good"}) != nil {
		t.Fatal("a log without an error line produced an excerpt")
	}
}

// A failed build refreshes the deployment for its error code (the SSE frame
// carries only the status) and shows the build output and the full-log
// command (H10-74).
func TestDeployBuildFailureShowsCauseAndBuildLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/deployments/dep-1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-1", Status: main.DeploymentStatusFailed,
				Error: "build exited 1", ErrorCode: api.CodeDepInstallFailed})
		case r.URL.Path == "/v1/deployments/dep-1/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			for _, line := range npmFailureBuildLog {
				entry, _ := json.Marshal(map[string]string{"line": line})
				_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", entry)
			}
			_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	client, err := main.AuthedClient()
	if err != nil {
		t.Fatal(err)
	}
	_, readStderr, restore := main.SwapIO(t)
	code := main.TerminalExitForDeploymentWithFailureContext(context.Background(), client,
		api.DeploymentResponse{ID: "dep-1", Status: main.DeploymentStatusFailed, Error: "build exited 1"}, "my-app", nil)
	stderr := readStderr()
	restore()
	if code == 0 {
		t.Fatal("a failed build exited 0")
	}
	for _, want := range []string{"build output:", "npm error 404 Not Found - GET https://registry.npmjs.org/missing-pkg", "full build log: gregale logs my-app --deployment dep-1"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "Deploy failed: build exited 1") {
		t.Fatalf("stderr kept the bare failure instead of the dep_install_failed copy:\n%s", stderr)
	}
}
