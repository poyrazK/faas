package githubdgrpc

import (
	"context"
	"io"
	"log/slog"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Service is the slice of pkg/githubd that the gRPC handlers need.
// Slice 1 registers only an Unimplemented service; slices 7-8 wire the
// real methods here. Defining the interface up-front means apid and
// tests can call githubd today and exercise the round-trip before any
// business logic lands.
//
// PR-C widens ExchangeOAuthCode's return shape to also carry
// default_branch — the apid handler needs it to seed BindAppRepo on
// the success path. The proto wire already had
// `default_branch = 2` on ExchangeOAuthCodeResponse (just unused),
// so this is a wire-compatible, additive widening.
type Service interface {
	GetInstallState(accountID string) (InstallState, string, string, error)
	ExchangeOAuthCode(accountID, code, state string) (installationID, defaultBranch string, err error)
	ListInstallableRepos(accountID string, installationID int64) ([]Repo, error)
	BindAppRepo(appID, accountID string, installationID int64, repoFullName, productionBranch string) (string, error)
	UnbindAppRepo(appID, accountID string) error
	GetAppBinding(appID, accountID string) (AppBinding, error)
	CreateDeploymentFromPush(repoFullName, ref, commitSHA, pusher string) (string, string, error)
	WriteCheck(repoFullName, commitSHA string, phase CheckPhase, logsURL, summary string) error
	// VerifyInstallation confirms an installation_id is real for the
	// configured GitHub App. apid's /oauth/callback handler calls this
	// before persisting the binding, so a forged callback cannot claim
	// an install_id the customer doesn't own (review finding #2).
	//
	// PR-B §11 ownership proof: when expectedLogin is non-empty, the
	// install's account.login MUST match for verified=true. Returns
	// accountLogin (the install's actual GitHub login) on the success
	// path so the apid handler can audit-log it. On mismatch
	// (verified=false, err=nil) accountLogin is empty so a forged
	// caller cannot learn whether the install exists.
	VerifyInstallation(installationID int64, expectedLogin string) (verified bool, accountLogin string, defaultBranch string, err error)

	// MintInstallationToken returns a fresh installation token for
	// (accountID, installationID) (DEPLOY-PROV-4 / ADR-092, issue #739).
	// The token is the canonical shape githubd's TokenCache hands out
	// — apid's source-ref deploy handler calls this RPC to
	// authenticate the codeload fetch when no installation is
	// available locally. The Service seam forces a fresh mint so a CI
	// runner that just got a 401 from codeload can retry without
	// waiting for the proactive refresh window. expiresAt is the
	// GitHub-reported expiry timestamp; apid stamps it on its
	// install-token cache so the next call can short-circuit a
	// cache miss.
	MintInstallationToken(accountID string, installationID int64) (token string, expiresAt time.Time, err error)

	// StreamSourceRef streams the raw tar.gz archive for a
	// (repo, ref) pair over the durable install's installation token
	// (DEPLOY-PROV-4 / ADR-092, issue #739). The returned
	// io.ReadCloser is the response body wrapped in
	// io.LimitReader(maxArchiveBytes + 1, …); the caller (apid's
	// handleSourceRefDeploy) is responsible for piping it into
	// validateAndSpool. The truncated flag surfaces when the cap
	// is hit; bytesStreamed is the post-cap cumulative count for
	// the deployment row's source_bytes column.
	StreamSourceRef(ctx context.Context, accountID string, installationID int64, repoFullName, ref string, maxArchiveBytes int64) (rc io.ReadCloser, resolvedCommitSHA string, truncated bool, bytesStreamed int64, err error)
}

// Server implements githubdpb.GithubdServer. It wraps a Service so
// unit tests can pass a fake (see bufconn_test.go). Slice 1 returns
// Unimplemented everywhere; slice 7 wires CreateDeploymentFromPush +
// WriteCheck, slice 8 wires the OAuth + binding methods.
type Server struct {
	githubdpb.UnimplementedGithubdServer

	svc Service
	ops *wire.OpsMetrics
	log *slog.Logger
}

// New wires the server. ops may be nil (a throwaway registry); log
// may be nil (slog default). The Service is required and is the seam
// for slice 1's Unimplemented pass-through (pass githubdpb's
// UnimplementedGithubdServer-adapter via UnimplementedService below).
func New(svc Service, ops *wire.OpsMetrics, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if ops == nil {
		ops = wire.NewOpsMetrics("githubd_test")
	}
	if svc == nil {
		svc = UnimplementedService{}
	}
	return &Server{svc: svc, ops: ops, log: log}
}

// Register binds s to a gRPC server.
func (s *Server) Register(g *grpc.Server) {
	githubdpb.RegisterGithubdServer(g, s)
}

// GetInstallState passes through to Service.GetInstallState. Slice 1
// returns Unimplemented (state == UNSPECIFIED).
func (s *Server) GetInstallState(ctx context.Context, req *githubdpb.GetInstallStateRequest) (*githubdpb.GetInstallStateResponse, error) {
	const op = "GetInstallState"
	start := time.Now()
	state, instID, branch, err := s.svc.GetInstallState(req.GetAccountId())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.GetInstallStateResponse{
		State:          githubdpb.InstallState(state),
		InstallationId: instID,
		DefaultBranch:  branch,
	}, nil
}

// ExchangeOAuthCode passes through to Service.ExchangeOAuthCode.
// PR-C: the Service interface widens to (string, string, error)
// (installation_id, default_branch, err). The proto wire stays at
// (string, error) for now — the default_branch is consumed via
// apid's internal VerifyInstallation follow-up (which already
// returns it). The widening is purely an in-process Service
// shape; the proto wire is unchanged, so no regeneration needed.
func (s *Server) ExchangeOAuthCode(ctx context.Context, req *githubdpb.ExchangeOAuthCodeRequest) (*githubdpb.ExchangeOAuthCodeResponse, error) {
	const op = "ExchangeOAuthCode"
	start := time.Now()
	instID, defaultBranch, err := s.svc.ExchangeOAuthCode(req.GetAccountId(), req.GetCode(), req.GetState())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	// PR-D / ADR-012 §6 closure: surface default_branch on the
	// wire. The proto field (`default_branch = 2`) has been
	// reserved since the slice-8 proto commit; the consumer
	// (apid's renderOAuthCallback) reads it to pre-fill the
	// bind picker without a follow-up VerifyInstallation RPC.
	return &githubdpb.ExchangeOAuthCodeResponse{
		InstallationId: instID,
		DefaultBranch:  defaultBranch,
	}, nil
}

// ListInstallableRepos passes through to Service.ListInstallableRepos.
func (s *Server) ListInstallableRepos(ctx context.Context, req *githubdpb.ListInstallableReposRequest) (*githubdpb.ListInstallableReposResponse, error) {
	const op = "ListInstallableRepos"
	start := time.Now()
	repos, err := s.svc.ListInstallableRepos(req.GetAccountId(), req.GetInstallationId())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	pb := make([]*githubdpb.Repo, 0, len(repos))
	for _, r := range repos {
		pb = append(pb, &githubdpb.Repo{
			FullName:      r.FullName,
			DefaultBranch: r.DefaultBranch,
			Private:       r.Private,
		})
	}
	return &githubdpb.ListInstallableReposResponse{Repos: pb}, nil
}

// BindAppRepo passes through to Service.BindAppRepo.
func (s *Server) BindAppRepo(ctx context.Context, req *githubdpb.BindAppRepoRequest) (*githubdpb.BindAppRepoResponse, error) {
	const op = "BindAppRepo"
	start := time.Now()
	bindingID, err := s.svc.BindAppRepo(req.GetAppId(), req.GetAccountId(), req.GetInstallationId(), req.GetRepoFullName(), req.GetProductionBranch())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.BindAppRepoResponse{BindingId: bindingID}, nil
}

// UnbindAppRepo passes through to Service.UnbindAppRepo.
func (s *Server) UnbindAppRepo(ctx context.Context, req *githubdpb.UnbindAppRepoRequest) (*githubdpb.UnbindAppRepoResponse, error) {
	const op = "UnbindAppRepo"
	start := time.Now()
	err := s.svc.UnbindAppRepo(req.GetAppId(), req.GetAccountId())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.UnbindAppRepoResponse{}, nil
}

// GetAppBinding passes through to Service.GetAppBinding.
func (s *Server) GetAppBinding(ctx context.Context, req *githubdpb.GetAppBindingRequest) (*githubdpb.GetAppBindingResponse, error) {
	const op = "GetAppBinding"
	start := time.Now()
	b, err := s.svc.GetAppBinding(req.GetAppId(), req.GetAccountId())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.GetAppBindingResponse{
		RepoFullName:     b.RepoFullName,
		ProductionBranch: b.ProductionBranch,
		BindingId:        b.BindingID,
	}, nil
}

// CreateDeploymentFromPush passes through to Service.CreateDeploymentFromPush.
func (s *Server) CreateDeploymentFromPush(ctx context.Context, req *githubdpb.CreateDeploymentFromPushRequest) (*githubdpb.CreateDeploymentFromPushResponse, error) {
	const op = "CreateDeploymentFromPush"
	start := time.Now()
	depID, appID, err := s.svc.CreateDeploymentFromPush(req.GetRepoFullName(), req.GetRef(), req.GetCommitSha(), req.GetPusher())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.CreateDeploymentFromPushResponse{
		DeploymentId: depID,
		AppId:        appID,
	}, nil
}

// WriteCheck passes through to Service.WriteCheck.
func (s *Server) WriteCheck(ctx context.Context, req *githubdpb.WriteCheckRequest) (*githubdpb.WriteCheckResponse, error) {
	const op = "WriteCheck"
	start := time.Now()
	err := s.svc.WriteCheck(req.GetRepoFullName(), req.GetCommitSha(), CheckPhase(req.GetPhase()), req.GetLogsUrl(), req.GetSummary())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.WriteCheckResponse{}, nil
}

// VerifyInstallation passes through to Service.VerifyInstallation.
// Called by apid's /oauth/callback handler before persisting a binding
// (review finding #1+#2 closure); githubd mints the App JWT and
// confirms the installation_id actually exists for the configured
// GitHub App. PR-B adds the §11 ownership proof: req.GetExpectedLogin
// (when non-empty) must match the install's account.login.
//
// A forged callback without a real install returns verified=false
// (or a non-nil err); the dashboard treats both as "401 the user,
// don't persist".
func (s *Server) VerifyInstallation(ctx context.Context, req *githubdpb.VerifyInstallationRequest) (*githubdpb.VerifyInstallationResponse, error) {
	const op = "VerifyInstallation"
	start := time.Now()
	verified, accountLogin, defaultBranch, err := s.svc.VerifyInstallation(req.GetInstallationId(), req.GetExpectedLogin())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.VerifyInstallationResponse{
		Verified:      verified,
		DefaultBranch: defaultBranch,
		AccountLogin:  accountLogin,
	}, nil
}

// MintInstallationToken passes through to Service.MintInstallationToken
// (DEPLOY-PROV-4 / ADR-092, issue #739). Called by apid's
// handleSourceRefDeploy so the install-token-bound codeload fetch
// can authenticate without the token ever crossing the apid process
// boundary (it stays scoped to the unary RPC response).
//
// Errors map:
//   - githubd.ErrNoBinding → codes.NotFound (the apid handler turns
//     this into 404 + code=github_install_not_found).
//   - any other error → codes.Unavailable via toStatusErr.
func (s *Server) MintInstallationToken(ctx context.Context, req *githubdpb.MintInstallationTokenRequest) (*githubdpb.MintInstallationTokenResponse, error) {
	const op = "MintInstallationToken"
	start := time.Now()
	token, expiresAt, err := s.svc.MintInstallationToken(req.GetAccountId(), req.GetInstallationId())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.MintInstallationTokenResponse{
		Token:     token,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
	}, nil
}

// StreamSourceRef passes through to Service.StreamSourceRef
// (DEPLOY-PROV-4 / ADR-092, issue #739). Called by apid's
// handleSourceRefDeploy; the server-streaming wire shape lets
// a Free-plan tarball that exceeds SourceTarballMaxMB be rejected
// mid-flight (chunk carrying truncated=true) rather than
// buffered entirely in memory. bytes_streamed is echoed on the
// final chunk so apid can record source_bytes on the deployment
// row without summing data fields.
//
// Errors map:
//   - githubd.ErrNoBinding → codes.NotFound.
//   - gitfetch.ErrBadArchive (wrapped) → codes.InvalidArgument.
//   - gitfetch.ErrUnauthorized (wrapped) → codes.Unauthenticated.
//   - any other error → codes.Unavailable via toStatusErr.
//
// The streaming body is consumed server-side; once the stream
// completes the inner io.ReadCloser is closed. The chunk loop
// reads in 32 KiB frames (a conservative mid-point between
// gRPC's default 16 KiB chunk and the codeload archive's
// typical block size).
func (s *Server) StreamSourceRef(req *githubdpb.StreamSourceRefRequest, stream grpc.ServerStreamingServer[githubdpb.StreamSourceRefChunk]) error {
	const op = "StreamSourceRef"
	start := time.Now()
	rc, resolvedCommitSHA, serviceTruncated, _, err := s.svc.StreamSourceRef(
		stream.Context(),
		req.GetAccountId(),
		req.GetInstallationId(),
		req.GetRepoFullName(),
		req.GetRef(),
		req.GetMaxArchiveBytes(),
	)
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return toStatusErr(err)
	}
	if rc == nil {
		return status.Error(codes.Unavailable, "githubd: source-ref streamer returned nil body")
	}
	defer func() { _ = rc.Close() }()

	const chunkSize = 32 * 1024
	buf := make([]byte, chunkSize)
	var streamed int64
	for {
		n, rerr := rc.Read(buf)
		if n > 0 {
			streamed += int64(n)
			chunk := &githubdpb.StreamSourceRefChunk{
				Data:          append([]byte(nil), buf[:n]...),
				BytesStreamed: streamed,
				Truncated: (serviceTruncated ||
					(req.GetMaxArchiveBytes() > 0 && streamed > req.GetMaxArchiveBytes())) &&
					rerr == io.EOF,
				ResolvedCommitSha: resolvedCommitSHA,
			}
			if serr := stream.Send(chunk); serr != nil {
				return toStatusErr(serr)
			}
		}
		if rerr == io.EOF {
			if n == 0 {
				// A metadata-only terminal frame is needed for empty
				// archives and for streams whose final read returned
				// no data. It also carries the canonical SHA.
				if serr := stream.Send(&githubdpb.StreamSourceRefChunk{
					BytesStreamed:     streamed,
					Truncated:         serviceTruncated || (req.GetMaxArchiveBytes() > 0 && streamed > req.GetMaxArchiveBytes()),
					ResolvedCommitSha: resolvedCommitSHA,
				}); serr != nil {
					return toStatusErr(serr)
				}
			}
			return nil
		}
		if rerr != nil {
			return toStatusErr(rerr)
		}
	}
}

