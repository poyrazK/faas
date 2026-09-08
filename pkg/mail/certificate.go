package mail

import (
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/redact"
)

// CertificateIssuanceFailure contains the customer-safe details for F2.
type CertificateIssuanceFailure struct {
	Domain       string
	AppSlug      string
	LastError    string
	DashboardURL string
	FailedAt     time.Time
}

// CertificateIssuanceFailedBody renders the F2 notification. The ACME error
// is provider-controlled but still passes through the standard redactor and
// control-character sanitizer before it reaches an outbound message.
func CertificateIssuanceFailedBody(f CertificateIssuanceFailure) (subject, body string) {
	redactor := redact.New(4096)
	safe := func(v string) string {
		v, _ = redactor.Apply(v)
		return logsanitize.Field(v)
	}
	domain := safe(f.Domain)
	if domain == "" {
		domain = "your custom domain"
	}
	app := safe(f.AppSlug)
	if app == "" {
		app = "your app"
	}
	if f.FailedAt.IsZero() {
		f.FailedAt = time.Now().UTC()
	}
	subject = fmt.Sprintf("Certificate issuance failed for %s", domain)
	var b strings.Builder
	fmt.Fprintf(&b, "Hi,\n\nWe couldn't issue a TLS certificate for %s (app %q). It has been failing since %s.\n\n",
		domain, app, f.FailedAt.UTC().Format("2006-01-02 15:04 UTC"))
	if errText := safe(f.LastError); errText != "" {
		fmt.Fprintf(&b, "Provider error: %s\n\n", errText)
	}
	if dashboard := safe(f.DashboardURL); dashboard != "" {
		fmt.Fprintf(&b, "Review the domain in the dashboard:\n%s\n\n", dashboard)
	}
	b.WriteString("Verify the domain's DNS-01 records and try again.\n\n— onebox faas\n")
	return subject, b.String()
}
