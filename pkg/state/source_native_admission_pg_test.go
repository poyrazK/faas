//go:build !no_pg

package state

// adr: 435. Real PostgreSQL authority fences; receipts remain simulated.

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPgSourceNativeAdmission(t *testing.T) {
	for _, runtime := range []string{"", "node22"} {
		t.Run("runtime="+runtime, func(t *testing.T) { s, _ := registryVerificationPGStore(t); sourceNativeAdmission(t, s, runtime) })
	}
}
func TestPgSourceNativeMissingScan(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	sourceNativeMissingScan(t, s)
}
func TestPgSourceNativeCapabilityDowngrade(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	sourceNativeRefusesCapabilityDowngrade(t, s)
}

func TestPgSourceNativeProtocolSQLFence(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	_, _, b, capture := sourceNativeFixture(t, s, "node22", true)
	b.ProtocolVersion, b.ArtifactSourcesHash = 1, ""
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `SELECT application_standard_native_artifact_protocol($1::jsonb,NULL,$2::jsonb,1::smallint)`, raw, capture.inputs)
	if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatal("SQL admitted source producer without consumed-byte capability", err)
	}
}
func TestPgSourceNativeRenewal(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	sourceNativeRenewal(t, s)
}
func TestPgSourceNativeLateChange(t *testing.T) {
	for _, mode := range []string{"command", "base", "build", "metadata", "publisher"} {
		t.Run(mode, func(t *testing.T) { s, _ := registryVerificationPGStore(t); sourceNativeLateChange(t, s, mode) })
	}
}

func TestPgSourceNativeShortApprovalBoundsGrant(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	f, ins, b, before := sourceNativeFixture(t, s, "node22", true)
	short := insertShortSourceApproval(t, s, f.Parent.Input)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b)
	if err != nil || grant.ExpiresAtUnixNano != short.ExpiresAt.UnixNano() {
		t.Fatal("source grant outlived newest approval", err)
	}
	time.Sleep(time.Until(short.ExpiresAt) + 30*time.Millisecond)
	_, after := createRuntimeArtifactCapture(t, s, f.App, f.Dep)
	if before.ArtifactInputHash != after.ArtifactInputHash || !reflect.DeepEqual(before.RuntimeArtifacts, after.RuntimeArtifacts) {
		t.Fatal("expired approval changed historical source capture")
	}
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("expired newest proof fell back to older approval", err)
	}
	fresh := short.Input
	fresh.ID = uuid.NewString()
	if _, err := s.RecordBuildExportPublication(t.Context(), fresh); err != nil {
		t.Fatal(err)
	}
	// Initial grant identity is immutable; renewal starts a fresh instance.
	freshInstance, next := nativeArtifactAttempt(t, s, f.App, f.Dep)
	next = sourceNativeBinding(t, s, next, after)
	if renewed, err := s.IssueInstanceApplicationStandardBoot(t.Context(), freshInstance.State, next); err != nil || renewed.ExpiresAtUnixNano <= grant.ExpiresAtUnixNano {
		t.Fatal("fresh exact claim could not issue new source authority", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}
