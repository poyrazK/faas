// adr: 20261017201015
package fcvm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
)

func TestRunningSuspendPermitsOnlyHeldOriginalDispatch(t *testing.T) {
	for _, mode := range []string{"complete", "other_forward", "wrong_dispatch", "no_activity_owner", "disconnect_during_pause", "forward_lost_during_pause", "held_connection_lost_during_pause"} {
		t.Run(mode, func(t *testing.T) {
			p := &suspendWakePolicy{receipt: uuid.NewString(), state: "waiting", pauseMS: 20, paused: make(chan struct{}), running: true, invocation: uuid.NewString()}
			v := &suspendReadyVMM{freezingVMM: &freezingVMM{fakeVMM: &fakeVMM{}}}
			tracker := activity.New(nil)
			m := newTestManager(&rehearsalInputRunner{}, v).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p).WithFreeze(FreezeConfig{Activity: tracker})
			req := WakeRequest{Instance: uuid.NewString(), AppID: uuid.NewString(), AccountID: uuid.NewString(), DeploymentID: uuid.NewString(), BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby}
			if _, err := m.Wake(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Destroy(context.WithoutCancel(t.Context()), req.Instance) })
			owner, err := m.InstanceRehearsalRecorder(req.Instance)
			if err != nil {
				t.Fatal(err)
			}
			var held *rehearsalrecorder.Connection
			held, err = owner.Attach(func() { held.Detach(false) })
			if err != nil {
				t.Fatal(err)
			}
			defer held.Detach(false)
			holdCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			holdDone := make(chan error, 1)
			go func() { holdDone <- owner.HoldSuspendCheckpoint(holdCtx, p.receipt, held) }()
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				p.mu.Lock()
				ready := p.state == "armed"
				p.mu.Unlock()
				if ready {
					break
				}
				select {
				case <-deadline.C:
					t.Fatal("checkpoint not held")
				case <-ticker.C:
				}
			}
			tracker.BeginDispatch(req.Instance, p.invocation, 1)
			// The ordinary freeze path must retain its existing busy guard.
			if _, err = m.Freeze(t.Context(), req.Instance, FreezeOptions{}); !errors.Is(err, ErrInstanceBusy) {
				t.Fatal("ordinary freeze paused an active job")
			}
			if mode == "other_forward" {
				tracker.Begin(req.Instance)
			}
			if mode == "wrong_dispatch" {
				tracker.EndDispatch(req.Instance, p.invocation, 1)
				tracker.BeginDispatch(req.Instance, uuid.NewString(), 1)
			}
			if mode == "no_activity_owner" {
				m.freezeActivity = nil
			}
			p.mu.Lock()
			p.state = "claimed"
			p.mu.Unlock()
			result := make(chan error, 1)
			go func() { result <- m.RehearseSuspendResume(t.Context(), req.Instance, p.receipt) }()
			if mode == "disconnect_during_pause" || mode == "forward_lost_during_pause" || mode == "held_connection_lost_during_pause" {
				select {
				case <-p.paused:
					switch mode {
					case "disconnect_during_pause":
						cancel()
					case "forward_lost_during_pause":
						tracker.EndDispatch(req.Instance, p.invocation, 1)
					case "held_connection_lost_during_pause":
						held.Detach(false)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("pause not observed")
				}
			}
			err = <-result
			if (err == nil) != (mode == "complete") {
				t.Fatalf("%s: %v", mode, err)
			}
			if mode == "complete" {
				if !owner.HeldSuspendCheckpoint(p.receipt) || len(v.resumes()) != 1 {
					t.Fatal("original response did not survive physical resume")
				}
				select {
				case <-holdDone:
					t.Fatal("guest released before scheduler publication")
				default:
				}
				p.mu.Lock()
				p.state = "complete"
				p.mu.Unlock()
				select {
				case err = <-holdDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("original response did not release")
				}
			} else {
				cancel()
				select {
				case <-holdDone:
				case <-time.After(3 * time.Second):
					t.Fatal("failed checkpoint did not join")
				}
			}
		})
	}
}