// toStatusErr converts a Service error to a gRPC status error. It
// preserves an existing *status.Status (so slice 1's codes.Unimplemented
// survives the round-trip), wraps *api.Problem via grpcerr.ToStatus
// (so apid's stable Code reaches the dashboard), and falls back to
// codes.Internal for plain errors. Mirrors scheddgrpc.toProblem.
func toStatusErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	if p := api.AsProblem(err); p != nil {
		return grpcerr.ToStatus(p)
	}
	return status.Error(codes.Internal, err.Error())
}

// UnimplementedService is the slice-1 default. Every method returns
// codes.Unimplemented so the round-trip exercises the gRPC plumbing
// without committing to a business-logic shape before slice 7.
type UnimplementedService struct{}

// GetInstallState returns Unimplemented. Slice 7 replaces this.
func (UnimplementedService) GetInstallState(string) (InstallState, string, string, error) {
	return InstallStateUnspecified, "", "", status.Error(codes.Unimplemented, "githubd: GetInstallState not yet wired (slice 8)")
}

// ExchangeOAuthCode returns Unimplemented. Slice 8 replaces this.
// PR-C widens to (string, string, error) so the response can carry
// default_branch.
func (UnimplementedService) ExchangeOAuthCode(string, string, string) (string, string, error) {
	return "", "", status.Error(codes.Unimplemented, "githubd: ExchangeOAuthCode not yet wired (slice 8)")
}

