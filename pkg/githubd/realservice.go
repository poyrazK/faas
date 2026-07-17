// RealService — slice 8. Implements the full pkg/githubdgrpc.Service
// contract (8 methods) using the OAuth + token cache + Checks writer
// from this slice. Slice 7's Service skeleton is the inbound-webhook
// side; RealService is the dashboard/OAuth side. Both share the
// githubdgrpc.Service interface via embedding UnimplementedService.
//
// Bindings are kept in-memory (a sync.Map keyed by accountID). The
// schema work to move this to Postgres is a follow-up slice — the
// v1.0 launch uses the in-memory store and persists nothing about
// the GitHub link (re-connect on restart is the contract).
package githubd

import (
	"context"
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

// defaultProductionBranch is the branch fallback used when the
// dashboard's bind form omits one. GitHub's "main" is the post-2020
// default; older installs default to "master" via the install
// payload (slice 9's dashboard form captures that).
const defaultProductionBranch = "main"

// RealService is the slice-8 production implementation of
// githubdgrpc.Service. It composes:
//   - AppAuth (RS256 JWT minting + installation-token exchange)
//   - TokenCache (singleflight, proactive refresh)
//   - ChecksAPI (POST /repos/{o}/{r}/check-runs)
//   - in-memory bindings + install-state stores
type RealService struct {
	githubdgrpc.UnimplementedService

	Auth   *AppAuth
	Tokens *TokenCache
	Checks *ChecksAPI

	// bindings is keyed by accountID → appID → binding. Direct
	// appID lookup avoids the suffix-scan UnbindAppRepo used to
	// do and is the canonical store.
	bindingsMu sync.RWMutex
	bindings   map[string]map[string]githubdgrpc.AppBinding

	// installs is keyed by accountID → install state.
	installsMu sync.RWMutex
	installs   map[string]installState
}

// installState mirrors the githubdgrpc.InstallState enum plus the
// installation_id (string for cross-language stability; GitHub's
// integer IDs fit comfortably).
type installState struct {
	State     githubdgrpc.InstallState
	InstID    string
	DefBranch string
}

// NewRealService builds a RealService. auth, tokens, and checks
// may all be nil — the service refuses calls that need them.
func NewRealService(auth *AppAuth, tokens *TokenCache, checks *ChecksAPI) *RealService {
	return &RealService{
		Auth:     auth,
		Tokens:   tokens,
		Checks:   checks,
		bindings: map[string]map[string]githubdgrpc.AppBinding{},
		installs: map[string]installState{},
	}
}

// GetInstallState returns the install state for the given account.
// Returns UNSPECIFIED for accounts that haven't connected.
func (s *RealService) GetInstallState(accountID string) (githubdgrpc.InstallState, string, string, error) {
	s.installsMu.RLock()
	defer s.installsMu.RUnlock()
	st, ok := s.installs[accountID]
	if !ok {
		return githubdgrpc.InstallStateUnspecified, "", "", nil
	}
	return st.State, st.InstID, st.DefBranch, nil
}

// ExchangeOAuthCode persists the install state for an account.
// The "code → installation" exchange happens via the dashboard's
// own redirect (slice 9 wires the CLI command). This stub returns
// the new installation_id once the caller hands it to us; the
// real exchange happens in the dashboard handler.
func (s *RealService) ExchangeOAuthCode(accountID, installationID, defaultBranch string) (string, error) {
	if accountID == "" {
		return "", fmt.Errorf("githubd: accountID required")
	}
	if installationID == "" {
		return "", fmt.Errorf("githubd: installationID required")
	}
	s.installsMu.Lock()
	s.installs[accountID] = installState{
		State:     githubdgrpc.InstallStateInstalled,
		InstID:    installationID,
		DefBranch: defaultBranch,
	}
	s.installsMu.Unlock()
	return installationID, nil
}

// ListInstallableRepos returns the repos the installation can see.
// Requires a non-nil Auth + Tokens.
func (s *RealService) ListInstallableRepos(accountID string) ([]githubdgrpc.Repo, error) {
	if s.Auth == nil || s.Tokens == nil {
		return nil, fmt.Errorf("githubd: OAuth not configured")
	}
	s.installsMu.RLock()
	st, ok := s.installs[accountID]
	s.installsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("githubd: account %s has no installation", accountID)
	}
	var instID int64
	if _, err := fmt.Sscanf(st.InstID, "%d", &instID); err != nil {
		return nil, fmt.Errorf("githubd: invalid installation id %q", st.InstID)
	}
	tok, err := s.Tokens.Token(context.Background(), instID)
	if err != nil {
		return nil, fmt.Errorf("githubd: install token: %w", err)
	}
	repos, err := s.Auth.ListInstallableRepos(context.Background(), tok, 0)
	if err != nil {
		return nil, err
	}
	out := make([]githubdgrpc.Repo, 0, len(repos))
	for _, r := range repos {
		out = append(out, githubdgrpc.Repo{
			FullName:      r.FullName,
			DefaultBranch: r.DefaultBranch,
			Private:       r.Private,
		})
	}
	return out, nil
}

