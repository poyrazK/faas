package apidserver

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// humanApprovalKeyLabels name the keys an interactive sign-in mints for a
// person at a terminal or browser: the dashboard sign-in key and the CLI
// login key. The MCP login key is left out on purpose: an agent holds it.
var humanApprovalKeyLabels = map[string]bool{signInKeyLabel: true, cliLoginKeyLabel: true}

// humanApprovalProblem refuses a protected-environment approval from an
// automation credential (bug hunt H70). The approval route needed only MFA
// and the deploy-write scope, which a CI key, an OIDC deploy bearer or an
// agent's MCP key all carry, so the credential that deploys could approve
// its own release to a protected environment. A browser session or an
// interactive sign-in key (a person who logged in with MFA) still approves;
// anything else is told to ask a person, who can use `gregale login` or the
// browser review (ADR-1242).
func humanApprovalProblem(r *http.Request) *api.Problem {
	p, ok := principalFrom(r)
	if !ok || p.Key == nil || humanApprovalKeyLabels[p.Key.Label] {
		return nil
	}
	return api.NewProblem(http.StatusForbidden, api.CodeForbidden,
		"A person must approve this environment",
		"protected environments are approved by a person signed in with MFA (the dashboard or `gregale login`), not by an API key, deploy token or agent key; ask an approver, or request a browser review")
}

// reservedKeyLabelProblem refuses a customer-chosen key label that names an
// interactive sign-in key. Those labels mark the keys a person's login
// minted, which protected-environment approvals and the sign-in revocation
// paths trust, so a CI key must not be able to take one.
func reservedKeyLabelProblem(label string) *api.Problem {
	switch strings.TrimSpace(label) {
	case signInKeyLabel, cliLoginKeyLabel, api.MCPLoginKeyLabel:
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Reserved key label",
			"the labels "+signInKeyLabel+", "+cliLoginKeyLabel+" and "+api.MCPLoginKeyLabel+" are reserved for keys an interactive sign-in creates; choose another label")
	}
	return nil
}
