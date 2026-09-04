// Package githubdgrpc turns pkg/githubd.GithubdService into the gRPC
// service defined in api/proto/onebox/faas/githubd/v1 (ADR-012). The
// client here is apid's handle to githubd (the reverse direction of
// the daemon — githubd dials apid for CreateDeploymentFromPush via the
// same shape once that surface is in place). Handlers stay thin —
// each wraps a single Service call and translates its result into the
// proto envelope, routing errors through pkg/grpcerr so apid maps them
// straight to RFC 7807. Mirrors pkg/scheddgrpc on the schedd side.
package githubdgrpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

// Client is apid's handle to githubd's gRPC surface (ADR-012). It is
// the read/write path apid uses to surface the install state, list
// installable repos, bind/unbind apps to repos, and (slice 7+) push
// commits via the webhook-driven path. githubd itself dials apid via
// the same shape in the other direction — see pkg/apidgrpc (slice 7).
type Client struct {
	conn *grpc.ClientConn
	cli  githubdpb.GithubdClient
}

// Dial opens a lazy gRPC connection to githubd's unix socket. The
// socket's 0660/group-`faas` DAC is the only auth in v1.0 (ADR-015),
// so the transport uses insecure credentials over a trusted local
// socket. The connection dials on first RPC; Dial never blocks on
// githubd being up.
//
// Legacy entrypoint kept for source compatibility with existing
// callers and tests; production code should call DialContext.
func Dial(socketPath string) (*Client, error) {
	return DialContext(context.Background(), socketPath, nil)
}

// DialContext opens a lazy gRPC connection to githubd. tlsCfg is
// required for tcp/dns targets (issue #95); nil tlsCfg is fine for the
// single-box unix default. Wire layer performs the mTLS gating.
func DialContext(ctx context.Context, target string, tlsCfg *tls.Config) (*Client, error) {
	if target == "" {
		return nil, errors.New("githubdgrpc: empty githubd target")
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("githubdgrpc: dial githubd %q: %w", target, err)
	}
	return &Client{conn: conn, cli: githubdpb.NewGithubdClient(conn)}, nil
}

// NewClient wraps an already-dialed connection (used by bufconn tests).
func NewClient(conn *grpc.ClientConn) *Client {
	return &Client{conn: conn, cli: githubdpb.NewGithubdClient(conn)}
}

// Close releases the underlying connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// GetInstallState reports the per-account install lifecycle state.
// Mirrors the dashboard's "Connect GitHub" state machine (UX §5.1).
func (c *Client) GetInstallState(ctx context.Context, accountID string) (InstallState, string, string, error) {
	resp, err := c.cli.GetInstallState(ctx, &githubdpb.GetInstallStateRequest{AccountId: accountID})
	if err != nil {
		return InstallStateUnspecified, "", "", liftErr(err)
	}
	return InstallState(resp.GetState()), resp.GetInstallationId(), resp.GetDefaultBranch(), nil
}

// ExchangeOAuthCode turns a GitHub OAuth code into an installation
// record. Returns (installation_id, default_branch, err) — the
// default_branch is empty when the install handshake didn't
// surface one (legacy installations, repository-less installs).
// PR-D / ADR-012 §6 closure: the default_branch flows back to
// apid's renderOAuthCallback handler so the bind picker can
// pre-fill the production branch without a follow-up
// VerifyInstallation RPC.
func (c *Client) ExchangeOAuthCode(ctx context.Context, accountID, code, state string) (string, string, error) {
	resp, err := c.cli.ExchangeOAuthCode(ctx, &githubdpb.ExchangeOAuthCodeRequest{
		AccountId: accountID,
		Code:      code,
		State:     state,
	})
	if err != nil {
		return "", "", liftErr(err)
	}
	return resp.GetInstallationId(), resp.GetDefaultBranch(), nil
}

// ListInstallableRepos returns the catalog of repos the installation
// has access to.
func (c *Client) ListInstallableRepos(ctx context.Context, accountID string) ([]Repo, error) {
	resp, err := c.cli.ListInstallableRepos(ctx, &githubdpb.ListInstallableReposRequest{AccountId: accountID})
	if err != nil {
		return nil, liftErr(err)
	}
	out := make([]Repo, 0, len(resp.GetRepos()))
	for _, r := range resp.GetRepos() {
		out = append(out, Repo{
			FullName:      r.GetFullName(),
			DefaultBranch: r.GetDefaultBranch(),
			Private:       r.GetPrivate(),
		})
	}
	return out, nil
}

