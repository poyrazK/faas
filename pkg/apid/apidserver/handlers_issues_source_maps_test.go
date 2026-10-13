//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 1113
func TestIssueNodeSourceMapsGroupActualFailuresAcrossDeployments(t *testing.T) {
	if os.Getenv("GREGALE_ISSUES_SDK_ACCEPTANCE") == "" {
		t.Skip("run make test-issues for SDK process acceptance")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("source-map acceptance requires enabled PostgreSQL tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-source-maps")
	server := httptest.NewServer((*e.H()))
	defer server.Close()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for i, symbol := range []string{"a", "b"} {
		dep := apidserver.IssueSeedDeployment(t, e, app, i+1)
		token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
		bundle := filepath.Join(t.TempDir(), "chunk-"+symbol+".mjs")
		code := "function " + symbol + "(){throw new Error('payment failed')} export {" + symbol + " as fail};"
		if err := os.WriteFile(bundle, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		// Each deployment runs a distinct minified artifact and supplies its
		// matching map. Original code content must never reach issue storage.
		script := `
const {createIssueReporter}=await import(process.env.GREGALE_ISSUE_NODE_MODULE);
const {pathToFileURL}=await import('node:url');
const reporter=createIssueReporter({
  baseURL:process.env.GREGALE_ISSUE_API,app:'issue-source-maps',token:process.env.GREGALE_ISSUE_TOKEN,
  groupingStrategy:'failure-site-v1',sourceMaps:[{
    generatedFile:process.env.GREGALE_ISSUE_BUNDLE,payload:JSON.stringify({
      version:3,sources:['/app/src/checkout.ts'],names:['charge'],mappings:'AASAA',
      sourcesContent:['PRIVATE_SOURCE_CONTENT']
    })
  }]
});
const bundle=await import(pathToFileURL(process.env.GREGALE_ISSUE_BUNDLE).href);
try { bundle.fail(); } catch (error) { reporter.captureException(error,{error_code:'PAYMENT_404'}); }
if(!await reporter.close())process.exit(2);
if(reporter.stats().mappedFrames!==1)process.exit(3);
`
		cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", script)
		cmd.Env = append(os.Environ(), "GREGALE_ISSUE_API="+server.URL, "GREGALE_ISSUE_TOKEN="+token.Token,
			"GREGALE_ISSUE_NODE_MODULE="+filepath.Join(root, "sdk/node/dist/issues.js"), "GREGALE_ISSUE_BUNDLE="+bundle)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("mapped Node capture: %v %s", err, out)
		}
	}
	client := api.NewClient(server.URL, (*e.Key()))
	list, err := client.ListIssues(t.Context(), app.Slug, "", "", "")
	if err != nil || len(list.Items) != 1 || list.Items[0].EventCount != 2 {
		t.Fatalf("bundle changes split the original failure: %+v %v", list, err)
	}
	detail, err := client.GetIssue(t.Context(), app.Slug, list.Items[0].ID, "", "")
	if err != nil || len(detail.Events) != 2 || len(detail.Releases) != 2 {
		t.Fatalf("mapped release history missing: %+v %v", detail, err)
	}
	for _, event := range detail.Events {
		if len(event.Frames) == 0 || event.Frames[0].File != "/app/src/checkout.ts" ||
			event.Frames[0].Function != "charge" || event.Frames[0].Line != 10 {
			t.Fatalf("original failure location missing: %+v", event.Frames)
		}
		explanation := event.GroupingExplanation
		if explanation == nil || len(explanation.Frames) != 1 || explanation.Frames[0].File != "src/checkout.ts" ||
			explanation.Frames[0].Function != "charge" {
			t.Fatalf("original grouping evidence did not verify: %+v", explanation)
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(event.StackTrace, "chunk-") || strings.Contains(string(raw), "PRIVATE_SOURCE_CONTENT") {
			t.Fatal("raw evidence lost or source content persisted")
		}
	}
}
