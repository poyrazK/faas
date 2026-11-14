package mcpmanagement

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// consentCapabilities are the scopes a connection may add before sign-in,
// beyond inspection and writes, with what they let the agent do
// (ADR-20261026195952). Domains and crons need only write access.
var consentCapabilities = []struct{ scope, description string }{
	{api.ScopeEnvWrite, "change non-secret environment variables and restore configuration revisions"},
	{api.ScopeUsageRead, "read usage and billing cost summaries"},
	{api.ScopeSecretsRequest, "ask you to enter secrets on a Gregale page; it never sees their values"},
	{api.ScopeStorageManage, "create object storage buckets and bind them to apps"},
	{api.ScopeManagedPostgresManage, "create managed PostgreSQL databases and bind them to apps (billed)"},
}

// chosenCapabilities validates the capability checkboxes: known scopes,
// once each; usage reads may also accompany a read-only connection.
func chosenCapabilities(values []string, writes bool) ([]string, error) {
	var chosen []string
	for _, value := range values {
		known := slices.ContainsFunc(consentCapabilities, func(c struct{ scope, description string }) bool { return c.scope == value })
		if !known || slices.Contains(chosen, value) {
			return nil, errors.New("unknown or repeated capability")
		}
		chosen = append(chosen, value)
		if !writes && value != api.ScopeUsageRead {
			return nil, errors.New("this capability needs write access")
		}
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
func (r *remoteServer) grantCLI(accountID, token string) LocalCLI {
	if r.opts.CLIRunner == nil {
		return nil
	}
	run := r.opts.CLIRunner(token)
	if run == nil || !r.opts.Hosted {
		return run
	}
	// The bridge's few CLI slots are shared: one account's agents running
	// several commands made every other account's CLI tools wait (bug hunt
	// H101), so an account holds at most hostedCLIPerAccount of them.
	return func(ctx context.Context, args []string, stdin []byte) (LocalCLIResult, error) {
		if !r.cliAccounts.acquire(accountID) {
			return LocalCLIResult{}, invalid("cli_busy",
				"another Gregale CLI command of this account is still running on the hosted bridge; wait for it to finish, then retry")
		}
		defer r.cliAccounts.release(accountID)
		return run(ctx, args, stdin)
	}
}

// accountSlots counts CLI commands running per account.
type accountSlots struct {
	mu      sync.Mutex
	running map[string]int
}

func (s *accountSlots) acquire(accountID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[accountID] >= hostedCLIPerAccount {
		return false
	}
	if s.running == nil {
		s.running = make(map[string]int)
	}
	s.running[accountID]++
	return true
}

func (s *accountSlots) release(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[accountID]--; s.running[accountID] <= 0 {
		delete(s.running, accountID)
	}
}