// BindAppRepo associates an app with a repo. Idempotent on
// (app_id, repo).
func (c *Client) BindAppRepo(ctx context.Context, appID, accountID, repoFullName, productionBranch string) (string, error) {
	resp, err := c.cli.BindAppRepo(ctx, &githubdpb.BindAppRepoRequest{
		AppId:            appID,
		AccountId:        accountID,
		RepoFullName:     repoFullName,
		ProductionBranch: productionBranch,
	})
	if err != nil {
		return "", liftErr(err)
	}
	return resp.GetBindingId(), nil
}

// UnbindAppRepo removes an app↔repo binding.
func (c *Client) UnbindAppRepo(ctx context.Context, appID, accountID string) error {
	_, err := c.cli.UnbindAppRepo(ctx, &githubdpb.UnbindAppRepoRequest{
		AppId:     appID,
		AccountId: accountID,
	})
	if err != nil {
		return liftErr(err)
	}
	return nil
}

// GetAppBinding returns the current binding for an app. Returns
// (empty, nil) when the app is unbound.
func (c *Client) GetAppBinding(ctx context.Context, appID, accountID string) (AppBinding, error) {
	resp, err := c.cli.GetAppBinding(ctx, &githubdpb.GetAppBindingRequest{
		AppId:     appID,
		AccountId: accountID,
	})
	if err != nil {
		return AppBinding{}, liftErr(err)
	}
	return AppBinding{
		RepoFullName:     resp.GetRepoFullName(),
		ProductionBranch: resp.GetProductionBranch(),
		BindingID:        resp.GetBindingId(),
	}, nil
}

// CreateDeploymentFromPush is the webhook-triggered path: githubd
// turns a verified GitHub push into a deployment row in apid. Returns
// ("", "", nil) when no app is bound to the repo.
func (c *Client) CreateDeploymentFromPush(ctx context.Context, repoFullName, ref, commitSHA, pusher string) (string, string, error) {
	resp, err := c.cli.CreateDeploymentFromPush(ctx, &githubdpb.CreateDeploymentFromPushRequest{
		RepoFullName: repoFullName,
		Ref:          ref,
		CommitSha:    commitSHA,
		Pusher:       pusher,
	})
	if err != nil {
		return "", "", liftErr(err)
	}
	return resp.GetDeploymentId(), resp.GetAppId(), nil
}

// WriteCheck pushes a commit-status update back to GitHub via the
// Checks API. Idempotent on (repo, sha, phase) per pkg/githubd/checks.go.
func (c *Client) WriteCheck(ctx context.Context, repoFullName, commitSHA string, phase CheckPhase, logsURL, summary string) error {
	_, err := c.cli.WriteCheck(ctx, &githubdpb.WriteCheckRequest{
		RepoFullName: repoFullName,
		CommitSha:    commitSHA,
		Phase:        githubdpb.CheckPhase(phase),
		LogsUrl:      logsURL,
		Summary:      summary,
	})
	if err != nil {
		return liftErr(err)
	}
	return nil
}

// VerifyInstallation is the "trust on first contact" check that
// closes review finding #1+#2 for the M7.5 OAuth path. apid's
// /oauth/callback handler calls this before persisting any binding;
// githubd mints the App JWT and confirms the installation actually
// exists for the configured GitHub App. Returns verified=true on a
// real install, verified=false on a forged/unknown id (404 from
// api.github.com); transport errors come back as a non-nil err so
// the dashboard renders the right "couldn't reach GitHub" UX.
//
// PR-B: expectedLogin carries the §11 ownership assertion; accountLogin
// on the response is empty when verified=false (the apid handler must
// not learn whether a forged install_id exists).
func (c *Client) VerifyInstallation(ctx context.Context, installationID int64, expectedLogin string) (verified bool, accountLogin string, defaultBranch string, err error) {
	resp, err := c.cli.VerifyInstallation(ctx, &githubdpb.VerifyInstallationRequest{
		InstallationId: installationID,
		ExpectedLogin:  expectedLogin,
	})
	if err != nil {
		return false, "", "", liftErr(err)
	}
	return resp.GetVerified(), resp.GetAccountLogin(), resp.GetDefaultBranch(), nil
}

