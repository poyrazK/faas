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
	"errors"
	"fmt"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
func Dial(socketPath string) (*Client, error) {
	if socketPath == "" {
		return nil, errors.New("githubdgrpc: empty githubd socket path")
	}
	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("githubdgrpc: dial githubd %q: %w", socketPath, err)
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
// record. Returns the new installation_id on success.
func (c *Client) ExchangeOAuthCode(ctx context.Context, accountID, code, state string) (string, error) {
	resp, err := c.cli.ExchangeOAuthCode(ctx, &githubdpb.ExchangeOAuthCodeRequest{
		AccountId: accountID,
		Code:      code,
		State:     state,
	})
	if err != nil {
		return "", liftErr(err)
	}
	return resp.GetInstallationId(), nil
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
