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
	"crypto/tls"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

// GithubdClient is the apid-side view of githubd. The interface exists
// so handlers can be unit-tested with a fake without dialing a socket.
type GithubdClient interface {
	GetInstallState(ctx context.Context, accountID string) (InstallState, string, string, error)
	// ExchangeOAuthCode turns the GitHub OAuth `code` into a durable
	// install token (sealed under the host age key, persisted to
	// github_installations). PR-C widens to also return the
	// default_branch so the apid handler can redirect the dashboard
	// to the right "Connect GitHub" success page without an extra
	// round-trip.
	ExchangeOAuthCode(ctx context.Context, accountID, code, state string) (string, string, error)
	ListInstallableRepos(ctx context.Context, accountID string, installationID int64) ([]Repo, error)
	BindAppRepo(ctx context.Context, appID, accountID string, installationID int64, repoFullName, productionBranch string) (string, error)
	UnbindAppRepo(ctx context.Context, appID, accountID string) error
	GetAppBinding(ctx context.Context, appID, accountID string) (AppBinding, error)
	CreateDeploymentFromPush(ctx context.Context, repoFullName, ref, commitSHA, pusher string) (string, string, error)
	WriteCheck(ctx context.Context, repoFullName, commitSHA string, phase CheckPhase, logsURL, summary string) error
	// VerifyInstallation is the "trust on first contact" check
	// called by /oauth/callback before persisting any binding
	// (review finding #1+#2 closure for the M7.5 OAuth path).
	// PR-B: expectedLogin carries the §11 ownership assertion; the
	// response includes the install's account_login so the apid
	// handler can audit-log it.
	VerifyInstallation(ctx context.Context, installationID int64, expectedLogin string) (verified bool, accountLogin string, defaultBranch string, err error)
	// MintInstallationToken (DEPLOY-PROV-4 / ADR-092, issue #739)
	// returns a fresh installation token for (accountID,
	// installationID). apid's handleSourceRefDeploy uses it to
	// authenticate a codeload archive fetch without scraping the
	// token from the durable install row. The token stays scoped
	// to one RPC response; expiresAt stamps the apid-side
	// install-token cache so the next call short-circuits.
	MintInstallationToken(ctx context.Context, accountID string, installationID int64) (token string, expiresAt time.Time, err error)
	// StreamSourceRef (DEPLOY-PROV-4 / ADR-092, issue #739) drains
	// the codeload tar.gz for (repo, ref) through githubd's
	// install-token bound fetch. The returned Result.Body is an
	// io.ReadCloser the apid handler pipes straight into
	// validateAndSpool — no in-memory buffering. Stats.Truncated +
	// Stats.BytesStreamed populate on Close.
	StreamSourceRef(ctx context.Context, accountID string, installationID int64, repoFullName, ref string, maxArchiveBytes int64) (*StreamSourceRefResult, error)
	Close() error
}

// StreamSourceRefResult mirrors pkg/githubdgrpc.StreamSourceRefResult
// so handler tests can construct it without importing the gRPC
// package. Stay field-for-field compatible with the wire
// counterpart (PR-A invariant — see cmd/apid/handlers_source_ref.go).
type StreamSourceRefResult struct {
	Body  io.ReadCloser
	Stats *StreamSourceRefStats
}