// liftErr converts a githubd gRPC error back into the platform's
// *api.Problem so its stable Code + Limit/Observed survive to apid.
// Errors that aren't status-shaped (e.g. a dial failure) pass through
// unchanged. Mirrors scheddgrpc.liftErr.
func liftErr(err error) error {
	if p, ok := grpcerr.FromStatus(err); ok && p != nil {
		return p
	}
	return err
}

// MintInstallationToken wraps the unary gRPC of the same name
// (DEPLOY-PROV-4 / ADR-092, issue #739). apid's handleSourceRefDeploy
// handler calls it to obtain a fresh installation token for the
// durable install the customer bound when connecting GitHub. The
// token never crosses the githubd process boundary on a sticky
// basis — every call mints fresh so a CI runner that got a 401
// from codeload can retry without waiting for the proactive
// refresh window. expiresAt is the GitHub-reported expiry as
// time.Time (zero when githubd stamps a stub).
//
// Error mapping: the wrapped *api.Problem (codes.NotFound →
// github_install_not_found; codes.Unavailable →
// source_ref_unavailable) survives via liftErr so the apid
// handler can branch on api.AsProblem(err) directly.
func (c *Client) MintInstallationToken(ctx context.Context, accountID string, installationID int64) (string, time.Time, error) {
	resp, err := c.cli.MintInstallationToken(ctx, &githubdpb.MintInstallationTokenRequest{
		AccountId:      accountID,
		InstallationId: installationID,
	})
	if err != nil {
		return "", time.Time{}, liftErr(err)
	}
	if resp.GetToken() == "" {
		return "", time.Time{}, fmt.Errorf("githubdgrpc: empty token in MintInstallationToken response")
	}
	exp, perr := time.Parse(time.RFC3339, resp.GetExpiresAt())
	if perr != nil {
		// Defensive — proto/regen puts a non-RFC3339 string here
		// would be a wire-bug. Hand back a zero time so the apid
		// caller can still use the token; the apid-side cache
		// stamps the issuer-reported value, not the parsed one.
		// Return perr so the apid-side log surfaces the wire
		// mismatch (lint: nilerr — don't drop the parse error).
		return resp.GetToken(), time.Time{}, fmt.Errorf("githubdgrpc: parse expires_at %q: %w", resp.GetExpiresAt(), perr)
	}
	return resp.GetToken(), exp, nil
}

// StreamSourceRefResult bundles the streaming body with the
// terminal-frame signals githubd stamps on the last chunk
// (truncated, bytes_streamed). apid's handleSourceRefDeploy reads
// Total only after a successful Read to EOF — populating Total
// happens lazily inside Close so a caller that bails out early
// (size-cap trip, context cancel) still sees the partial byte
// count without re-iterating the stream.
type StreamSourceRefResult struct {
	// Body is the streaming tar.gz bytes. The caller MUST Close
	// it; defer Close immediately after StreamSourceRef returns.
	Body io.ReadCloser
	// Stats is populated on Close. Until Close returns, the pointed-to
	// value holds zero values — readers MUST NOT peek at it before
	// Body.Read returns io.EOF or Body.Close is called.
	Stats *StreamSourceRefStats
}

// StreamSourceRefStats captures the truncated/total fields the
// proto wire carries on each chunk's final frame.
type StreamSourceRefStats struct {
	// Truncated is true when githubd hit the caller's
	// max_archive_bytes cap mid-stream; the apid handler maps
	// this to code=source_too_large (RFC 7807 413).
	Truncated bool
	// BytesStreamed is the post-cap cumulative bytes the githubd
	// streamer forwarded to apid. Recorded on the deployment
	// row's source_bytes column.
	BytesStreamed int64
	// ResolvedCommitSHA is the immutable commit selected for the
	// requested branch, tag, or short SHA.
	ResolvedCommitSHA string
	// Err is the terminal stream error if the body ended
	// prematurely (liftErr-preserved *api.Problem). nil on a
	// clean EOF.
	Err error
}

