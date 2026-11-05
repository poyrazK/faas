//go:build !no_pg

package apidserver_test

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestIssueSDKEndToEndPostgres(t *testing.T) {
	if os.Getenv("GREGALE_ISSUES_SDK_ACCEPTANCE") == "" {
		t.Skip("run make test-issues for SDK process acceptance")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("SDK acceptance requires enabled PostgreSQL tests")
	}
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-sdk")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	server := httptest.NewServer((*e.H()))
	defer server.Close()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node SDK acceptance requires Node.js")
	}
	// Dynamic import accepts an absolute path without shell interpolation.
	script := `const {createIssueReporter}=await import(process.env.GREGALE_ISSUE_NODE_MODULE);const r=createIssueReporter({baseURL:process.env.GREGALE_ISSUE_API,app:'issue-sdk',token:process.env.GREGALE_ISSUE_TOKEN,groupingStrategy:'failure-site-v1'});r.captureException(new TypeError('Node SDK failure'),{error_code:'NODE_FAILURE'});if(!await r.close())process.exit(2);`
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", script)
	cmd.Env = append(os.Environ(), "GREGALE_ISSUE_API="+server.URL, "GREGALE_ISSUE_TOKEN="+token.Token, "GREGALE_ISSUE_NODE_MODULE="+filepath.Join(root, "sdk/node/dist/issues.js"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Node capture: %v %s", err, out)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python SDK acceptance requires Python")
	}
	script = `import os
from faas_sdk.preview.issues import IssueReporter
r=IssueReporter(os.environ['GREGALE_ISSUE_API'],'issue-sdk',os.environ['GREGALE_ISSUE_TOKEN'],grouping_strategy='failure-site-v1')
try:
 raise ValueError('Python SDK failure')
except ValueError as e:
 r.capture_exception(e,error_code='PYTHON_FAILURE')
assert r.close()
`
	cmd = exec.CommandContext(t.Context(), python, "-c", script)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(root, "sdk/python"), "GREGALE_ISSUE_API="+server.URL, "GREGALE_ISSUE_TOKEN="+token.Token)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Python capture: %v %s", err, out)
	}
	c := api.NewClient(server.URL, (*e.Key()))
	list, err := c.ListIssues(t.Context(), app.Slug, "", "", "")
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("SDK issues = %+v %v", list, err)
	}
	for _, issue := range list.Items {
		detail, err := c.GetIssue(t.Context(), app.Slug, issue.ID, "", "")
		if err != nil || len(detail.Events) != 1 || len(detail.Events[0].Frames) == 0 || detail.Releases[0].DeploymentID != dep.ID {
			t.Fatalf("SDK evidence incomplete: %+v %v", detail, err)
		}
		if issue.GroupingVersion != 2 || detail.Events[0].GroupingStrategy != "failure-site-v1" || detail.Events[0].ErrorCode == "" {
			t.Fatalf("SDK grouping controls not applied: %+v", detail)
		}
		wantOrder := "newest-first"
		if detail.Events[0].ErrorCode == "PYTHON_FAILURE" {
			wantOrder = "oldest-first"
		}
		if detail.Events[0].FrameOrder != wantOrder {
			t.Fatalf("SDK frame order = %q, want %q", detail.Events[0].FrameOrder, wantOrder)
		}
	}
}
