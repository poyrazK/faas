package scheddgrpc

import (
	"context"
	"errors"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) InterruptHostedTaskWorker(ctx context.Context, req *scheddpb.InterruptHostedTaskWorkerRequest) (*scheddpb.InterruptHostedTaskWorkerResponse, error) {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil || p.LocalAddr == nil || p.LocalAddr.Network() != "unix" {
		return nil, status.Error(codes.PermissionDenied, "rehearsal interruption requires the private scheduler socket")
	}
	if err := req.GetObservedAt().CheckValid(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "missing valid rehearsal lease observation time")
	}
	if _, err := s.authorizeApp(ctx, req.GetAppId()); err != nil {
		return nil, err
	}
	engine, ok := s.engine.(interface {
		InterruptHostedTaskWorker(context.Context, tasktoolrehearsal.WorkerInterruption) (tasktoolrehearsal.StoppedWorker, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "rehearsal interruption is unavailable")
	}
	stopped, err := engine.InterruptHostedTaskWorker(ctx, tasktoolrehearsal.WorkerInterruption{
		AccountID: req.GetAccountId(), RunID: req.GetRunId(), PlanSHA256: req.GetPlanSha256(), TaskID: req.GetTaskId(),
		AppID: req.GetAppId(), DeploymentID: req.GetDeploymentId(), InstanceID: req.GetInstanceId(),
		NodeID: req.GetNodeId(), WakeID: req.GetWakeId(), ObservedAt: req.GetObservedAt().AsTime(),
	})
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		return nil, status.Error(codes.InvalidArgument, "malformed rehearsal identity")
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrNotFound):
		return nil, status.Error(codes.FailedPrecondition, "rehearsal worker no longer matches the observed run")
	case err != nil:
		s.log.Warn("hosted Task worker interruption inconclusive", "instance", req.GetInstanceId(), "err", err)
		return nil, status.Error(codes.Internal, "could not confirm rehearsal worker interruption")
	default:
		return &scheddpb.InterruptHostedTaskWorkerResponse{InstanceId: stopped.InstanceID, DeploymentId: stopped.DeploymentID}, nil
	}
}

func (c *Client) InterruptHostedTaskWorker(ctx context.Context, in tasktoolrehearsal.WorkerInterruption) (tasktoolrehearsal.StoppedWorker, error) {
	reply, err := c.cli.InterruptHostedTaskWorker(ctx, &scheddpb.InterruptHostedTaskWorkerRequest{
		AccountId: in.AccountID, RunId: in.RunID, PlanSha256: in.PlanSHA256, TaskId: in.TaskID,
		AppId: in.AppID, DeploymentId: in.DeploymentID, InstanceId: in.InstanceID, NodeId: in.NodeID,
		WakeId: in.WakeID, ObservedAt: timestamppb.New(in.ObservedAt),
	})
	if err != nil {
		return tasktoolrehearsal.StoppedWorker{}, err
	}
	return tasktoolrehearsal.StoppedWorker{InstanceID: reply.GetInstanceId(), DeploymentID: reply.GetDeploymentId()}, nil
}