// ListInstallableRepos returns Unimplemented. Slice 8 replaces this.
func (UnimplementedService) ListInstallableRepos(string, int64) ([]Repo, error) {
	return nil, status.Error(codes.Unimplemented, "githubd: ListInstallableRepos not yet wired (slice 8)")
}

// BindAppRepo returns Unimplemented. Slice 8 replaces this.
func (UnimplementedService) BindAppRepo(string, string, int64, string, string) (string, error) {
	return "", status.Error(codes.Unimplemented, "githubd: BindAppRepo not yet wired (slice 8)")
}

// UnbindAppRepo returns Unimplemented. Slice 8 replaces this.
func (UnimplementedService) UnbindAppRepo(string, string) error {
	return status.Error(codes.Unimplemented, "githubd: UnbindAppRepo not yet wired (slice 8)")
}

// GetAppBinding returns Unimplemented. Slice 8 replaces this.
func (UnimplementedService) GetAppBinding(string, string) (AppBinding, error) {
	return AppBinding{}, status.Error(codes.Unimplemented, "githubd: GetAppBinding not yet wired (slice 8)")
}

// CreateDeploymentFromPush returns Unimplemented. Slice 7 replaces this.
func (UnimplementedService) CreateDeploymentFromPush(string, string, string, string) (string, string, error) {
	return "", "", status.Error(codes.Unimplemented, "githubd: CreateDeploymentFromPush not yet wired (slice 7)")
}

