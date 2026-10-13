//go:build linux || darwin

// adr: 590
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
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

func TestNativeQualificationLaunchEvidencePinnedIncarnation(t *testing.T) {
	m, v, frame, _, ctx := nativeQualificationManagerFixture(t)
	j := v.nativeRecovery.journal.qualifications(frame.NodeID)
	claimed, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.owner.prepare(nativeQualificationContext(ctx, claimed), qualificationLease(frame.InstanceID)); err != nil {
		t.Fatal(err)
	}
	prepared, err := m.ObserveEnvironmentQualificationLaunch(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	physical, err := j.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	physical.Authorized, physical.PID, physical.StartTime = true, 42, 101
	if err = j.owner.write(physical); err != nil {
		t.Fatal(err)
	}
	if out, err := m.ObserveEnvironmentQualificationLaunchGeneration(ctx, frame, prepared.Native.Identity); !errors.Is(err, state.ErrConflict) || out != (NativeQualificationLaunchEvidence{}) {
		t.Fatal("prepared identity borrowed live incarnation", err)
	}
	live, err := m.ObserveEnvironmentQualificationLaunch(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := m.ObserveEnvironmentQualificationLaunchGeneration(ctx, frame, live.Native.Identity); err != nil || out != live {
		t.Fatal("exact incarnation rejected", err)
	}
	for _, change := range []func(*NativeLaunchIdentity){
		func(x *NativeLaunchIdentity) { x.InstanceID = uuid.NewString() },
		func(x *NativeLaunchIdentity) { x.Generation = uuid.NewString() },
		func(x *NativeLaunchIdentity) { x.KernelBootID = uuid.NewString() },
		func(x *NativeLaunchIdentity) { x.PID++ },
		func(x *NativeLaunchIdentity) { x.StartTime++ },
		func(x *NativeLaunchIdentity) { x.PID = 1 },
	} {
		expected := live.Native.Identity
		change(&expected)
		if out, err := m.ObserveEnvironmentQualificationLaunchGeneration(ctx, frame, expected); err == nil || out != (NativeQualificationLaunchEvidence{}) {
			t.Fatal("foreign incarnation accepted")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if out, err := m.ObserveEnvironmentQualificationLaunchGeneration(canceled, frame, live.Native.Identity); err == nil || out != (NativeQualificationLaunchEvidence{}) {
		t.Fatal("canceled observation accepted")
	}
	if _, err = j.revoke(ctx, frame); err != nil {
		t.Fatal(err)
	}
	out, err := m.ObserveEnvironmentQualificationLaunchGeneration(ctx, frame, live.Native.Identity)
	if err != nil || !out.RequestRevoked || !out.Native.Revoked || out.Native.ExitConfirmed || out.Native.ResourcesRemoved {
		t.Fatal("revocation changed incarnation or inferred closure", err)
	}
}
