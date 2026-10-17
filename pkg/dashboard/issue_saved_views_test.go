package dashboard

import (
	"bytes"
	"github.com/onebox-faas/faas/pkg/api"
	"html/template"
	"strings"
	"testing"
)

func TestIssueSavedViewLinksStayLiveAndEscapeNames(t *testing.T) {
	links := IssueSavedViewLinks("payments", []api.IssueSavedView{{Key: "mine", Name: "<script>attack</script>", Filters: api.IssueSavedViewFilters{Assignee: "me"}}})
	if links[0].URL != "/dashboard/apps/payments/issues?view=mine" {
		t.Fatal("saved link froze its filter definition")
	}
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "issue-saved-views", IssueSavedViewsView{AppSlug: "payments", CSRF: "token", Items: links, CurrentFilters: api.IssueSavedViewFilters{Assignee: "me", Tag: "component=payments"}, MaxViews: 20}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "<script>") || !strings.Contains(out.String(), "&lt;script&gt;") || !strings.Contains(out.String(), `name="csrf_token" value="token"`) || !strings.Contains(out.String(), `name="assignee" value="me"`) {
		t.Fatal("unsafe name, missing CSRF, or me resolved when saving")
	}
}
