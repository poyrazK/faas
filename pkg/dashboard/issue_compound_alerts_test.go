// adr: 1662
package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestIssueCompoundAlertDraftPreviewControls(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_compound_alerts.html")
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]any{"AppSlug": "payments", "CSRF": "csrf-marker", "Policy": api.IssueCompoundAlertPolicy{Revision: 7, Rules: []api.IssueCompoundAlertRule{{Key: "existing"}}}, "Draft": api.IssueCompoundAlertRule{Key: "<script>bad</script>", MinimumCustomers: 3}, "Preview": api.IssueCompoundAlertPreview{Reason: "customer_threshold"}, "IssueID": "sample", "MaxEvents": api.IssueFrequencyAlertMaxEvents, "MaxCustomers": api.IssueCompoundAlertMaxCustomers, "MaxRules": api.IssueCompoundAlertMaxRules, "MinWindow": 60, "MaxWindow": 86400, "MinCooldown": 60, "MaxCooldown": 604800}
	var body bytes.Buffer
	if err = tmpl.Execute(&body, map[string]any{"Data": data, "Nonce": "nonce"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`name="expected_revision" value="7"`, `name="minimum_customers"`, "csrf-marker", "customer_threshold", "&lt;script&gt;bad&lt;/script&gt;", `value="preview"`, `value="remove"`, "same rolling window"} {
		if !strings.Contains(body.String(), want) {
			t.Fatal("missing control or evidence", want)
		}
	}
	if strings.Contains(body.String(), "<script>bad") {
		t.Fatal("unescaped draft")
	}
}