// StreamSourceRefStats mirrors pkg/githubdgrpc.StreamSourceRefStats.
type StreamSourceRefStats struct {
	Truncated         bool
	BytesStreamed     int64
	ResolvedCommitSHA string
	Err               error
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
func (stubGithubdClient) ExchangeOAuthCode(context.Context, string, string, string) (string, string, error) {
	return "", "", errGithubdNotReady
}

// ListInstallableRepos returns the not-ready problem. Slice 8 replaces this.
func (stubGithubdClient) ListInstallableRepos(context.Context, string, int64) ([]Repo, error) {
	return nil, errGithubdNotReady
}

// BindAppRepo returns the not-ready problem. Slice 8 replaces this.
func (stubGithubdClient) BindAppRepo(context.Context, string, string, int64, string, string) (string, error) {
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
func (stubGithubdClient) VerifyInstallation(context.Context, int64, string) (bool, string, string, error) {
	return false, "", "", errGithubdNotReady
}

// MintInstallationToken returns the not-ready problem (DEPLOY-PROV-4
// / ADR-092, issue #739). With no githubd wired, the source-ref
// deploy path renders the same "GitHub integration not configured"
// page the OAuth callback does — this stays consistent until
// slice 8 is live in production.
func (stubGithubdClient) MintInstallationToken(context.Context, string, int64) (string, time.Time, error) {
	return "", time.Time{}, errGithubdNotReady
}

// StreamSourceRef returns the not-ready problem (DEPLOY-PROV-4 /
// ADR-092, issue #739). The handler that consumes this (apid's
// handleSourceRefDeploy) maps the underlying problem Code to
// 503 source_ref_unavailable.
func (stubGithubdClient) StreamSourceRef(context.Context, string, int64, string, string, int64) (*StreamSourceRefResult, error) {
	return nil, errGithubdNotReady
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
// PR-C widens the apid-facing surface to (installID, defaultBranch, err)
// so the renderOAuthCodeCallback handler can seed the dashboard redirect
// without a second round-trip. PR-D / ADR-012 §6 closure: the
// default_branch is now actually surfaced on the gRPC wire (the proto
// field at `default_branch = 2` was reserved since slice 8; PR-D fills
// it at the Server.ExchangeOAuthCode layer so this pass-through is
// no longer a TODO).
func (l *liveClient) ExchangeOAuthCode(ctx context.Context, accountID, code, state string) (string, string, error) {
	return l.c.ExchangeOAuthCode(ctx, accountID, code, state)
}

// ListInstallableRepos passes through to githubdgrpc.Client.ListInstallableRepos.
func (l *liveClient) ListInstallableRepos(ctx context.Context, accountID string, installationID int64) ([]Repo, error) {
	return l.c.ListInstallableRepos(ctx, accountID, installationID)
}

// BindAppRepo passes through to githubdgrpc.Client.BindAppRepo.
func (l *liveClient) BindAppRepo(ctx context.Context, appID, accountID string, installationID int64, repoFullName, productionBranch string) (string, error) {
	return l.c.BindAppRepo(ctx, appID, accountID, installationID, repoFullName, productionBranch)
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
func (l *liveClient) VerifyInstallation(ctx context.Context, installationID int64, expectedLogin string) (bool, string, string, error) {
	return l.c.VerifyInstallation(ctx, installationID, expectedLogin)
}

// MintInstallationToken passes through to
// githubdgrpc.Client.MintInstallationToken (DEPLOY-PROV-4 /
// ADR-092, issue #739). The token never touches disk; it lives
// only inside the RPC response frame and the apid-side
// install-token cache populated by handleSourceRefDeploy.
// Consumers strip it from the (token, expiry, err) tuple and
// discard it before returning.
func (l *liveClient) MintInstallationToken(ctx context.Context, accountID string, installationID int64) (string, time.Time, error) {
	return l.c.MintInstallationToken(ctx, accountID, installationID)
}

// StreamSourceRef passes through to
// githubdgrpc.Client.StreamSourceRef (DEPLOY-PROV-4 / ADR-092,
// issue #739). The returned io.ReadCloser wraps a pipe the
// underlying gRPC streaming receiver drains into; the apid
// handler pipes it into validateAndSpool and calls Close exactly
// once.
func (l *liveClient) StreamSourceRef(ctx context.Context, accountID string, installationID int64, repoFullName, ref string, maxArchiveBytes int64) (*StreamSourceRefResult, error) {
	res, err := l.c.StreamSourceRef(ctx, accountID, installationID, repoFullName, ref, maxArchiveBytes)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	stats := &StreamSourceRefStats{}
	return &StreamSourceRefResult{
		Body:  &liveSourceRefBody{ReadCloser: res.Body, remote: res.Stats, local: stats},
		Stats: stats,
	}, nil
}

type liveSourceRefBody struct {
	io.ReadCloser
	remote *githubdgrpc.StreamSourceRefStats
	local  *StreamSourceRefStats
	once   sync.Once
	err    error
}

func (b *liveSourceRefBody) Close() error {
	b.once.Do(func() {
		b.err = b.ReadCloser.Close()
		if b.remote == nil || b.local == nil {
			return
		}
		b.local.Truncated = b.remote.Truncated
		b.local.BytesStreamed = b.remote.BytesStreamed
		b.local.ResolvedCommitSHA = b.remote.ResolvedCommitSHA
		b.local.Err = b.remote.Err
	})
	return b.err
}

// newGithubdClient is the slice-1 constructor: returns the stub. Slice 7
// replaces with a live dial when cfg.Socket != "". Returning an
// interface (not a concrete *stubGithubdClient) means callers never have
// to type-assert, and the slice-7 swap is a one-line change here.
//
// Issue #95: signature now takes ctx so the dial participates in
// apid's lifecycle; tlsCfg is nil for the loopback UNIX socket.
// Remote-target dial + mTLS will be wired here in the follow-up that
// decouples the control plane.
func newGithubdClient(ctx context.Context, socketPath string, tlsCfg *tls.Config, log *slog.Logger) GithubdClient {
	if socketPath == "" {
		if log != nil {
			log.Info("githubd socket not configured; using stub client (slice 1)")
		}
		return stubGithubdClient{}
	}
	c, err := githubdgrpc.DialContext(ctx, socketPath, tlsCfg)
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
