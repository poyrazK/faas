// ADR-594: boot admission must be corroborated by the receiving vmmd's
// registered identity and current durable job/task claim before VM allocation.
package vmmdgrpc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type jobClaimSnapshot struct {
	instance   state.Instance
	run        state.JobRun
	task       state.JobTask
	job        state.Job
	node       state.ComputeNode
	readError  error
	failedRead string
}

func jobClaimsForRequest(req *vmmdpb.JobColdBootRequest) *jobClaimSnapshot {
	instance, token, region := req.GetInstance(), req.GetLeaseToken(), "eu-jobs"
	expires := time.Now().Add(time.Minute)
	return &jobClaimSnapshot{
		instance: state.Instance{ID: instance, NodeID: req.GetNodeId(), Kind: "job_task", State: "cold_booting", RAMMB: int(req.GetMemSizeMib()), JobID: "job", JobRunID: req.GetRunId(), JobTaskIndex: int(req.GetTaskIndex())},
		run:      state.JobRun{ID: req.GetRunId(), JobID: "job", AccountID: req.GetAccountId(), AggregateStatus: "running"},
		task:     state.JobTask{RunID: req.GetRunId(), TaskIndex: int(req.GetTaskIndex()), Status: "claimed", InstanceID: &instance, LeaseToken: &token, LeaseExpiresAt: &expires},
		job:      state.Job{ID: "job", AccountID: req.GetAccountId(), Status: "active", ComputeRegion: region},
		node:     state.ComputeNode{ID: req.GetNodeId(), Active: true, Region: &region},
	}
}

func (s *jobClaimSnapshot) JobExecutionInstanceByID(context.Context, string) (state.JobExecutionInstance, error) {
	return state.JobExecutionInstance{
		ID: s.instance.ID, JobID: s.instance.JobID, NodeID: s.instance.NodeID,
		Kind: s.instance.Kind, State: s.instance.State, RAMMB: s.instance.RAMMB,
	}, s.err("instance")
}
func (s *jobClaimSnapshot) JobRunGetByID(context.Context, string) (state.JobRun, error) {
	return s.run, s.err("run")
}
func (s *jobClaimSnapshot) JobTaskGet(context.Context, string, int) (state.JobTask, error) {
	return s.task, s.err("task")
}
func (s *jobClaimSnapshot) JobGetByID(context.Context, string) (state.Job, error) {
	return s.job, s.err("job")
}
func (s *jobClaimSnapshot) ComputeNodeByID(context.Context, string) (state.ComputeNode, error) {
	return s.node, s.err("node")
}
func (s *jobClaimSnapshot) err(resource string) error {
	if resource == s.failedRead {
		return s.readError
	}
	return nil
}

func jobPlacementRequest() *vmmdpb.JobColdBootRequest {
	return &vmmdpb.JobColdBootRequest{
		Instance: "instance", AccountId: "account", NodeId: "worker", Plan: "hobby",
		RunId: "run", ImageRef: "jobs/image.ext4", KernelKey: "kernel", BaseKey: "base",
		Command: []string{"/bin/true"}, VcpuCount: 1, MemSizeMib: 128, TaskTimeoutSec: 30, LeaseToken: "lease",
	}
}

func TestJobColdBoot_DurablePlacementFence(t *testing.T) {
	for _, scenario := range []string{
		"owner", "unconstrained", "same-region wrong receiver", "forged request node", "missing identity", "missing claim reader",
		"wrong run", "wrong task", "wrong account", "wrong instance kind", "stopped instance", "different job", "different RAM",
		"cancelled run", "cancelled task", "different task instance", "missing task instance",
		"stale lease token", "missing lease token", "expired lease", "missing lease expiry", "paused definition",
		"foreign region", "inactive worker", "other dispatcher lease owner",
	} {
		t.Run(scenario, func(t *testing.T) {
			req := jobPlacementRequest()
			claims := jobClaimsForRequest(req)
			nodeID := req.GetNodeId()
			var store vmmdgrpc.JobClaimStore = claims
			want := codes.FailedPrecondition
			switch scenario {
			case "owner", "other dispatcher lease owner":
				want = codes.OK
				other := "control-plane-scheduler"
				claims.task.LastLeaseNode = &other
			case "unconstrained":
				claims.job.ComputeRegion = ""
				want = codes.OK
			case "same-region wrong receiver":
				nodeID = "peer"
			case "forged request node":
				nodeID, req.NodeId = "peer", "peer"
			case "missing identity":
				nodeID = ""
			case "missing claim reader":
				store = nil
				want = codes.Unavailable
			case "wrong run":
				req.RunId = "other-run"
			case "wrong task":
				req.TaskIndex = 1
			case "wrong account":
				req.AccountId = "other-account"
			case "wrong instance kind":
				claims.instance.Kind = "app"
			case "stopped instance":
				claims.instance.State = "stopped"
			case "different RAM":
				req.MemSizeMib++
			case "different job":
				claims.run.JobID = "other-job"
			case "cancelled run":
				claims.run.AggregateStatus = "cancelled"
			case "cancelled task":
				claims.task.Status = "cancelled"
			case "different task instance":
				other := "other-instance"
				claims.task.InstanceID = &other
			case "missing task instance":
				claims.task.InstanceID = nil
			case "stale lease token":
				req.LeaseToken = "old-lease"
			case "missing lease token":
				claims.task.LeaseToken = nil
			case "expired lease":
				expired := time.Now().Add(-time.Minute)
				claims.task.LeaseExpiresAt = &expired
			case "missing lease expiry":
				claims.task.LeaseExpiresAt = nil
			case "paused definition":
				claims.job.Status = "paused"
			case "foreign region":
				claims.job.ComputeRegion = "other-region"
			case "inactive worker":
				claims.node.Active = false
			}
			calls := 0
			fake := &fakeVMM{jobBootFn: func(_ context.Context, req fcvm.JobBootRequest) (*fcvm.Instance, error) {
				calls++
				return &fcvm.Instance{Lease: fcvm.Lease{Instance: req.Instance}}, nil
			}}
			srv := vmmdgrpc.New(fake, nil, "1.0.0", nil).WithNodeID(nodeID).WithJobClaimStore(store)
			resp, err := srv.JobColdBoot(t.Context(), req)
			if status.Code(err) != want {
				t.Fatalf("code=%s want=%s err=%v", status.Code(err), want, err)
			}
			wantCalls := 0
			if want == codes.OK {
				wantCalls = 1
				if resp.GetNodeId() != nodeID {
					t.Fatalf("reported unverified execution host: %+v", resp)
				}
			}
			if calls != wantCalls {
				t.Fatalf("BootJob calls=%d want=%d", calls, wantCalls)
			}
		})
	}
}

