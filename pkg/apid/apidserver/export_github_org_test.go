package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exposed to apidserver_test for organization-owned GitHub installations
// (ADR-20261110200000).

var SealOAuthCodeStateForOrg = sealOAuthCodeStateForOrg

func GitHubConnectAccountAtCallback(s *Server, ctx context.Context, acct state.Account, orgID string) (string, *api.Problem) {
	return s.githubConnectAccountAtCallback(ctx, acct, orgID)
}

// OpenOAuthCodeState opens state with mgr the way the callback does.
func OpenOAuthCodeState(mgr *session.Manager, state, accountID string) (string, bool) {
	return (&server{sessions: mgr}).openOAuthCodeState(state, accountID)
}
