//go:build !no_pg

package apidserver_test

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// adr: 1117
func TestIssueCauseGroupingPersistsEvidenceAndRetryProvenance(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-causes")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Wrapper", Message: "wrapped", GroupingStrategy: "cause-chain-v1",
		Causes: []api.IssueCause{{ExceptionType: "DatabaseError", Message: "query failed", ErrorCode: "DB_DOWN", Frames: []api.IssueFrame{{File: "/app/src/db.ts", Function: "query", InApp: true}}}}}
	first := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
	retry := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
	if !retry.Duplicate || retry.IssueID != first.IssueID {
		t.Fatal("retry changed identity")
	}
	event.EventID = uuid.NewString()
	event.ExceptionType = "OtherWrapper"
	event.Message = "other wording"
	second := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
	if second.IssueID != first.IssueID {
		t.Fatal("wrapper split cause")
	}
	detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues/"+first.IssueID, nil, nil), 200)
	if detail.Issue.GroupingVersion != 3 || detail.Issue.EventCount != 2 || len(detail.Events) != 2 {
		t.Fatal("wrong group totals")
	}
	for _, occ := range detail.Events {
		if len(occ.Causes) != 1 || occ.Causes[0].ErrorCode != "DB_DOWN" || occ.GroupingExplanation == nil || occ.GroupingExplanation.CauseIndex == nil || *occ.GroupingExplanation.CauseIndex != 0 {
			t.Fatalf("lost cause evidence: %+v", occ)
		}
	}
	event.EventID = uuid.NewString()
	event.CausesIncomplete = true
	partial := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
	if partial.IssueID == first.IssueID {
		t.Fatal("partial root merged")
	}
}

// adr: 1117
func TestIssueCauseSDKEndToEndPostgres(t *testing.T) {
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
	script := `const {createIssueReporter}=await import(process.env.GREGALE_ISSUE_NODE_MODULE);const r=createIssueReporter({baseURL:process.env.GREGALE_ISSUE_API,app:'issue-sdk',token:process.env.GREGALE_ISSUE_TOKEN,groupingStrategy:'cause-chain-v1'});const root=new TypeError('Node SDK failure');for(const text of ['wrapper A','wrapper B'])r.captureException(new Error(text,{cause:root}));if(!await r.close())process.exit(2);`
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
from faas_sdk.issues import IssueReporter
r=IssueReporter(os.environ['GREGALE_ISSUE_API'],'issue-sdk',os.environ['GREGALE_ISSUE_TOKEN'],grouping_strategy='cause-chain-v1')
try:
 raise ValueError('Python SDK failure')
except ValueError as cause:
 for text in ['wrapper A','wrapper B']:
  try:
   raise RuntimeError(text) from cause
  except RuntimeError as wrapper:
   r.capture_exception(wrapper)
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
		if err != nil || len(detail.Events) != 2 || len(detail.Events[0].Frames) == 0 || detail.Releases[0].DeploymentID != dep.ID {
			t.Fatalf("SDK evidence incomplete: %+v %v", detail, err)
		}
		if issue.GroupingVersion != 3 || detail.Events[0].GroupingStrategy != "cause-chain-v1" || len(detail.Events[0].Causes) != 1 || detail.Events[0].GroupingExplanation == nil || detail.Events[0].GroupingExplanation.CauseIndex == nil {
			t.Fatalf("SDK grouping controls not applied: %+v", detail)
		}
		wantOrder := "newest-first"
		if detail.Events[0].Causes[0].ExceptionType == "builtins.ValueError" {
			wantOrder = "oldest-first"
		}
		if detail.Events[0].FrameOrder != wantOrder {
			t.Fatalf("SDK frame order = %q, want %q", detail.Events[0].FrameOrder, wantOrder)
		}
	}
}
