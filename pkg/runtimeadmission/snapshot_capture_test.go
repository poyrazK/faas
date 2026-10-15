package runtimeadmission

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

func snapshotCaptureFixture(t *testing.T) SnapshotCapture {
	t.Helper()
	parent := consumedReceiptFixture(t)
	prefix := "snap/" + parent.Binding.DeploymentID + "/captures/" + uuid.NewString() + "/v2/"
	return SnapshotCapture{Version: SnapshotCaptureVersion, Parent: parent, Memory: CapturedArtifact{StorageKey: prefix + "mem", Digest: "sha256:" + strings.Repeat("1", 64), Bytes: 16384}, VMState: CapturedArtifact{StorageKey: prefix + "vmstate", Digest: "sha256:" + strings.Repeat("2", 64), Bytes: 4096}, PrivateDrive: CapturedArtifact{StorageKey: prefix + "drive", Digest: "sha256:" + strings.Repeat("3", 64), Bytes: 8192}, CapturedAtUnixNano: time.Now().UnixNano()}
}

func TestSnapshotCaptureRetainsHistoricalBootWithoutRenewingAuthority(t *testing.T) {
	c := snapshotCaptureFixture(t)
	if err := c.Check(time.Now().Add(24 * time.Hour)); err != nil {
		t.Fatal("historical lineage wrongly expired with the boot grant", err)
	}
	if c.Parent.Binding.Validate(time.Now().Add(24*time.Hour)) == nil {
		t.Fatal("capture renewed an expired boot grant")
	}
	copy := c.Clone()
	copy.Parent.ArtifactConsumption.Drives[0].Source.StorageKey = "mutated"
	if c.Equal(copy) || c.Parent.ArtifactConsumption.Drives[0].Source.StorageKey == "mutated" {
		t.Fatal("capture parent was aliased")
	}
}

func TestSnapshotCaptureRejectsIncompleteOrCrossCaptureFacts(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*SnapshotCapture)
	}{
		{"version", func(c *SnapshotCapture) { c.Version++ }},
		{"legacy parent", func(c *SnapshotCapture) { c.Parent.Binding.ProtocolVersion = ProtocolVersion }},
		{"unmeasured parent", func(c *SnapshotCapture) { c.Parent.ArtifactConsumption = ArtifactConsumption{} }},
		{"before boot", func(c *SnapshotCapture) { c.CapturedAtUnixNano = c.Parent.CompletedAtUnixNano - 1 }},
		{"future", func(c *SnapshotCapture) { c.CapturedAtUnixNano = time.Now().Add(time.Hour).UnixNano() }},
		{"empty memory", func(c *SnapshotCapture) { c.Memory.Bytes = 0 }},
		{"oversized memory", func(c *SnapshotCapture) { c.Memory.Bytes = api.ApplicationStandardSnapshotMaxArtifactBytes + 1 }},
		{"missing state digest", func(c *SnapshotCapture) { c.VMState.Digest = "" }},
		{"uppercase digest", func(c *SnapshotCapture) { c.PrivateDrive.Digest = "sha256:" + strings.Repeat("A", 64) }},
		{"wrong private size", func(c *SnapshotCapture) { c.PrivateDrive.Bytes++ }},
		{"state from another capture", func(c *SnapshotCapture) {
			c.VMState.StorageKey = strings.ReplaceAll(c.VMState.StorageKey, "/captures/", "/warm/captures/")
		}},
		{"legacy pair", func(c *SnapshotCapture) { c.Memory.StorageKey = "snap/" + c.Parent.Binding.DeploymentID + "/mem" }},
		{"caller capture id", func(c *SnapshotCapture) {
			parts := strings.Split(c.Memory.StorageKey, "/")
			parts[3] = "arbitrary"
			c.Memory.StorageKey = strings.Join(parts, "/")
		}},
		{"different deployment", func(c *SnapshotCapture) {
			c.Memory.StorageKey = strings.ReplaceAll(c.Memory.StorageKey, c.Parent.Binding.DeploymentID, uuid.NewString())
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := snapshotCaptureFixture(t)
			test.edit(&c)
			if c.Check(time.Now()) == nil {
				t.Fatal("invalid lineage accepted")
			}
		})
	}
}

func TestSnapshotCaptureWarmAndCompactDeploymentKeys(t *testing.T) {
	for _, compact := range []bool{false, true} {
		c := snapshotCaptureFixture(t)
		for _, artifact := range []*CapturedArtifact{&c.Memory, &c.VMState, &c.PrivateDrive} {
			artifact.StorageKey = strings.ReplaceAll(artifact.StorageKey, "/captures/", "/warm/captures/")
			if compact {
				artifact.StorageKey = strings.ReplaceAll(artifact.StorageKey, c.Parent.Binding.DeploymentID, strings.ReplaceAll(c.Parent.Binding.DeploymentID, "-", ""))
			}
		}
		if err := c.Check(time.Now()); err != nil {
			t.Fatal("canonical warm capture refused", err)
		}
	}
}

func TestSnapshotCaptureProtoChecksNestedUnknownsAndExactResponse(t *testing.T) {
	c := snapshotCaptureFixture(t)
	p := &vmmdpb.SnapshotResponse{MemBytes: c.Memory.Bytes, VmstateBytes: c.VMState.Bytes, StoredBytes: 4096, Capture: c.ToProto()}
	got, err := CheckSnapshotResponse(p, c.Parent.Binding.InstanceID, c.Memory.StorageKey, c.VMState.StorageKey)
	if err != nil || !got.Equal(c) {
		t.Fatal("capture did not round trip", err)
	}
	p.Capture.Parent.ArtifactConsumption.Drives[0].Source.StorageKey = "caller-mutated"
	if !got.Equal(c) {
		t.Fatal("proto caller mutated returned evidence")
	}
	for _, edit := range []func(*vmmdpb.SnapshotResponse){
		func(p *vmmdpb.SnapshotResponse) { p.MemBytes++ },
		func(p *vmmdpb.SnapshotResponse) { p.StoredBytes = -1 },
		func(p *vmmdpb.SnapshotResponse) { p.Capture.Parent.Binding.InstanceId = uuid.NewString() },
		func(p *vmmdpb.SnapshotResponse) { p.Capture.PrivateDrive = nil },
		func(p *vmmdpb.SnapshotResponse) { p.Capture.Memory.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) },
		func(p *vmmdpb.SnapshotResponse) { p.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) },
	} {
		p.Capture, p.MemBytes, p.StoredBytes = c.ToProto(), c.Memory.Bytes, 4096
		edit(p)
		if _, err := CheckSnapshotResponse(p, c.Parent.Binding.InstanceID, c.Memory.StorageKey, c.VMState.StorageKey); err == nil {
			t.Fatal("malformed or stale wire evidence accepted")
		}
	}
	if zero, err := SnapshotCaptureFromProto(nil); err != nil || !zero.IsZero() || zero.ToProto() != nil {
		t.Fatal("legacy absence was upgraded to lineage evidence", err)
	}
}
