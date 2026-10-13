package dashboard

import (
	"bytes"
	"github.com/onebox-faas/faas/pkg/api"
	"html/template"
	"strings"
	"testing"
)

func TestIssueTrackerLinksEscapeTitlesAndProtectMutations(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	view := IssueTrackerLinksView{AppSlug: "payments", IssueID: "group", CSRF: "token", NextURL: "/dashboard/apps/payments/issues?issue=group&tracker_link_cursor=next", Items: []api.IssueTrackerLink{{ID: "link-one", OriginalIssueID: "original", Provider: "github", ExternalKey: "owner/repo#42", URL: "https://github.com/owner/repo/issues/42", Title: "<script>attack</script>"}}}
	if err := tmpl.ExecuteTemplate(&out, "issue-tracker-links", view); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("tracker title was not escaped")
	}
	for _, want := range []string{`rel="noopener noreferrer"`, `action="/dashboard/apps/payments/issues/group/tracker-links"`, `name="link_id" value="link-one"`, `name="action" value="delete"`, `name="action" value="save"`, `original issue <code>original</code>`, `tracker_link_cursor=next`} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Count(html, `name="csrf_token" value="token"`) != 2 {
		t.Fatal("both mutations require CSRF")
	}
}
