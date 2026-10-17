package fcvm

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"testing"
)

type coldWakePolicy struct {
	recorderWakePolicy
	required, fenced, completed bool
	preparation                 string
	fenceErr, bootErr           error
	methods                     []string
	onComplete                  func() error
}

func (p *coldWakePolicy) ScenarioColdBootRequired(context.Context, rehearsalrecorder.Identity) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.required, nil
}
func (p *coldWakePolicy) RecordScenarioColdBoot(_ context.Context, _ rehearsalrecorder.Identity, _, method string, _ int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.methods = append(p.methods, method)
	return p.bootErr
}
func (p *coldWakePolicy) FenceScenarioColdRetirement(_ context.Context, _ rehearsalrecorder.Identity, prep, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if prep != p.preparation {
		return errors.New("preparation differs")
	}
	if p.fenceErr != nil {
		return p.fenceErr
	}
	p.fenced = true
	return nil
}
func (p *coldWakePolicy) CompleteScenarioColdRetirement(_ context.Context, _ rehearsalrecorder.Identity, prep, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.fenced || p.failed || prep != p.preparation {
		return errors.New("original collector lost")
	}
	if err := p.onComplete(); err != nil {
		return err
	}
	p.completed = true
	return nil
}
func (p *coldWakePolicy) FailScenarioColdPreparation(context.Context, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = true
	return nil
}

func TestColdStartRefusesSnapshotBeforeBootAndRecordsActualMethod(t *testing.T) {
	for _, mode := range []string{"cold", "snapshot", "paused", "receipt_failed"} {
		t.Run(mode, func(t *testing.T) {
			p := &coldWakePolicy{required: true}
			if mode == "receipt_failed" {
				p.bootErr = errors.New("storage unavailable")
			}
			runner, vmm := &rehearsalInputRunner{}, &fakeVMM{}
			m := newTestManager(runner, vmm).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
			req := WakeRequest{Instance: uuid.NewString(), AppID: uuid.NewString(), AccountID: uuid.NewString(), DeploymentID: uuid.NewString(), BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby}
			if mode == "snapshot" {
				req.Snapshot = usableSnapshot()
			}
			if mode == "paused" {
				req.KeepPaused = true
			}
			_, err := m.Wake(t.Context(), req)
			if mode == "cold" {
				if err != nil {
					t.Fatal(err)
				}
				if len(p.methods) != 1 || p.methods[0] != "cold_boot" {
					t.Fatalf("actual method missing: %v", p.methods)
				}
				if err = m.Destroy(t.Context(), req.Instance); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unsupported or unrecorded cold boot succeeded")
			}
			if mode == "snapshot" || mode == "paused" {
				if vmm.bootCount != 0 || len(vmm.restored) != 0 || len(runner.commands) != 0 || m.LeasedCount() != 0 {
					t.Fatal("cold-start refusal reached guest effects")
				}
			}
			if mode == "receipt_failed" && (m.LiveCount() != 0 || m.LeasedCount() != 0) {
				t.Fatal("unrecorded boot was published or leaked resources")
			}
		})
	}
}

func TestColdStartRetirementRequiresOriginalOwnerAndPhysicalCleanup(t *testing.T) {
	for _, mode := range []string{"complete", "unsettled_effect", "cleanup_failed", "wrong_preparation", "ordinary_destroy", "lost_owner"} {
		t.Run(mode, func(t *testing.T) {
			p := &coldWakePolicy{preparation: uuid.NewString()}
			vmm := &fakeVMM{}
			if mode == "unsettled_effect" {
				p.fenceErr = errors.New("unsettled effect")
			}
			if mode == "cleanup_failed" {
				vmm.killErr = errors.New("guest exit unconfirmed")
			}
			m := NewManager(&fakeRunner{}, vmm, Paths{}, "1.0.0", nil, nil).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
			id := uuid.NewString()
			lease, err := m.alloc.Acquire(id)
			if err != nil {
				t.Fatal(err)
			}
			inst := &Instance{Lease: lease, AppID: uuid.NewString(), AccountID: uuid.NewString(), DeploymentID: uuid.NewString()}
			inst.Net.RehearsalRunID = rehearsalNetworkTestRun
			m.live[id] = inst
			owner, err := rehearsalrecorder.Start(t.Context(), t.Context(), p, rehearsalrecorder.Identity{AppID: inst.AppID, AccountID: inst.AccountID, DeploymentID: inst.DeploymentID, RunID: inst.Net.RehearsalRunID, InstanceID: id})
			if err != nil {
				t.Fatal(err)
			}
			owner.Activate()
			m.rehearsalRecorders = map[string]*rehearsalrecorder.Session{id: owner}
			joined := false
			var connection *rehearsalrecorder.Connection
			connection, err = owner.Attach(func() { joined = true; connection.Detach(false) })
			if err != nil {
				t.Fatal(err)
			}
			p.onComplete = func() error {
				if !joined || m.LiveCount() != 0 || m.LeasedCount() != 0 {
					return errors.New("receipt preceded joined collectors and physical teardown")
				}
				return nil
			}
			if mode == "ordinary_destroy" {
				if err = m.Destroy(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "lost_owner" {
				delete(m.rehearsalRecorders, id)
			}
			prep := p.preparation
			if mode == "wrong_preparation" {
				prep = uuid.NewString()
			}
			err = m.RetireRehearsalCold(t.Context(), id, prep)
			p.mu.Lock()
			completed := p.completed
			p.mu.Unlock()
			if mode == "complete" {
				if err != nil || !completed {
					t.Fatalf("retirement failed: %v", err)
				}
			} else if err == nil || completed {
				t.Fatal("incomplete cleanup or lost owner acknowledged retirement")
			}
			owner.Stop()
		})
	}
}
