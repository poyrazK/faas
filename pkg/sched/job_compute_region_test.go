// adr: 468
package sched

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestJobComputeRegionPlacementAndOutage(t *testing.T) {
	for _, tc := range []struct {
		name, region, code string
		active             bool
		ceiling            int
	}{
		{name: "regional", region: "eu-jobs", active: true, ceiling: 1024},
		{name: "capacity", region: "eu-jobs", active: true, ceiling: 128, code: api.CodeComputeRegionCapacityExhausted},
		{name: "outage", region: "eu-jobs", ceiling: 1024, code: api.CodeComputeRegionUnavailable},
		{name: "legacy", ceiling: 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "regional-jobs@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			region := "eu-jobs"
			if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: region, DisplayName: "Jobs", AcceptingNewApps: true}); err != nil {
				t.Fatal(err)
			}
			node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "jobs-worker", TargetURL: "unix:///jobs-worker", Region: &region, Active: tc.active, AdmissionCeilingMB: tc.ceiling, VCPUBudget: 8, VPCPUs: 4})
			if err != nil {
				t.Fatal(err)
			}
			job, err := store.JobCreateScheduledIfUnderQuota(ctx, state.Job{AccountID: acct.ID, Name: "regional-jobs", Kind: "batch", ImageRef: "registry.example/jobs:v1", Command: []string{"/bin/true"}, RAMMB: 128, TaskTimeoutS: 60, MaxParallelism: 1, RetryMax: 1, ComputeRegion: tc.region}, 10)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", ""); err != nil {
				t.Fatal(err)
			}
			run, _, err := store.JobRunCreate(ctx, job.ID, acct.ID, "manual", nil, nil, nil, nil, 1)
			if err != nil {
				t.Fatal(err)
			}
			// Existing definitions keep dispatching when new admission is paused.
			if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: region, DisplayName: "Jobs", AcceptingNewApps: false}); err != nil {
				t.Fatal(err)
			}
			vmm := &recordingJobVMM{}
			local, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			if tc.region != "" {
				for _, target := range []string{local.ID, ""} {
					if _, err := store.CreateJobInstance(ctx, "invalid-instance", job.ID, run.ID, 0, "cold_booting", 128, target, ""); !errors.Is(err, state.ErrConflict) {
						t.Fatalf("direct store bypass target=%q: %v", target, err)
					}
				}
				if _, err := store.CreateAndClaimJobInstance(ctx, "invalid-claim", job.ID, run.ID, 0, "cold_booting", 128, local.ID, "", "lease", time.Now().Add(time.Minute), local.ID); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("atomic claim bypassed region: %v", err)
				}
				relabel := node
				replacement := "elsewhere"
				relabel.Region = &replacement
				if _, err := store.UpsertComputeNodeFromOperator(ctx, relabel); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("relabelled regional job peer: %v", err)
				}
			}
			engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOwnerNodeID(local.ID).
				WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
			result, err := engine.WakeJob(ctx, acct.ID, run.ID, 0)
			if tc.code != "" {
				if prob := api.AsProblem(err); prob == nil || prob.Code != tc.code || prob.ComputeRegion != region {
					t.Fatalf("wake=%+v %v", result, err)
				}
				task, _ := store.JobTaskGet(ctx, run.ID, 0)
				if vmm.calls != 0 || task.Status != "queued" || task.Attempt != 1 || task.InstanceID != nil {
					t.Fatalf("outage admitted or consumed retry: task=%+v calls=%d", task, vmm.calls)
				}
				return
			}
			if err != nil || vmm.calls != 1 {
				t.Fatalf("wake=%+v err=%v calls=%d", result, err, vmm.calls)
			}
			if tc.region != "" && (result.NodeID != node.ID || vmm.spec.NodeID != node.ID) {
				t.Fatalf("job escaped its region: %+v %+v", result, vmm.spec)
			}
			if tc.region == "" && result.NodeID == node.ID {
				t.Fatalf("legacy job selected inactive worker: %+v", result)
			}
			if tc.region == "" {
				return
			}
			// A queued retry cannot escape to the healthy local node after outage.
			if err := store.JobTaskCompleteClaimedWithLogs(ctx, run.ID, 0, result.InstanceID, string(result.LeaseToken), "failed", 1, "failed", "", "", false, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := store.JobRunRecompute(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			replay, _, err := store.JobRunReplayFailed(ctx, run.ID, acct.ID)
			if err != nil {
				t.Fatal(err)
			}
			replayWake, err := engine.WakeJob(ctx, acct.ID, replay.ID, 0)
			if err != nil || replayWake.NodeID != node.ID {
				t.Fatalf("replay escaped its job region: %+v %v", replayWake, err)
			}
			if err := store.JobTaskRetry(ctx, run.ID, 0, time.Now().Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := store.SetComputeNodeActive(ctx, node.ID, false); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.WakeJob(ctx, acct.ID, run.ID, 0); api.AsProblem(err) == nil || api.AsProblem(err).Code != api.CodeComputeRegionUnavailable {
				t.Fatalf("retry crossed region after outage: %v", err)
			}
			if vmm.calls != 2 {
				t.Fatalf("outage booted another VM: %d", vmm.calls)
			}
		})
	}
}
