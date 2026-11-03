package mcpmanagement

import (
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// consentCapabilities are the scopes a connection may add before sign-in,
// beyond inspection and writes, with what they let the agent do
// (ADR-20261026195952). Domains and crons need only write access.
var consentCapabilities = []struct{ scope, description string }{
	{api.ScopeSecretsRequest, "ask you to enter secrets on a Gregale page; it never sees their values"},
	{api.ScopeStorageManage, "create object storage buckets and bind them to apps"},
	{api.ScopeManagedPostgresManage, "create managed PostgreSQL databases and bind them to apps (billed)"},
}

// chosenCapabilities validates the capability checkboxes: known scopes,
// once each, and only with write access.
func chosenCapabilities(values []string, writes bool) ([]string, error) {
	var chosen []string
	for _, value := range values {
		known := slices.ContainsFunc(consentCapabilities, func(c struct{ scope, description string }) bool { return c.scope == value })
		if !known || slices.Contains(chosen, value) {
			return nil, errors.New("unknown or repeated capability")
		}
		chosen = append(chosen, value)
	}
	if len(chosen) > 0 && !writes {
		return nil, errors.New("these capabilities need write access")
	}
	return chosen, nil
}

func describeCapabilities(scopes []string) []string {
	var out []string
	for _, c := range consentCapabilities {
		if slices.Contains(scopes, c.scope) {
			out = append(out, c.description)
		}
	}
	return out
}

// grantCLI is the hosted bridge's CLI runner for one grant's credential.
func (r *remoteServer) grantCLI(token string) LocalCLI {
	if r.opts.CLIRunner == nil {
		return nil
	}
	return r.opts.CLIRunner(token)
}
