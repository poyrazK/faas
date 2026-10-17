package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestIssueFrequencyControlsCarryRevisionAndEscapeValues(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]any{
		"FrequencyAvailable": true, "FrequencySaved": true, "AppSlug": "exports", "CSRF": "csrf-marker",
		"FrequencyPolicy":   api.IssueFrequencyAlertPolicy{Revision: 7, Rules: []api.IssueFrequencyAlertRule{{Key: "<script>bad</script>", Enabled: true, Environment: "application", MinimumEvents: 50, WindowSeconds: 300, CooldownSeconds: 1800}}},
		"FrequencyMaxRules": api.IssueFrequencyAlertMaxRules, "FrequencyMaxEvents": api.IssueFrequencyAlertMaxEvents,
		"FrequencyMinWindow": api.IssueFrequencyAlertMinWindowSeconds, "FrequencyMaxWindow": api.IssueFrequencyAlertMaxWindowSeconds,
		"FrequencyMinCooldown": api.IssueFrequencyAlertMinCooldownSeconds, "FrequencyMaxCooldown": api.IssueFrequencyAlertMaxCooldownSeconds,
	}
	var body bytes.Buffer
	if err := tmpl.ExecuteTemplate(&body, "issue-frequency-alerts", map[string]any{"Data": data}); err != nil {
		t.Fatal(err)
	}
	text := body.String()
	for _, want := range []string{`name="expected_revision" value="7"`, `name="csrf_token" value="csrf-marker"`, "&lt;script&gt;bad&lt;/script&gt;", `name="minimum_events"`, `name="window_seconds"`, `name="cooldown_seconds"`, `name="environment"`, `value="remove"`, `role="status"`, "Add an alert rule"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(text, "<script>bad") {
		t.Fatal("rule value was not escaped")
	}
	data["FrequencyAvailable"] = false
	body.Reset()
	if err := tmpl.ExecuteTemplate(&body, "issue-frequency-alerts", map[string]any{"Data": data}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body.String(), "<form") {
		t.Fatal("unavailable store exposed controls")
	}
}
