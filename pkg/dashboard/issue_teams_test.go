package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestIssueTeamInboxControlsEscapeNamesAndRetainRoster(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	data := struct {
		AppSlug, CSRF string
		Teams         []api.IssueTeam
		Members       []struct{ ID, Name string }
	}{AppSlug: "payments", CSRF: "csrf-team", Teams: []api.IssueTeam{{ID: "team-id", Key: "payments", Name: "<script>attack</script>", MemberAccountIDs: []string{"member-one", "member-two"}}}}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "issue-teams", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "?team=team-id&amp;state=open") || !strings.Contains(html, `name="members" value="member-one,member-two"`) || strings.Count(html, "csrf-team") != 3 {
		t.Fatalf("missing inbox/roster/CSRF or unsafe names: %s", html)
	}
}
