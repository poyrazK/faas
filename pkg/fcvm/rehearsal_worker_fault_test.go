package fcvm

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
)

type workerFaultWakePolicy struct {
	recorderWakePolicy
	ready                 chan struct{}
	fenced, completed     bool
	fenceErr, completeErr error
	onComplete            func() error
}

func (p *workerFaultWakePolicy) ReadyScenarioWorkerCheckpoint(context.Context, rehearsalrecorder.Identity, string, string) error {
	close(p.ready)
	return nil
}
func (p *workerFaultWakePolicy) FenceScenarioWorkerTermination(context.Context, rehearsalrecorder.Identity, string, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fenceErr != nil {
		return p.fenceErr
	}
	p.fenced = true
	return nil
}
func (p *workerFaultWakePolicy) CompleteScenarioWorkerTermination(context.Context, rehearsalrecorder.Identity, string, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.fenced || p.failed {
		return errors.New("collector continuity lost")
	}
	if p.completeErr != nil {
		return p.completeErr
	}
	if err := p.onComplete(); err != nil {
		return err
	}
	p.completed = true
	return nil
}
func (p *workerFaultWakePolicy) FailScenarioWorkerFault(context.Context, string) error {
	p.mu.Lock()
	p.failed = true
	p.mu.Unlock()
	return nil
}

func TestWorkerTerminationRequiresOriginalCheckpointCollectorAndPhysicalCleanup(t *testing.T) {
	for _, mode := range []string{"complete", "missing_checkpoint", "wrong_checkpoint", "cleanup_failed", "fence_failed", "receipt_failed", "ordinary_destroy", "other_teardown"} {
		t.Run(mode, func(t *testing.T) {
			p := &workerFaultWakePolicy{ready: make(chan struct{})}
			v := &fakeVMM{}
			if mode == "cleanup_failed" {
				v.killErr = errors.New("VM exit unconfirmed")
			}
			if mode == "fence_failed" {
				p.fenceErr = errors.New("unsettled request")
			}
			if mode == "receipt_failed" {
				p.completeErr = errors.New("receipt storage unavailable")
			}
			m := NewManager(&fakeRunner{}, v, Paths{}, "1.0.0", nil, nil).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
			instance := "22222222-2222-4222-8222-222222222222"
			fault := "33333333-3333-4333-8333-333333333333"
			lease, err := m.alloc.Acquire(instance)
			if err != nil {
				t.Fatal(err)
			}
			inst := &Instance{Lease: lease, AppID: "app", AccountID: "account", DeploymentID: "deployment"}
			inst.Net.RehearsalRunID = rehearsalNetworkTestRun
			m.live[instance] = inst
			owner, err := rehearsalrecorder.Start(t.Context(), t.Context(), p, rehearsalrecorder.Identity{AccountID: inst.AccountID, AppID: inst.AppID, DeploymentID: inst.DeploymentID, RunID: inst.Net.RehearsalRunID, InstanceID: instance})
			if err != nil {
				t.Fatal(err)
			}
			owner.Activate()
			m.rehearsalRecorders = map[string]*rehearsalrecorder.Session{instance: owner}
			checkpointCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			joined := make(chan struct{})
			if mode != "missing_checkpoint" {
				var connection *rehearsalrecorder.Connection
				connection, err = owner.Attach(func() { <-joined; connection.Detach(false) })
				if err != nil {
					t.Fatal(err)
				}
				go func() { _ = owner.HoldWorkerCheckpoint(checkpointCtx, fault); close(joined) }()
				<-p.ready
			}
			p.onComplete = func() error {
				if m.LiveCount() != 0 || m.LeasedCount() != 0 {
					return errors.New("receipt preceded physical cleanup")
				}
				v.mu.Lock()
				defer v.mu.Unlock()
				if len(v.killed) == 0 {
					return errors.New("VM was not killed")
				}
				return nil
			}
			requested := fault
			if mode == "wrong_checkpoint" {
				requested = "44444444-4444-4444-8444-444444444444"
			}
			if mode == "ordinary_destroy" {
				if err = m.Destroy(t.Context(), instance); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "other_teardown" {
				entered, release := make(chan struct{}), make(chan struct{})
				v.destroyWithExportHook = func() { close(entered); <-release }
				destroyed := make(chan error, 1)
				go func() { destroyed <- m.Destroy(t.Context(), instance) }()
				<-entered
				terminated := make(chan error, 1)
				go func() { terminated <- m.TerminateRehearsalWorker(t.Context(), instance, requested) }()
				close(release)
				if destroyErr := <-destroyed; destroyErr != nil {
					t.Fatal(destroyErr)
				}
				err = <-terminated
				v.destroyWithExportHook = nil
			} else {
				err = m.TerminateRehearsalWorker(t.Context(), instance, requested)
			}
			cancel()
			owner.Stop()
			p.mu.Lock()
			completed := p.completed
			p.mu.Unlock()
			if (err == nil) != (mode == "complete") || completed != (mode == "complete") {
				t.Fatalf("%s: completed=%v err=%v", mode, completed, err)
			}
			if mode == "cleanup_failed" && m.LeasedCount() != 1 {
				t.Fatal("uncertain cleanup released VM identity")
			}
			v.mu.Lock()
			v.killErr = nil
			v.mu.Unlock()
			m.RetryPendingCleanups(t.Context())
			_ = m.Destroy(context.WithoutCancel(t.Context()), instance)
		})
	}
}