// WriteCheck returns Unimplemented. Slice 7 replaces this.
func (UnimplementedService) WriteCheck(string, string, CheckPhase, string, string) error {
	return status.Error(codes.Unimplemented, "githubd: WriteCheck not yet wired (slice 7)")
}

// VerifyInstallation returns Unimplemented. Replaced by the real
// githubd.RealService in cmd/githubd/main.go (slice 8 closure for
// review finding #2). The slice-1 / test build keeps returning
// Unimplemented so the round-trip exercises the gRPC plumbing
// without committing to the verify-via-GitHub-API shape.
func (UnimplementedService) VerifyInstallation(int64, string) (bool, string, string, error) {
	return false, "", "", status.Error(codes.Unimplemented, "githubd: VerifyInstallation not yet wired (slice 8)")
}

// MintInstallationToken returns Unimplemented. Replaced by the real
// githubd.RealService (cmd/githubd/realservice.go) for the DEPLOY-PROV-4
// / ADR-092 (issue #739) source-ref deploy path. The slice-1 / test
// build keeps returning Unimplemented so the round-trip exercises
// the gRPC plumbing without committing to the TokenCache-backed
// shape before PR-A lands.
func (UnimplementedService) MintInstallationToken(string, int64) (string, time.Time, error) {
	return "", time.Time{}, status.Error(codes.Unimplemented, "githubd: MintInstallationToken not yet wired (DEPLOY-PROV-4)")
}

// StreamSourceRef returns Unimplemented. Replaced by the real
// githubd.RealService (cmd/githubd/realservice.go) for the
// DEPLOY-PROV-4 / ADR-092 (issue #739) source-ref deploy path.
// The slice-1 / test build keeps returning Unimplemented so the
// round-trip exercises the gRPC plumbing without committing to
// the codeload-streaming shape before PR-A lands.
func (UnimplementedService) StreamSourceRef(context.Context, string, int64, string, string, int64) (io.ReadCloser, string, bool, int64, error) {
	return nil, "", false, 0, status.Error(codes.Unimplemented, "githubd: StreamSourceRef not yet wired (DEPLOY-PROV-4)")
}
