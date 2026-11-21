//go:build linux

// adr: 20261114010000 — private template authority and native flight retirement.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func templateNativeJournalFixture(t *testing.T) (*JailerVMM, context.Context, string, string) {
	t.Helper()
	_, v, _ := nativeManagerFixture(t)
	v.nativeRecovery.journal.owners = true
	if err := v.nativeRecovery.acquireDaemonOwnership(t.Context()); err != nil {
		t.Fatal(err)
	}
	v.nativeRuntimeCapture = true
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := v.UseNativeRuntimeSnapshotRoot(root); err != nil {
		t.Fatal(err)
	}
	id, key := uuid.NewString(), state.SnapshotCaptureMemKey(uuid.Nil.String(), "", uuid.NewString())
	ctx, err := v.beginExecutionTemplateFlight(t.Context(), id, key)
	if err != nil {
		t.Fatal(err)
	}
	return v, ctx, id, key
}

func TestExecutionTemplateNativeAuthorityDoesNotWidenOrdinaryCapture(t *testing.T) {
	a := runtimeCaptureAuthorityFixture(t, state.SnapshotTierInit)
	a.InstanceID = uuid.NewString()
	a.NativeLease.Instance = a.InstanceID
	a.NativeLease.Netns = "fc-" + a.InstanceID
	a.NativeLease.Plan = api.PlanScale
	a.NativeLease.Networkless = true
	a.DeploymentID = uuid.Nil.String()
	a.Keys = nativeRuntimeCaptureKeys(state.SnapshotCaptureMemKey(a.DeploymentID, "", uuid.NewString()))
	if err := a.validate(); err == nil {
		t.Fatal("ordinary authority accepted reserved template keys")
	}
	a.Version = 2
	if err := a.validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*nativeRuntimeCaptureAuthority){
		"tenant namespace": func(a *nativeRuntimeCaptureAuthority) {
			a.DeploymentID = uuid.NewString()
			a.Keys = nativeRuntimeCaptureKeys(state.SnapshotCaptureMemKey(a.DeploymentID, "", uuid.NewString()))
		},
		"networked": func(a *nativeRuntimeCaptureAuthority) { a.NativeLease.Networkless = false },
		"uncoupled": func(a *nativeRuntimeCaptureAuthority) { a.Keys.VMState += "-other" },
		"warm prefix": func(a *nativeRuntimeCaptureAuthority) {
			a.Keys = nativeRuntimeCaptureKeys(state.SnapshotCaptureMemKey(uuid.Nil.String(), state.SnapshotTierWarm, uuid.NewString()))
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := a
			change(&bad)
			if bad.validate() == nil {
				t.Fatal("invalid template authority accepted")
			}
		})
	}
}

func TestExecutionTemplateNativeFlightFencesLaunchAndRequiresRetirement(t *testing.T) {
	v, ctx, id, key := templateNativeJournalFixture(t)
	j := v.nativeRecovery.journal
	lease := leaseForSlot(id, 3)
	lease.Plan = api.PlanScale
	lease.Networkless = true
	if err := j.prepare(t.Context(), lease); err == nil {
		t.Fatal("ordinary wake borrowed template reservation")
	}
	if err := j.prepare(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := v.finishExecutionTemplateFlight(ctx, id, key); err == nil {
		t.Fatal("live/unconfirmed owner lost its flight")
	}
	record, err := j.read(id)
	if err != nil {
		t.Fatal(err)
	}
	record.Revoked = true
	record.ExitConfirmed = true
	record.ResourcesRemoved = true
	if err := j.write(record); err != nil {
		t.Fatal(err)
	}
	if err := v.finishExecutionTemplateFlight(ctx, id, key); err != nil {
		t.Fatal(err)
	}
	if _, err := j.readExecutionTemplate(id); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retired flight remained", err)
	}
}

func TestExecutionTemplateNativeRecoveryRequiresDeadOriginalAndPreservesObjects(t *testing.T) {
	v, ctx, id, key := templateNativeJournalFixture(t)
	j := v.nativeRecovery.journal
	lease := leaseForSlot(id, 3)
	lease.Plan = api.PlanScale
	lease.Networkless = true
	if err := j.prepare(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := v.nativeRuntimeSnapshots.Put(t.Context(), key, strings.NewReader("possibly committed catalog object")); err != nil {
		t.Fatal(err)
	}
	alive := errors.New("old daemon alive")
	v.nativeRecovery.producerExited = func(context.Context, nativeProducerIdentity) error { return alive }
	called := 0
	cleanup := func(context.Context, Lease) error {
		called++
		record, err := j.read(id)
		if err != nil {
			return err
		}
		record.Revoked = true
		record.ExitConfirmed = true
		record.ResourcesRemoved = true
		return j.write(record)
	}
	if _, err := v.recoverExecutionTemplateFlights(t.Context(), cleanup); !errors.Is(err, alive) || called != 0 {
		t.Fatal("borrowed live owner", err)
	}
	v.nativeRecovery.producerExited = func(context.Context, nativeProducerIdentity) error { return nil }
	changed, err := v.recoverExecutionTemplateFlights(t.Context(), cleanup)
	if err != nil || !changed || called != 1 {
		t.Fatalf("recovery changed=%t calls=%d err=%v", changed, called, err)
	}
	file, err := v.nativeRuntimeSnapshots.Get(t.Context(), key)
	if err != nil {
		t.Fatal("recovery deleted uncertain catalog object", err)
	}
	_ = file.Close()
	if changed, err := v.recoverExecutionTemplateFlights(t.Context(), cleanup); err != nil || changed || called != 1 {
		t.Fatal("replayed recovered flight", err)
	}
	if _, err := j.readExecutionTemplate(id); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestExecutionTemplateNativeFlightRejectsCorruptionAndReplacement(t *testing.T) {
	v, ctx, id, key := templateNativeJournalFixture(t)
	j := v.nativeRecovery.journal
	path, err := j.executionTemplatePath(id)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := j.readExecutionTemplate(id); err == nil {
		t.Fatal("corrupt flight accepted")
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(path)+".original", path); err != nil {
		t.Fatal(err)
	}
	if err := v.finishExecutionTemplateFlight(ctx, id, key); err == nil {
		t.Fatal("linked flight accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".original", path); err != nil {
		t.Fatal(err)
	}
	if err := v.finishExecutionTemplateFlight(ctx, id, key); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionTemplateNativeModeRemainsOptIn(t *testing.T) {
	v, _, _, _ := templateNativeJournalFixture(t)
	key := state.SnapshotCaptureMemKey(uuid.Nil.String(), "", uuid.NewString())
	v.nativeRuntimeCapture = false
	if _, err := v.executionTemplateStorage(t.Context(), key); err == nil {
		t.Fatal("disabled native capture accepted")
	}
	v.nativeRuntimeCapture = true
	v.nativeRecovery.journal.owners = false
	if _, err := v.executionTemplateStorage(t.Context(), key); err == nil {
		t.Fatal("missing owner records accepted")
	}
	v.nativeRecovery.journal.owners = true
	t.Setenv("FAAS_STORAGE_BACKEND", "gcs")
	if _, err := v.executionTemplateStorage(t.Context(), key); err == nil {
		t.Fatal("unqualified remote capture accepted")
	}
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	if _, err := v.executionTemplateStorage(t.Context(), key); err != nil && !errors.Is(err, storage.ErrArtifactExists) {
		t.Fatal(err)
	}
}
