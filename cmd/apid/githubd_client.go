// apid↔githubd client wrapper (ADR-012). The wire surface lives in
// pkg/githubdgrpc; this file holds the apid-side seam: a small interface
// (so handlers can be tested without a real socket) and a thin wrapper
// that dials, closes, and is nil-safe. Slice 1 ships a stub that returns
// api.Problem{Code:"githubd_not_ready"} for every method so the dashboard
// + REST surface can land before githubd is fully wired. Slices 7+8
// replace the stub with a real *liveClient dialing pkg/githubdgrpc.Client.
//
// Auth: the unix socket's 0660/group-`faas` DAC is the only auth in v1.0
// (ADR-015). Transport is insecure credentials over a trusted local path
// (see pkg/githubdgrpc.Dial).
package main

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

// GithubdClient is the apid-side view of githubd. The interface exists
// so handlers can be unit-tested with a fake without dialing a socket.
type GithubdClient interface {
	GetInstallState(ctx context.Context, accountID string) (InstallState, string, string, error)
	ExchangeOAuthCode(ctx context.Context, accountID, code, state string) (string, error)
	ListInstallableRepos(ctx context.Context, accountID string) ([]Repo, error)
	BindAppRepo(ctx context.Context, appID, accountID, repoFullName, productionBranch string) (string, error)
	UnbindAppRepo(ctx context.Context, appID, accountID string) error
	GetAppBinding(ctx context.Context, appID, accountID string) (AppBinding, error)
	CreateDeploymentFromPush(ctx context.Context, repoFullName, ref, commitSHA, pusher string) (string, string, error)
	WriteCheck(ctx context.Context, repoFullName, commitSHA string, phase CheckPhase, logsURL, summary string) error
	// VerifyInstallation is the "trust on first contact" check
	// called by /oauth/callback before persisting any binding
	// (review finding #1+#2 closure for the M7.5 OAuth path).
	VerifyInstallation(ctx context.Context, installationID int64) (verified bool, defaultBranch string, err error)
	Close() error
}

// Aliases for the platform-friendly enum + struct mirrors — same shape
// handlers see when called from the dashboard. Kept here (not in pkg/api)
// because githubd is the only consumer; the dashboard and CLI dial apid,
// never githubd directly.
type (
	InstallState = githubdgrpc.InstallState
	CheckPhase   = githubdgrpc.CheckPhase
	Repo         = githubdgrpc.Repo
	AppBinding   = githubdgrpc.AppBinding
)

const (
	InstallStateUnspecified  = githubdgrpc.InstallStateUnspecified
	InstallStateNotInstalled = githubdgrpc.InstallStateNotInstalled
	InstallStateInstalling   = githubdgrpc.InstallStateInstalling
	InstallStateInstalled    = githubdgrpc.InstallStateInstalled
	InstallStateBound        = githubdgrpc.InstallStateBound

	CheckPhaseUnspecified = githubdgrpc.CheckPhaseUnspecified
	CheckPhaseQueued      = githubdgrpc.CheckPhaseQueued
	CheckPhaseBuilding    = githubdgrpc.CheckPhaseBuilding
	CheckPhaseLive        = githubdgrpc.CheckPhaseLive
	CheckPhaseFailed      = githubdgrpc.CheckPhaseFailed
)

// stubGithubdClient is the slice-1 default. It returns a stable
// api.Problem for every RPC so handlers can render a "GitHub not yet
// connected" UX without a githubd process running. Close() is a no-op so
// cleanup paths are safe. Slices 7+8 replace with a *liveClient.
type stubGithubdClient struct{}

// errGithubdNotReady is the problem returned by every stub method. The
// dashboard renders the message verbatim; the Code is stable for tests.
var errGithubdNotReady = api.NewProblem(
	503, "githubd_not_ready",
	"GitHub integration is not wired on this host yet (M7.5 slices 7-8).",
	"",
)

// GetInstallState returns the not-ready problem. Slice 7 replaces this.
func (stubGithubdClient) GetInstallState(context.Context, string) (InstallState, string, string, error) {
	return InstallStateUnspecified, "", "", errGithubdNotReady
}

// ExchangeOAuthCode returns the not-ready problem. Slice 8 replaces this.
func (stubGithubdClient) ExchangeOAuthCode(context.Context, string, string, string) (string, error) {
	return "", errGithubdNotReady
}

// ListInstallableRepos returns the not-ready problem. Slice 8 replaces this.
func (stubGithubdClient) ListInstallableRepos(context.Context, string) ([]Repo, error) {
	return nil, errGithubdNotReady
}

// BindAppRepo returns the not-ready problem. Slice 8 replaces this.
func (stubGithubdClient) BindAppRepo(context.Context, string, string, string, string) (string, error) {
	return "", errGithubdNotReady
}

// UnbindAppRepo returns the not-ready problem. Slice 8 replaces this.
func (stubGithubdClient) UnbindAppRepo(context.Context, string, string) error {
	return errGithubdNotReady
}

// GetAppBinding returns the not-ready problem. Slice 8 replaces this.
func (stubGithubdClient) GetAppBinding(context.Context, string, string) (AppBinding, error) {
	return AppBinding{}, errGithubdNotReady
}

