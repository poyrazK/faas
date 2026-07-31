// Regression guard for the unsubstituted docs-domain placeholder
// (issue #257 acceptance 6, issue #420).
//
// `Docs: https://docs.DOMAIN` shipped in the CLI's root help text —
// the single place a customer looks when a command fails. Everywhere
// else in the tree had already been substituted (pkg/api/errors.go
// WithDocs targets, api/openapi.yaml's contact url, docs/DPA.md), so
// this was the last leak and the most visible one.
//
// The test asserts the rendered help, not the source line, so a future
// refactor that builds the footer from a variable is still covered.

package main

import (
	"strings"
	"testing"
)

// docsBaseURL is the canonical customer docs origin. Every other
// customer-facing docs link in the repo already points here
// (pkg/api/errors.go:560+, api/openapi.yaml contact, docs/DPA.md), so
// the CLI footer agreeing with them is the whole point of this test.
const docsBaseURL = "https://docs.gregale.dev"

// TestUsage_DocsLinkIsSubstituted pins that the root help footer
// carries a resolvable docs URL and never a template placeholder.
//
// The placeholder check is deliberately broader than the exact string
// `docs.DOMAIN`: any of the conventional unsubstituted spellings
// showing up in customer-visible help is the same defect.
func TestUsage_DocsLinkIsSubstituted(t *testing.T) {
	if !strings.Contains(usage, docsBaseURL) {
		t.Errorf("root usage is missing the docs URL %q", docsBaseURL)
	}

	placeholders := []string{
		"docs.DOMAIN",
		"DOMAIN",
		"example.com",
		"<domain>",
	}
	for _, p := range placeholders {
		if strings.Contains(usage, p) {
			t.Errorf("root usage leaks the unsubstituted placeholder %q", p)
		}
	}
}
