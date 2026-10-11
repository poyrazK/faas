//go:build linux || darwin

// adr: 590
package fcvm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNativeQualificationLaunchEvidenceOriginalBinding(t *testing.T) {
	j, frame, ctx := nativeQualificationFixture(t)
	claimed, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := j.observeLaunchBinding(ctx, frame); err == nil || out != (NativeQualificationLaunchEvidence{}) {
		t.Fatal("unbound request accepted")
	}
	if err = j.owner.prepare(nativeQualificationContext(ctx, claimed), qualificationLease(frame.InstanceID)); err != nil {
		t.Fatal(err)
	}
	before, err := j.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := j.observeLaunchBinding(ctx, frame)
	if err != nil || observed.RequestID != frame.RequestID || observed.AppID != frame.AppID || observed.DeploymentID != frame.DeploymentID || observed.NodeID != frame.NodeID || observed.WakeID != frame.WakeID || observed.IncomingGeneration != claimed.Generation || observed.Native.Identity.Generation != before.NativeGeneration || observed.Native.Identity.KernelBootID != before.KernelBootID || observed.RequestRevoked || observed.Native.Authorized {
		t.Fatal("original binding changed", err)
	}
	raw, err := json.Marshal(observed)
	if err != nil || strings.Contains(string(raw), frame.CleanupToken) || strings.Contains(string(raw), frame.Artifact.RootfsKey) {
		t.Fatal("private execution capabilities exposed", err)
	}
	for _, change := range []func(*state.EnvironmentQualificationExecution){
		func(x *state.EnvironmentQualificationExecution) { x.AppID = uuid.NewString() },
		func(x *state.EnvironmentQualificationExecution) { x.DeploymentID = uuid.NewString() },
		func(x *state.EnvironmentQualificationExecution) { x.WakeID = uuid.NewString() },
		func(x *state.EnvironmentQualificationExecution) { x.CleanupToken = uuid.NewString() },
		func(x *state.EnvironmentQualificationExecution) { x.Attempt++ },
		func(x *state.EnvironmentQualificationExecution) { x.NodeID = uuid.NewString() },
	} {
		foreign := frame
		change(&foreign)
		if out, err := j.observeLaunchBinding(ctx, foreign); err == nil || out != (NativeQualificationLaunchEvidence{}) {
			t.Fatal("foreign request borrowed physical generation")
		}
	}
	if _, err = j.revoke(ctx, frame); err != nil {
		t.Fatal(err)
	}
	revoked, err := j.observeLaunchBinding(ctx, frame)
	if err != nil || !revoked.RequestRevoked || revoked.Native.Identity != observed.Native.Identity || revoked.Native.ExitConfirmed || revoked.Native.ResourcesRemoved {
		t.Fatal("revocation inferred closure", err)
	}
	restarted := j.owner.qualifications(frame.NodeID)
	if replay, err := restarted.observeLaunchBinding(ctx, frame); err != nil || replay != revoked {
		t.Fatal("daemon restart lost binding", err)
	}
	physical, err := j.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	physical.Generation = uuid.NewString()
	if err = j.owner.write(physical); err != nil {
		t.Fatal(err)
	}
	if out, err := j.observeLaunchBinding(ctx, frame); err == nil || out != (NativeQualificationLaunchEvidence{}) {
		t.Fatal("replacement generation borrowed original request")
	}
}

func TestNativeQualificationLaunchEvidenceManagerNode(t *testing.T) {
	m, v, frame, _, ctx := nativeQualificationManagerFixture(t)
	j := v.nativeRecovery.journal.qualifications(frame.NodeID)
	claimed, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.owner.prepare(nativeQualificationContext(ctx, claimed), qualificationLease(frame.InstanceID)); err != nil {
		t.Fatal(err)
	}
	if out, err := m.ObserveEnvironmentQualificationLaunch(ctx, frame); err != nil || out.RequestID != frame.RequestID {
		t.Fatal("configured manager lost original binding", err)
	}
	for _, node := range []string{"", uuid.NewString()} {
		m.WithNativeQualificationNodeID(node)
		if out, err := m.ObserveEnvironmentQualificationLaunch(ctx, frame); err == nil || out != (NativeQualificationLaunchEvidence{}) {
			t.Fatal("foreign or unconfigured node accepted")
		}
	}
	m.WithNativeQualificationNodeID(frame.NodeID)
	physical, err := j.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	physical.Lease.MemoryMaxMiB++
	if err = j.owner.write(physical); err != nil {
		t.Fatal(err)
	}
	if out, err := m.ObserveEnvironmentQualificationLaunch(ctx, frame); err == nil || out != (NativeQualificationLaunchEvidence{}) {
		t.Fatal("changed physical lease accepted")
	}
}