// CreateDeploymentFromPush returns the not-ready problem. Slice 7 replaces this.
func (stubGithubdClient) CreateDeploymentFromPush(context.Context, string, string, string, string) (string, string, error) {
	return "", "", errGithubdNotReady
}

// WriteCheck returns the not-ready problem. Slice 7 replaces this.
func (stubGithubdClient) WriteCheck(context.Context, string, string, CheckPhase, string, string) error {
	return errGithubdNotReady
}

// VerifyInstallation returns the not-ready problem. Slice 8
// replaces this; the OAuth callback handler (cmd/apid/
// handlers_oauth.go) treats the not-ready sentinel as a "GitHub
// integration not configured" page rather than a hard 500, since
// "Connect GitHub" is a slice 8 capability and the v1.0 launch can
// ship without it.
func (stubGithubdClient) VerifyInstallation(context.Context, int64) (bool, string, error) {
	return false, "", errGithubdNotReady
}

// Close is a no-op for the stub.
func (stubGithubdClient) Close() error { return nil }

// liveClient is the slice-7 wrapper around *githubdgrpc.Client. Slices
// 7+8 swap a *stubGithubdClient for a *liveClient in newGithubdClient().
// Lives in this file so the apid-side handler tests don't have to import
// pkg/githubdgrpc directly — they only see the GithubdClient interface.
type liveClient struct {
	c   *githubdgrpc.Client
	log *slog.Logger
}

// Close releases the underlying socket connection.
func (l *liveClient) Close() error {
	if l == nil || l.c == nil {
		return nil
	}
	return l.c.Close()
}

// GetInstallState passes through to githubdgrpc.Client.GetInstallState.
func (l *liveClient) GetInstallState(ctx context.Context, accountID string) (InstallState, string, string, error) {
	return l.c.GetInstallState(ctx, accountID)
}

// ExchangeOAuthCode passes through to githubdgrpc.Client.ExchangeOAuthCode.
func (l *liveClient) ExchangeOAuthCode(ctx context.Context, accountID, code, state string) (string, error) {
	return l.c.ExchangeOAuthCode(ctx, accountID, code, state)
}

// ListInstallableRepos passes through to githubdgrpc.Client.ListInstallableRepos.
func (l *liveClient) ListInstallableRepos(ctx context.Context, accountID string) ([]Repo, error) {
	return l.c.ListInstallableRepos(ctx, accountID)
}

// BindAppRepo passes through to githubdgrpc.Client.BindAppRepo.
func (l *liveClient) BindAppRepo(ctx context.Context, appID, accountID, repoFullName, productionBranch string) (string, error) {
	return l.c.BindAppRepo(ctx, appID, accountID, repoFullName, productionBranch)
}

// UnbindAppRepo passes through to githubdgrpc.Client.UnbindAppRepo.
func (l *liveClient) UnbindAppRepo(ctx context.Context, appID, accountID string) error {
	return l.c.UnbindAppRepo(ctx, appID, accountID)
}

// GetAppBinding passes through to githubdgrpc.Client.GetAppBinding.
func (l *liveClient) GetAppBinding(ctx context.Context, appID, accountID string) (AppBinding, error) {
	return l.c.GetAppBinding(ctx, appID, accountID)
}

// CreateDeploymentFromPush passes through to githubdgrpc.Client.CreateDeploymentFromPush.
func (l *liveClient) CreateDeploymentFromPush(ctx context.Context, repoFullName, ref, commitSHA, pusher string) (string, string, error) {
	return l.c.CreateDeploymentFromPush(ctx, repoFullName, ref, commitSHA, pusher)
}

// WriteCheck passes through to githubdgrpc.Client.WriteCheck.
func (l *liveClient) WriteCheck(ctx context.Context, repoFullName, commitSHA string, phase CheckPhase, logsURL, summary string) error {
	return l.c.WriteCheck(ctx, repoFullName, commitSHA, phase, logsURL, summary)
}

// VerifyInstallation passes through to githubdgrpc.Client.VerifyInstallation.
func (l *liveClient) VerifyInstallation(ctx context.Context, installationID int64) (bool, string, error) {
	return l.c.VerifyInstallation(ctx, installationID)
}

// newGithubdClient is the slice-1 constructor: returns the stub. Slice 7
// replaces with a live dial when cfg.Socket != "". Returning an
// interface (not a concrete *stubGithubdClient) means callers never have
// to type-assert, and the slice-7 swap is a one-line change here.
func newGithubdClient(socketPath string, log *slog.Logger) GithubdClient {
	if socketPath == "" {
		if log != nil {
			log.Info("githubd socket not configured; using stub client (slice 1)")
		}
		return stubGithubdClient{}
	}
	c, err := githubdgrpc.Dial(socketPath)
	if err != nil {
		if log != nil {
			log.Error("githubd dial failed; falling back to stub", "socket", socketPath, "err", err)
		}
		return stubGithubdClient{}
	}
	if log != nil {
		log.Info("githubd connected", "socket", socketPath)
	}
	return &liveClient{c: c, log: log}
}
