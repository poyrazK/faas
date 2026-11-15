package apidserver

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exposed to apidserver_test for organization-owned GitHub installations
// (ADR-20261110200000).

var SealOAuthCodeStateForOrg = sealOAuthCodeStateForOrg

// StreamAcctID is the namespace the last source stream ran as.
func (r *sourceRefFake) StreamAcctID() *string { return &r.streamAcctID }

// ReposByInstall lets a test give an installation repositories.
func (r *sourceRefFake) ReposByInstall() *map[int64][]Repo { return &r.reposByInstall }

// PostWithHeaders posts with extra headers; an Authorization header replaces
// the environment's own key.
func (e sourceRefTestEnv) PostWithHeaders(t *testing.T, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	return e.postWithHeaders(t, path, body, headers)
}

func GitHubConnectAccountAtCallback(s *Server, ctx context.Context, acct state.Account, orgID string) (string, *api.Problem) {
	return s.githubConnectAccountAtCallback(ctx, acct, orgID)
}

// OpenOAuthCodeState opens state with mgr the way the callback does.
func OpenOAuthCodeState(mgr *session.Manager, state, accountID string) (string, bool) {
	return (&server{sessions: mgr}).openOAuthCodeState(state, accountID)
}
