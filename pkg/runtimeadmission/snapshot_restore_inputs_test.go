package runtimeadmission

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func snapshotRestoreInputFixture(t *testing.T) (SnapshotCapture, Binding, []ArtifactSource) {
	t.Helper()
	capture := snapshotCaptureFixture(t)
	binding := capture.Parent.Binding
	binding.Token, binding.InstanceID = uuid.NewString(), uuid.NewString()
	binding.NodeID, binding.Incarnation = uuid.NewString(), uuid.NewString()
	binding.CapturedInputHash = strings.Repeat("9", 64)
	now := time.Now()
	binding.IssuedAtUnixNano, binding.ExpiresAtUnixNano = now.UnixNano(), now.Add(time.Minute).UnixNano()
	sources := []ArtifactSource{}
	for _, drive := range capture.Parent.ArtifactConsumption.Drives {
		sources = append(sources, drive.Source)
	}
	return capture, binding, sources
}

func TestSnapshotRestoreInputsNeedFreshAuthorityAndPermitHistoricalCrossNodeLineage(t *testing.T) {
	capture, binding, sources := snapshotRestoreInputFixture(t)
	capture.Parent.Binding.IssuedAtUnixNano -= int64(2 * time.Hour)
	capture.Parent.Binding.ExpiresAtUnixNano -= int64(2 * time.Hour)
	capture.Parent.CompletedAtUnixNano -= int64(2 * time.Hour)
	capture.CapturedAtUnixNano -= int64(2 * time.Hour)
	slices.Reverse(sources)
	if err := capture.CheckRestoreInputs(binding, sources, capture.Memory.StorageKey, capture.VMState.StorageKey, capture.Memory.Bytes, time.Now()); err != nil {
		t.Fatal("fresh target-node authority rejected historical lineage", err)
	}
	if capture.Parent.Binding.Validate(time.Now()) == nil {
		t.Fatal("historical capture renewed its parent's authority")
	}
	binding.ExpiresAtUnixNano = time.Now().Add(-time.Second).UnixNano()
	binding.IssuedAtUnixNano = binding.ExpiresAtUnixNano - int64(time.Minute)
	if err := capture.CheckRestoreInputs(binding, sources, capture.Memory.StorageKey, capture.VMState.StorageKey, capture.Memory.Bytes, time.Now()); !errors.Is(err, ErrExpired) {
		t.Fatal("historical lineage bypassed fresh grant expiry", err)
	}
}

func TestSnapshotRestoreInputsRejectChangedScopePolicySourceAndCache(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*SnapshotCapture, *Binding, []ArtifactSource)
	}{
		{"old token", func(c *SnapshotCapture, b *Binding, _ []ArtifactSource) { b.Token = c.Parent.Binding.Token }},
		{"old instance", func(c *SnapshotCapture, b *Binding, _ []ArtifactSource) { b.InstanceID = c.Parent.Binding.InstanceID }},
		{"account", func(_ *SnapshotCapture, b *Binding, _ []ArtifactSource) { b.AccountID = uuid.NewString() }},
		{"application", func(_ *SnapshotCapture, b *Binding, _ []ArtifactSource) { b.AppID = uuid.NewString() }},
		{"deployment", func(_ *SnapshotCapture, b *Binding, _ []ArtifactSource) { b.DeploymentID = uuid.NewString() }},
		{"standard revision", func(_ *SnapshotCapture, b *Binding, _ []ArtifactSource) { b.DesiredRevision++ }},
		{"effective policy", func(_ *SnapshotCapture, b *Binding, _ []ArtifactSource) { b.EffectiveHash = strings.Repeat("0", 64) }},
		{"egress revision", func(_ *SnapshotCapture, b *Binding, _ []ArtifactSource) { b.EgressRevision++ }},
		{"source changed", func(_ *SnapshotCapture, b *Binding, s []ArtifactSource) {
			s[0].Digest = "sha256:" + strings.Repeat("0", 64)
			b.ArtifactSourcesHash, _ = HashArtifactSources(s)
		}},
		{"source omitted", func(_ *SnapshotCapture, _ *Binding, s []ArtifactSource) { s[0] = s[1] }},
		{"memory from another capture", func(c *SnapshotCapture, _ *Binding, _ []ArtifactSource) { c.Memory.StorageKey = "snap/other/mem" }},
		{"legacy parent", func(c *SnapshotCapture, _ *Binding, _ []ArtifactSource) {
			c.Parent.Binding.ProtocolVersion = ProtocolVersion
		}},
		{"before capture", func(c *SnapshotCapture, b *Binding, _ []ArtifactSource) {
			b.IssuedAtUnixNano = c.CapturedAtUnixNano - int64(2*api.ApplicationStandardRuntimeAdmissionClockSkew)
			b.ExpiresAtUnixNano = b.IssuedAtUnixNano + int64(api.ApplicationStandardRuntimeAdmissionTTL)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture, binding, sources := snapshotRestoreInputFixture(t)
			memoryKey, vmstateKey := capture.Memory.StorageKey, capture.VMState.StorageKey
			tc.edit(&capture, &binding, sources)
			if capture.CheckRestoreInputs(binding, sources, memoryKey, vmstateKey, capture.Memory.Bytes, time.Now()) == nil {
				t.Fatal("changed inputs accepted historical capture")
			}
		})
	}
	for _, tc := range []struct {
		name, memory, vmstate string
		bytes                 int64
	}{
		{"wrong memory", "other", "", 0}, {"wrong state", "", "other", 0}, {"wrong RAM", "", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, b, s := snapshotRestoreInputFixture(t)
			memory, vmstate := c.Memory.StorageKey, c.VMState.StorageKey
			if tc.memory != "" {
				memory = tc.memory
			}
			if tc.vmstate != "" {
				vmstate = tc.vmstate
			}
			if c.CheckRestoreInputs(b, s, memory, vmstate, c.Memory.Bytes+tc.bytes, time.Now()) == nil {
				t.Fatal("wrong selected cache accepted")
			}
		})
	}
}
