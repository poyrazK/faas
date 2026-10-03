package vmmdgrpc

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// JobClaimStore is the durable execution view read by vmmd before a job boot.
// schedd remains the sole writer of claims and instance lifecycle state.
type JobClaimStore interface {
	JobExecutionInstanceByID(context.Context, string) (state.JobExecutionInstance, error)
	JobRunGetByID(context.Context, string) (state.JobRun, error)
	JobTaskGet(context.Context, string, int) (state.JobTask, error)
	JobGetByID(context.Context, string) (state.Job, error)
	ComputeNodeByID(context.Context, string) (state.ComputeNode, error)
}

func (s *Server) WithJobClaimStore(store JobClaimStore) *Server {
	if s != nil {
		s.jobClaimStore = store
	}
	return s
}

// validateJobPlacement uses the receiver's registered identity, rather than
// trusting the request's node id. A stale or misrouted RPC cannot allocate a VM.
func (s *Server) validateJobPlacement(ctx context.Context, boot fcvm.JobBootRequest) error {
	if s.jobClaimStore == nil {
		return status.Error(codes.Unavailable, "cannot verify job claim")
	}
	if s.nodeID == "" || boot.NodeID != s.nodeID {
		return status.Error(codes.FailedPrecondition, "job claim belongs to another compute node")
	}
	instance, err := s.jobClaimStore.JobExecutionInstanceByID(ctx, boot.Instance)
	if err != nil {
		return jobClaimReadError(err, "instance")
	}
	if instance.ID != boot.Instance || instance.Kind != "job_task" || instance.NodeID != s.nodeID || instance.JobID == "" ||
		instance.RAMMB != boot.MemSizeMiB ||
		(instance.State != "cold_booting" && instance.State != "running") {
		return status.Error(codes.FailedPrecondition, "job instance does not match the active claim")
	}
	run, err := s.jobClaimStore.JobRunGetByID(ctx, boot.RunID)
	if err != nil {
		return jobClaimReadError(err, "run")
	}
	if run.ID != boot.RunID || run.JobID != instance.JobID || run.AccountID != boot.AccountID || run.AggregateStatus == "cancelled" {
		return status.Error(codes.FailedPrecondition, "job run does not match the active claim")
	}
	if err := s.validateJobClaimRegion(ctx, instance.JobID, boot.AccountID); err != nil {
		return err
	}
	// Check the current task lease last, immediately before the boot boundary.
	return s.validateJobTaskClaim(ctx, boot)
}

func (s *Server) validateJobTaskClaim(ctx context.Context, boot fcvm.JobBootRequest) error {
	task, err := s.jobClaimStore.JobTaskGet(ctx, boot.RunID, boot.TaskIndex)
	if err != nil {
		return jobClaimReadError(err, "task")
	}
	if task.RunID != boot.RunID || task.TaskIndex != boot.TaskIndex || task.Status != "claimed" || task.InstanceID == nil || *task.InstanceID != boot.Instance ||
		task.LeaseToken == nil || *task.LeaseToken != boot.LeaseToken ||
		task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(time.Now()) {
		return status.Error(codes.FailedPrecondition, "job task lease is no longer current")
	}
	return nil
}

func (s *Server) validateJobClaimRegion(ctx context.Context, jobID, accountID string) error {
	job, err := s.jobClaimStore.JobGetByID(ctx, jobID)
	if err != nil {
		return jobClaimReadError(err, "definition")
	}
	if job.ID != jobID || job.AccountID != accountID || job.Status != "active" {
		return status.Error(codes.FailedPrecondition, "job definition is no longer active")
	}
	node, err := s.jobClaimStore.ComputeNodeByID(ctx, s.nodeID)
	if err != nil {
		return jobClaimReadError(err, "compute node")
	}
	if node.ID != s.nodeID || !node.Active || !state.ComputeRegionAllowsNode(job.ComputeRegion, node) {
		return status.Error(codes.FailedPrecondition, "job compute node is outside the selected region or inactive")
	}
	return nil
}

func jobClaimReadError(err error, resource string) error {
	if errors.Is(err, state.ErrNotFound) {
		return status.Errorf(codes.FailedPrecondition, "job claim %s is missing", resource)
	}
	return status.Errorf(codes.Unavailable, "cannot verify job claim %s", resource)
}
