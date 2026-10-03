package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/openapidiff"
)

func renderPreviewSecurityFindings(w io.Writer, report previewRouteReport, markdown bool) {
	if report.Security.Status == "" {
		return
	}
	if markdown {
		_, _ = fmt.Fprint(w, "\n### Declared authentication changes\n\n")
	} else {
		_, _ = fmt.Fprint(w, "\nDeclared authentication changes\n")
	}
	_, _ = fmt.Fprint(w, "Findings compare captured declarations. They do not verify runtime authentication, token validity, authorization, or credential strength.\n\n")
	count := 0
	for _, route := range report.Routes {
		security := route.SecurityCompatibility
		if security == nil {
			continue
		}
		if security.Changed || len(security.Findings) > 0 {
			_, _ = fmt.Fprintf(w, "- %s: %s -> %s\n", previewSourceDisplay(previewReportRouteKey(route.Method, route.Path), markdown), previewSecuritySummary(security.Baseline), previewSecuritySummary(security.Candidate))
		}
		for _, finding := range security.Findings {
			count++
			revision := ""
			if finding.Revision != "" {
				revision = " (" + finding.Revision + ")"
			}
			_, _ = fmt.Fprintf(w, "  - %s: %s%s\n", finding.Severity, securityFindingDescription(finding.Code), revision)
		}
	}
	if count == 0 && report.Security.Status == "available" {
		_, _ = fmt.Fprintln(w, "No supported authentication changes were found in compared operations.")
	}
}

func previewSecuritySummary(summary *openapidiff.SecuritySummary) string {
	if summary == nil {
		return "unknown"
	}
	text := summary.Authentication + " (" + summary.Source
	if len(summary.CredentialKinds) > 0 {
		text += "; " + strings.Join(summary.CredentialKinds, ", ")
	}
	return text + ")"
}

func securityFindingDescription(code string) string {
	switch code {
	case "anonymous_access_added":
		return "Previously required authentication now has a declared anonymous alternative or no declared requirement."
	case "security_requirements_weakened":
		return "More credential combinations satisfy the declared requirements; a credential/scope was removed or an alternative was added."
	case "authentication_required":
		return "Previously anonymous requests now require declared credentials."
	case "security_requirements_restricted":
		return "Previously supported credential combinations no longer satisfy the declared requirements."
	case "security_scheme_changed":
		return "A referenced credential definition changed, including its transport, HTTP mechanism, or identity-provider configuration; review clients and enforcement."
	case "security_requirements_incomparable":
		return "The declared requirements changed in both directions; credential replacements require review."
	case "comparison_unavailable":
		return "The security comparison is unavailable because its bounds or captured evidence could not be established."
	default:
		return "Declared authentication comparison is unresolved: " + code + "."
	}
}
