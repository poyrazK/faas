//go:build linux || darwin

// adr: 590
package fcvm

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestNativeLaunchEvidenceOriginalIdentityAndRevocation(t *testing.T) {
	j, lease := preparedNativeJournal(t)
	v := &JailerVMM{nativeRecovery: &nativeProcessRecoveryRuntime{journal: j}}
	initial, err := v.ObserveNativeLaunch(t.Context(), lease.Instance)
	if err != nil || initial.Identity.InstanceID != lease.Instance || initial.Identity.Generation == "" || initial.Identity.KernelBootID == "" || initial.Identity.PID != 0 || initial.Authorized || initial.Revoked || initial.ExitConfirmed || initial.ResourcesRemoved {
		t.Fatal("prepared evidence changed", err)
	}
	original, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Identity.Generation != original.Generation || initial.Identity.KernelBootID != original.KernelBootID {
		t.Fatal("invented native incarnation")
	}
	if _, err = v.ObserveNativeLaunchGeneration(t.Context(), initial.Identity); err != nil {
		t.Fatal(err)
	}
	if _, err = j.revoke(t.Context(), lease.Instance); err != nil {
		t.Fatal(err)
	}
	revoked, err := v.ObserveNativeLaunchGeneration(t.Context(), initial.Identity)
	if err != nil || !revoked.Revoked || revoked.ExitConfirmed || revoked.ResourcesRemoved {
		t.Fatal("revocation inferred exit or resource removal", err)
	}
	// Observation must leave durable evidence byte-for-byte untouched.
	before, err := os.ReadFile(j.path(lease.Instance))
	if err != nil {
		t.Fatal(err)
	}
	revoked.Identity.Generation = uuid.NewString()
	replay, err := v.ObserveNativeLaunch(t.Context(), lease.Instance)
	if err != nil || replay.Identity != initial.Identity {
		t.Fatal("caller mutation changed journal", err)
	}
	after, err := os.ReadFile(j.path(lease.Instance))
	if err != nil || string(before) != string(after) {
		t.Fatal("observation mutated durable evidence", err)
	}
	for _, change := range []func(*NativeLaunchIdentity){
		func(x *NativeLaunchIdentity) { x.Generation = uuid.NewString() },
		func(x *NativeLaunchIdentity) { x.KernelBootID = uuid.NewString() },
		func(x *NativeLaunchIdentity) { x.PID = 42; x.StartTime = 123 },
		func(x *NativeLaunchIdentity) { x.Generation = "" },
		func(x *NativeLaunchIdentity) { x.PID = 1; x.StartTime = 1 },
		func(x *NativeLaunchIdentity) { x.InstanceID = "../escape" },
	} {
		expected := initial.Identity
		change(&expected)
		if evidence, err := v.ObserveNativeLaunchGeneration(t.Context(), expected); err == nil || evidence != (NativeLaunchEvidence{}) {
			t.Fatal("foreign or invalid incarnation accepted")
		}
	}
}

func TestNativeLaunchEvidenceMissingDamagedAndWrongBoot(t *testing.T) {
	j, lease := preparedNativeJournal(t)
	v := &JailerVMM{nativeRecovery: &nativeProcessRecoveryRuntime{journal: j}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if evidence, err := v.ObserveNativeLaunch(ctx, lease.Instance); err == nil || evidence != (NativeLaunchEvidence{}) {
		t.Fatal("canceled read exposed evidence")
	}
	for _, instance := range []string{"missing-instance", "../escape"} {
		if evidence, err := v.ObserveNativeLaunch(t.Context(), instance); err == nil || evidence != (NativeLaunchEvidence{}) {
			t.Fatal("absence accepted as closure")
		}
	}
	j.bootID = func() (string, error) { return uuid.NewString(), nil }
	if evidence, err := v.ObserveNativeLaunch(t.Context(), lease.Instance); err == nil || evidence != (NativeLaunchEvidence{}) {
		t.Fatal("prior boot accepted")
	}
	j.bootID = func() (string, error) { return "d3124d9a-657b-4f37-a9df-f5dfb2e50764", nil }
	if err := os.WriteFile(j.path(lease.Instance), []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if evidence, err := v.ObserveNativeLaunch(t.Context(), lease.Instance); err == nil || evidence != (NativeLaunchEvidence{}) {
		t.Fatal("damaged record accepted")
	}
	for _, missing := range []*JailerVMM{nil, {}, {nativeRecovery: &nativeProcessRecoveryRuntime{}}} {
		if evidence, err := missing.ObserveNativeLaunch(t.Context(), lease.Instance); err == nil || evidence != (NativeLaunchEvidence{}) {
			t.Fatal("unconfigured journal accepted")
		}
	}
}

func TestNativeLaunchEvidenceAuthorizationAndReplacement(t *testing.T) {
	j, lease := preparedNativeJournal(t)
	v := &JailerVMM{nativeRecovery: &nativeProcessRecoveryRuntime{journal: j}}
	prepared, err := v.ObserveNativeLaunch(t.Context(), lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	record, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic durable evidence exercises observation only; no process is started.
	record.Authorized = true
	record.PID = os.Getpid()
	record.StartTime = 1234
	if err = j.write(record); err != nil {
		t.Fatal(err)
	}
	if evidence, err := v.ObserveNativeLaunchGeneration(t.Context(), prepared.Identity); err == nil || evidence != (NativeLaunchEvidence{}) {
		t.Fatal("prepared identity borrowed authorized process")
	}
	authorized, err := v.ObserveNativeLaunch(t.Context(), lease.Instance)
	if err != nil || !authorized.Authorized || authorized.Identity.PID != record.PID || authorized.Identity.StartTime != record.StartTime || authorized.Identity.Generation != prepared.Identity.Generation || authorized.Revoked || authorized.ExitConfirmed || authorized.ResourcesRemoved {
		t.Fatal("authorized process evidence changed", err)
	}
	if evidence, err := v.ObserveNativeLaunchGeneration(t.Context(), authorized.Identity); err != nil || evidence != authorized {
		t.Fatal("exact incarnation rejected", err)
	}
	record.Generation = uuid.NewString()
	if err = j.write(record); err != nil {
		t.Fatal(err)
	}
	if evidence, err := v.ObserveNativeLaunchGeneration(t.Context(), authorized.Identity); err == nil || evidence != (NativeLaunchEvidence{}) {
		t.Fatal("replaced generation borrowed old process identity")
	}
}