func TestJobColdBoot_ClaimReadsFailClosed(t *testing.T) {
	for _, resource := range []string{"instance", "run", "task", "job", "node"} {
		for _, missing := range []bool{false, true} {
			t.Run(resource+"/"+map[bool]string{true: "missing", false: "unavailable"}[missing], func(t *testing.T) {
				req := jobPlacementRequest()
				claims := jobClaimsForRequest(req)
				claims.failedRead, claims.readError = resource, errors.New("database unavailable")
				want := codes.Unavailable
				if missing {
					claims.readError = state.ErrNotFound
					want = codes.FailedPrecondition
				}
				fake := &fakeVMM{jobBootFn: func(context.Context, fcvm.JobBootRequest) (*fcvm.Instance, error) {
					t.Fatal("booted without durable placement evidence")
					return nil, nil
				}}
				_, err := vmmdgrpc.New(fake, nil, "1.0.0", nil).WithNodeID(req.GetNodeId()).WithJobClaimStore(claims).JobColdBoot(t.Context(), req)
				if status.Code(err) != want {
					t.Fatalf("code=%s want=%s err=%v", status.Code(err), want, err)
				}
			})
		}
	}
}

func TestJobColdBoot_RealClaimSurvivesAdmissionPause(t *testing.T) {
	exerciseDurableJobClaim(t, state.NewMemStore())
}

func exerciseDurableJobClaim(t *testing.T, store interface {
	state.Store
	state.JobScheduleCreateStore
	state.JobImageMaterializationStore
}) {
	t.Helper()
	acct, err := store.CreateAccount(t.Context(), "job-receiver@example.com", "hobby")
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreateScheduledIfUnderQuota(t.Context(), state.Job{AccountID: acct.ID, Name: "receiver-job", Kind: "batch", ImageRef: "registry.example/job:v1", Command: []string{"/bin/true"}, RAMMB: 128, TaskTimeoutS: 30, MaxParallelism: 1, ComputeRegion: "local"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobSetImageMaterialization(t.Context(), job.ID, job.ImageRef, "ready", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "jobs/image.ext4", ""); err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(t.Context(), job.ID, acct.ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	req := jobPlacementRequest()
	req.Instance, req.AccountId, req.RunId, req.NodeId, req.LeaseToken = uuid.NewString(), acct.ID, run.ID, node.ID, uuid.NewString()
	if _, err := store.CreateAndClaimJobInstance(t.Context(), req.Instance, job.ID, run.ID, 0, "cold_booting", 128, node.ID, "", req.LeaseToken, time.Now().Add(time.Minute), node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "local", DisplayName: "Local", AcceptingNewApps: false}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fake := &fakeVMM{jobBootFn: func(_ context.Context, req fcvm.JobBootRequest) (*fcvm.Instance, error) {
		calls++
		return &fcvm.Instance{Lease: fcvm.Lease{Instance: req.Instance}}, nil
	}}
	cli, _ := newServer(t, fake, func(s *vmmdgrpc.Server) { s.WithNodeID(node.ID).WithJobClaimStore(store) })
	if _, err := cli.JobColdBoot(t.Context(), req); err != nil {
		t.Fatalf("existing admitted job refused after region pause: %v", err)
	}
	if err := store.JobTaskCancel(t.Context(), run.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.JobColdBoot(t.Context(), req); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale RPC after task cancellation: %v", err)
	}
	if calls != 1 {
		t.Fatalf("BootJob calls=%d want=1", calls)
	}
}