// StreamSourceRef wraps the server-streaming gRPC of the same name
// (DEPLOY-PROV-4 / ADR-092, issue #739). It drains every chunk into
// a single io.ReadCloser the caller pipes straight into
// validateAndSpool — no double buffering, no tee. Stats +
// terminal error surface on Close.
//
// Error mapping mirrors MintInstallationToken: liftErr preserves
// the *api.Problem Code so api.AsProblem(err) reaches the
// handler untouched. A streaming error after the gRPC Invoke
// succeeded surfaces as Stats.Err (Close returns it), so the
// caller can render the stable Code without re-walking the
// stream.
func (c *Client) StreamSourceRef(ctx context.Context, accountID string, installationID int64, repoFullName, ref string, maxArchiveBytes int64) (*StreamSourceRefResult, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := c.cli.StreamSourceRef(streamCtx, &githubdpb.StreamSourceRefRequest{
		AccountId:       accountID,
		InstallationId:  installationID,
		RepoFullName:    repoFullName,
		Ref:             ref,
		MaxArchiveBytes: maxArchiveBytes,
	})
	if err != nil {
		cancel()
		return nil, liftErr(err)
	}
	pr, pw := io.Pipe()
	res := &streamSourceRefConn{
		pr:         pr,
		pw:         pw,
		stream:     stream,
		stats:      StreamSourceRefStats{},
		pumpExited: make(chan struct{}),
		cancel:     cancel,
	}
	// Single producer goroutine drains the gRPC stream into the
	// pipe. Close-on-error surfaces to Read; Close-on-EOF cleanly
	// terminates the reader.
	go res.pump()
	return &StreamSourceRefResult{Body: res, Stats: &res.stats}, nil
}

// streamSourceRefConn is the io.ReadCloser + terminal-stats bundle
// StreamSourceRef returns. The fields are accessible only on the
// single goroutine that owns the call: apid's handler reads in
// the request goroutine, the pump writes from the gRPC recv
// goroutine. State mutation happens inside Close (after pump
// returns) so the handler observes a stable view.
type streamSourceRefConn struct {
	pr         *io.PipeReader
	pw         *io.PipeWriter
	stream     grpc.ServerStreamingClient[githubdpb.StreamSourceRefChunk]
	stats      StreamSourceRefStats
	pumpExited chan struct{}
	cancel     context.CancelFunc
	closeOnce  sync.Once
	closeErr   error
}

// pump drains Recv into the pipe. It is the single writer of pw.
func (s *streamSourceRefConn) pump() {
	defer close(s.pumpExited)
	defer func() {
		if s.cancel != nil {
			s.cancel()
		}
	}()
	defer func() { _ = s.pw.Close() }()
	for {
		chunk, rerr := s.stream.Recv()
		if errors.Is(rerr, io.EOF) {
			return
		}
		if rerr != nil {
			s.stats.Err = liftErr(rerr)
			_ = s.pw.CloseWithError(s.stats.Err)
			return
		}
		if chunk.GetTruncated() {
			s.stats.Truncated = true
		}
		if chunk.GetBytesStreamed() > 0 {
			s.stats.BytesStreamed = chunk.GetBytesStreamed()
		}
		if chunk.GetResolvedCommitSha() != "" {
			s.stats.ResolvedCommitSHA = chunk.GetResolvedCommitSha()
		}
		if n := len(chunk.GetData()); n > 0 {
			if _, werr := s.pw.Write(chunk.GetData()); werr != nil {
				// Reader went away (Close before EOF). The
				// caller is fine; just exit quietly.
				return
			}
		}
	}
}

// Read satisfies io.Reader. Bytes flow from the streaming gRPC
// goroutine through the pipe into apid's validateAndSpool.
func (s *streamSourceRefConn) Read(p []byte) (int, error) {
	return s.pr.Read(p)
}

// Close cancels the child RPC context, shuts down the read side,
// and waits for the pump to return before handing the caller the
// terminal stats + error. Cancelling first is important when the
// caller stops at the archive cap: Recv may otherwise remain blocked
// on a still-streaming githubd response. Idempotent and safe to defer;
// double-close is a no-op.
func (s *streamSourceRefConn) Close() error {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		_ = s.pr.Close()
		<-s.pumpExited
		s.closeErr = s.stats.Err
	})
	return s.closeErr
}
