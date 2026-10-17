// adr: 20261017201008
package fcvm

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
)

type commandWakePolicy struct {
	recorderWakePolicy
	spec                rehearsalcommand.Spec
	fenceErr, retireErr error
	fenced, retired     bool
	onRetire            func() error
}

func (p *commandWakePolicy) ScenarioRehearsalCommand(_ context.Context, id rehearsalrecorder.Identity) (rehearsalcommand.Spec, error) {
	return p.spec, nil
}
func (p *commandWakePolicy) FenceScenarioCommandNetwork(context.Context, rehearsalrecorder.Identity) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fenceErr != nil {
		return p.fenceErr
	}
	p.fenced = true
	return nil
}
func (p *commandWakePolicy) RetireScenarioCommandRecorder(_ context.Context, _ rehearsalrecorder.Identity, _ string, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.fenced || p.failed {
		return errors.New("command network or recorder incomplete")
	}
	if p.retireErr != nil {
		return p.retireErr
	}
	if err := p.onRetire(); err != nil {
		return err
	}
	p.retired = true
	return nil
}
func (p *commandWakePolicy) SettleScenarioRecorder(ctx context.Context, id rehearsalrecorder.Identity, session string, failed, ack bool) (string, error) {
	p.mu.Lock()
	retired := p.retired
	p.mu.Unlock()
	if retired {
		return "closed", nil
	}
	return p.recorderWakePolicy.SettleScenarioRecorder(ctx, id, session, failed, ack)
}

type commandResultVMM struct {
	*fakeVMM
	spec       rehearsalcommand.Spec
	badReceipt bool
	dials      int
}

func (v *commandResultVMM) DialAppTask(ctx context.Context, _ Lease) (*AppTaskSession, error) {
	v.mu.Lock()
	v.dials++
	v.mu.Unlock()
	host, guest := net.Pipe()
	go func() {
		defer guest.Close()
		_ = apptaskproto.Serve(ctx, guest, func(_ context.Context, req apptaskproto.Request, out, _ *apptaskproto.OutputWriter) (apptaskproto.Result, error) {
			if !v.badReceipt {
				raw, _ := (rehearsalcommand.Receipt{Version: 1, TaskID: req.TaskID, SpecSHA256: v.spec.SHA256()}).Marshal()
				_, _ = out.Write(raw)
			}
			exit := 0
			return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit}, nil
		})
	}()
	return NewAppTaskSession(host)
}

func TestContainedCommandRetirementRequiresPhysicalCleanupAndRecorderReceipt(t *testing.T) {
	for _, mode := range []string{"complete", "missing-receipt", "cleanup-failed", "revocation-failed", "recorder-gap", "intent-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			id := "22222222-2222-2222-2222-222222222222"
			spec := rehearsalcommand.Spec{Version: 1, RunID: strings.Repeat("a", 32), DeploymentID: "11111111-1111-1111-1111-111111111111", Command: []string{"true"}}
			v := &commandResultVMM{fakeVMM: &fakeVMM{}, spec: spec, badReceipt: mode == "missing-receipt"}
			p := &commandWakePolicy{spec: spec}
			if mode == "cleanup-failed" {
				v.killErr = errors.New("VM exit unconfirmed")
			}
			if mode == "revocation-failed" {
				p.fenceErr = errors.New("authority storage offline")
			}
			if mode == "recorder-gap" {
				p.retireErr = errors.New("pending request")
			}
			m := NewManager(&fakeRunner{}, v, Paths{}, "1.0.0", nil, nil).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
			lease, err := m.alloc.Acquire(id)
			if err != nil {
				t.Fatal(err)
			}
			inst := &Instance{Lease: lease, AppTaskOnly: true, AppID: "app", AccountID: "account", DeploymentID: spec.DeploymentID}
			inst.Net.RehearsalRunID = spec.RunID
			m.live[id] = inst
			owner, err := rehearsalrecorder.Start(t.Context(), t.Context(), p, rehearsalrecorder.Identity{AccountID: "account", AppID: "app", RunID: spec.RunID, InstanceID: id, DeploymentID: spec.DeploymentID})
			if err != nil {
				t.Fatal(err)
			}
			owner.MarkCommand()
			owner.Activate()
			m.rehearsalRecorders = map[string]*rehearsalrecorder.Session{id: owner}
			p.onRetire = func() error {
				if m.LiveCount() != 0 || m.LeasedCount() != 0 {
					return errors.New("retirement preceded physical cleanup")
				}
				v.mu.Lock()
				defer v.mu.Unlock()
				if len(v.killed) == 0 {
					return errors.New("VM was not killed")
				}
				return nil
			}
			dispatch := spec
			if mode == "intent-mismatch" {
				dispatch.Command = []string{"different-command"}
			}
			argv, _ := rehearsalcommand.Encode(dispatch)
			_, err = m.ExecuteAppTask(t.Context(), id, apptaskproto.Request{Version: apptaskproto.RehearsalVersion, TaskID: id, Command: argv, TimeoutSeconds: 5, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes})
			p.mu.Lock()
			retired := p.retired
			p.mu.Unlock()
			if (err == nil) != (mode == "complete") || retired != (mode == "complete") {
				t.Fatalf("unexpected retirement: retired=%v err=%v", retired, err)
			}
			if mode == "intent-mismatch" {
				v.mu.Lock()
				dials := v.dials
				v.mu.Unlock()
				if dials != 0 || m.LiveCount() != 0 {
					t.Fatal("different intent reached the guest or leaked its VM")
				}
			}
			if (mode == "cleanup-failed" || mode == "revocation-failed") && m.LeasedCount() != 1 {
				t.Fatal("uncertain cleanup released the address")
			}
			v.mu.Lock()
			v.killErr = nil
			v.mu.Unlock()
			p.mu.Lock()
			p.fenceErr = nil
			p.mu.Unlock()
			m.RetryPendingCleanups(t.Context())
			owner.Stop()
		})
	}
}

func TestContainedCommandOwnerLossBeforeActivationRetainsNetworkCleanupIdentity(t *testing.T) {
	id := "22222222-2222-2222-2222-222222222222"
	p := &commandWakePolicy{fenceErr: errors.New("revocation storage unavailable")}
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{}, "1.0.0", nil, nil).WithRehearsalNetworkPolicies(p)
	lease, err := m.alloc.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	identity := rehearsalrecorder.Identity{AccountID: "account", AppID: "app", RunID: strings.Repeat("a", 32), InstanceID: id, DeploymentID: "11111111-1111-1111-1111-111111111111"}
	lifecycle, cancel := context.WithCancel(t.Context())
	cancel()
	owner, err := rehearsalrecorder.Start(t.Context(), lifecycle, p, identity)
	if err != nil {
		t.Fatal(err)
	}
	<-owner.Done()
	owner.MarkCommand()
	m.rehearsalRecorders = map[string]*rehearsalrecorder.Session{id: owner}
	inst := &Instance{Lease: lease, AppTaskOnly: true}
	inst.Net.RehearsalRunID = identity.RunID
	m.live[id] = inst
	if err := m.Destroy(t.Context(), id); err == nil || m.LeasedCount() != 1 {
		t.Fatal("failed owner lost the command's address revocation obligation")
	}
	p.mu.Lock()
	p.fenceErr = nil
	p.mu.Unlock()
	completed, pending := m.RetryPendingCleanups(t.Context())
	if completed != 1 || pending != 0 {
		t.Fatal("command cleanup could not resume after storage recovered")
	}
}
