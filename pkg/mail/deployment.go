package mail

import (
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/redact"
)

// DeploymentFailure is the customer-facing portion of a failed deployment.
// The fields are copied from the persisted deployment row by apid; keeping
// this type here makes the mail template independent of pkg/state.
type DeploymentFailure struct {
	AppSlug      string
	DeploymentID string
	ErrorCode    string
	Error        string
	ErrorHint    string
	ErrorWhy     string
	ErrorFix     string
	RelevantLogs []api.LogExcerpt
	DashboardURL string
	FailedAt     time.Time
}

// DeploymentFailedBody renders the I1 deploy-failure notification. Every
// customer-controlled diagnostic string passes through the same ADR-096
// redactor used for persisted error excerpts, then through the mail body's
// control-character sanitizer. This keeps secrets and header injection out of
// both the email body and provider logs.
func DeploymentFailedBody(f DeploymentFailure) (subject, body string) {
	redactor := redact.New(4096)
	safe := func(v string) string {
		v, _ = redactor.Apply(v)
		return logsanitize.Field(v)
	}

	slug := safe(f.AppSlug)
	if slug == "" {
		slug = "your app"
	}
	if f.FailedAt.IsZero() {
		f.FailedAt = time.Now().UTC()
	}
	subject = fmt.Sprintf("Deploy failed for %s", slug)

	var b strings.Builder
	fmt.Fprintf(&b, "Hi,\n\nA deployment for app %q failed at %s.\n\n", slug,
		f.FailedAt.UTC().Format("2006-01-02 15:04 UTC"))
	if id := safe(f.DeploymentID); id != "" {
		fmt.Fprintf(&b, "Deployment: %s\n", id)
	}
	if code := safe(f.ErrorCode); code != "" {
		fmt.Fprintf(&b, "Error code: %s\n", code)
	}
	if errText := safe(f.Error); errText != "" {
		fmt.Fprintf(&b, "Error: %s\n", errText)
	}
	if f.ErrorCode != "" || f.Error != "" {
		b.WriteByte('\n')
	}
	if hint := safe(f.ErrorHint); hint != "" {
		fmt.Fprintf(&b, "Hint: %s\n", hint)
	}
	if why := safe(f.ErrorWhy); why != "" {
		fmt.Fprintf(&b, "Why: %s\n", why)
	}
	if fix := safe(f.ErrorFix); fix != "" {
		fmt.Fprintf(&b, "Fix: %s\n", fix)
	}
	if f.ErrorHint != "" || f.ErrorWhy != "" || f.ErrorFix != "" {
		b.WriteByte('\n')
	}
	if len(f.RelevantLogs) > 0 {
		b.WriteString("Relevant logs:\n")
		for _, entry := range f.RelevantLogs {
			fmt.Fprintf(&b, "%s [%s] %s: %s\n", safe(entry.Timestamp), safe(entry.Level), safe(entry.Source), safe(entry.Message))
		}
		b.WriteByte('\n')
	}
	if dashboard := safe(f.DashboardURL); dashboard != "" {
		fmt.Fprintf(&b, "Open the deployment in the dashboard:\n%s\n\n", dashboard)
	}
	b.WriteString("— onebox faas\n")
	return subject, b.String()
}
