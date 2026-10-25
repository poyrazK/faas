package api

import "testing"

// Production-us rc.294: `gregale dashboards list` and `gregale incidents`
// answered "the ... preview is not enabled" with the CLI's generic outage hint
// ("Check Gregale's service status ... before retrying a deploy") because the
// server sent none (Hunt 14 H14-37).
func TestDisabledPreviewsSayRetryingWillNotHelp(t *testing.T) {
	for name, problem := range map[string]*Problem{
		"dashboards": ErrDashboardsUnavailable(),
		"log search": ErrLogSearchUnavailable(),
		"incidents":  ErrIncidentsUnavailable(),
	} {
		if problem.Hint != PreviewNotEnabledHint {
			t.Errorf("%s hint = %q, want %q", name, problem.Hint, PreviewNotEnabledHint)
		}
	}
}
