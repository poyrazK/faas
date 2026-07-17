package githubdgrpc

import (
	"context"
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
type Service interface {
	GetInstallState(accountID string) (InstallState, string, string, error)
	ExchangeOAuthCode(accountID, code, state string) (string, error)
	ListInstallableRepos(accountID string) ([]Repo, error)
	BindAppRepo(appID, accountID, repoFullName, productionBranch string) (string, error)
	UnbindAppRepo(appID, accountID string) error
	GetAppBinding(appID, accountID string) (AppBinding, error)
	CreateDeploymentFromPush(repoFullName, ref, commitSHA, pusher string) (string, string, error)
	WriteCheck(repoFullName, commitSHA string, phase CheckPhase, logsURL, summary string) error
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
func (s *Server) ExchangeOAuthCode(ctx context.Context, req *githubdpb.ExchangeOAuthCodeRequest) (*githubdpb.ExchangeOAuthCodeResponse, error) {
	const op = "ExchangeOAuthCode"
	start := time.Now()
	instID, err := s.svc.ExchangeOAuthCode(req.GetAccountId(), req.GetCode(), req.GetState())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.ExchangeOAuthCodeResponse{InstallationId: instID}, nil
}

// ListInstallableRepos passes through to Service.ListInstallableRepos.
func (s *Server) ListInstallableRepos(ctx context.Context, req *githubdpb.ListInstallableReposRequest) (*githubdpb.ListInstallableReposResponse, error) {
	const op = "ListInstallableRepos"
	start := time.Now()
	repos, err := s.svc.ListInstallableRepos(req.GetAccountId())
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
	bindingID, err := s.svc.BindAppRepo(req.GetAppId(), req.GetAccountId(), req.GetRepoFullName(), req.GetProductionBranch())
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
func (UnimplementedService) ExchangeOAuthCode(string, string, string) (string, error) {
	return "", status.Error(codes.Unimplemented, "githubd: ExchangeOAuthCode not yet wired (slice 8)")
}

// ListInstallableRepos returns Unimplemented. Slice 8 replaces this.
func (UnimplementedService) ListInstallableRepos(string) ([]Repo, error) {
	return nil, status.Error(codes.Unimplemented, "githubd: ListInstallableRepos not yet wired (slice 8)")
}

// BindAppRepo returns Unimplemented. Slice 8 replaces this.
func (UnimplementedService) BindAppRepo(string, string, string, string) (string, error) {
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
