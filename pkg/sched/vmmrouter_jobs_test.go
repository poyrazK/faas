package sched

import (
	"context"
	"crypto/tls"
	"testing"
)

type jobVMMWithoutHeldRelease struct {
	*fakeRouterVMM
	coldBootCalls int
}

func (v *jobVMMWithoutHeldRelease) JobColdBoot(_ context.Context, spec JobVmmSpec) (JobVmmResult, error) {
	v.coldBootCalls++
	return JobVmmResult{InstanceID: spec.InstanceID, NodeID: spec.NodeID, StartHeld: spec.StartHeld}, nil
}

func (v *jobVMMWithoutHeldRelease) WaitJobExit(context.Context, JobExitSpec) (JobExitResult, error) {
	return JobExitResult{}, nil
}

func TestVMMRouterRejectsHeldJobBootWhenNodeCannotRelease(t *testing.T) {
	client := &jobVMMWithoutHeldRelease{fakeRouterVMM: &fakeRouterVMM{}}
	router := NewVMMRouter([]ComputeNodeInfo{{ID: "node-a", TargetURL: "unix:///node-a.sock"}},
		func(context.Context, string, *tls.Config) (VMM, error) { return client, nil }, nil)

	_, err := router.JobColdBoot(context.Background(), JobVmmSpec{NodeID: "node-a", InstanceID: "held-job", StartHeld: true})
	if err == nil || client.coldBootCalls != 0 {
		t.Fatalf("held Job boot without downstream release support: err=%v coldBootCalls=%d", err, client.coldBootCalls)
	}

	if _, err := router.JobColdBoot(context.Background(), JobVmmSpec{NodeID: "node-a", InstanceID: "ordinary-job"}); err != nil || client.coldBootCalls != 1 {
		t.Fatalf("ordinary Job boot should retain compatibility: err=%v coldBootCalls=%d", err, client.coldBootCalls)
	}
}