// BindAppRepo associates an app with (repo, branch) for the given
// account. Returns the new binding_id.
func (s *RealService) BindAppRepo(appID, accountID, repoFullName, productionBranch string) (string, error) {
	if appID == "" || accountID == "" || repoFullName == "" {
		return "", fmt.Errorf("githubd: appID, accountID, repoFullName required")
	}
	if productionBranch == "" {
		productionBranch = defaultProductionBranch
	}
	bindingID := fmt.Sprintf("bind-%s-%s", appID, repoFullName)
	s.bindingsMu.Lock()
	if _, ok := s.bindings[accountID]; !ok {
		s.bindings[accountID] = map[string]githubdgrpc.AppBinding{}
	}
	s.bindings[accountID][appID] = githubdgrpc.AppBinding{
		RepoFullName:     repoFullName,
		ProductionBranch: productionBranch,
		BindingID:        bindingID,
	}
	s.bindingsMu.Unlock()
	return bindingID, nil
}

// UnbindAppRepo removes the binding for an app. Returns nil even if
// no binding existed (idempotent).
func (s *RealService) UnbindAppRepo(appID, accountID string) error {
	s.bindingsMu.Lock()
	defer s.bindingsMu.Unlock()
	if byApp, ok := s.bindings[accountID]; ok {
		delete(byApp, appID)
	}
	return nil
}

// GetAppBinding looks up the binding for an app. Returns empty
// AppBinding if not found (caller checks BindingID == "").
func (s *RealService) GetAppBinding(appID, accountID string) (githubdgrpc.AppBinding, error) {
	s.bindingsMu.RLock()
	defer s.bindingsMu.RUnlock()
	byApp, ok := s.bindings[accountID]
	if !ok {
		return githubdgrpc.AppBinding{}, nil
	}
	b, ok := byApp[appID]
	if !ok {
		return githubdgrpc.AppBinding{}, nil
	}
	return b, nil
}

// CreateDeploymentFromPush is the inbound gRPC entry from apid.
// Today it returns Unimplemented-equivalent errors — the inbound
// webhook path uses HTTP, not gRPC. Kept for the gRPC contract
// round-trip test (slice 7 bufconn_test).
func (s *RealService) CreateDeploymentFromPush(_, _, _, _ string) (string, string, error) {
	return "", "", fmt.Errorf("githubd: CreateDeploymentFromPush is HTTP-driven (slice 7 webhook path)")
}

// WriteCheck pushes a check-run for (repo, sha, phase). Requires
// non-nil Checks.
func (s *RealService) WriteCheck(repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, logsURL, summary string) error {
	if s.Checks == nil {
		return fmt.Errorf("githubd: Checks writer not configured")
	}
	return s.Checks.WriteCheck(context.Background(), repoFullName, commitSHA, phase, logsURL, summary)
}
