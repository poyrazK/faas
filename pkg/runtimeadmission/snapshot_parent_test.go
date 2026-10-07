package runtimeadmission

// adr: 595 Restored serving history cannot renew authority or reuse its input capture.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSnapshotRestoredParentUsesFreshCaptureAndHistoricalClock(t *testing.T) {
	r, _ := snapshotConsumedReceiptFixture(t)
	now, old := time.Now(), time.Now().Add(-time.Hour)
	r.Binding.IssuedAtUnixNano, r.Binding.ExpiresAtUnixNano = old.UnixNano(), old.Add(time.Minute).UnixNano()
	r.CompletedAtUnixNano = old.UnixNano()
	prefix := "snap/" + r.Binding.DeploymentID + "/captures/" + uuid.NewString() + "/v2/"
	c := SnapshotCapture{Version: SnapshotCaptureVersion, Parent: r.Clone(), Memory: r.SnapshotConsumption.Memory,
		VMState: r.SnapshotConsumption.VMState, PrivateDrive: r.SnapshotConsumption.PrivateDrive, CapturedAtUnixNano: now.UnixNano()}
	c.Memory.StorageKey, c.VMState.StorageKey, c.PrivateDrive.StorageKey = prefix+"mem", prefix+"vmstate", prefix+"drive"
	g := SnapshotGrant{Version: SnapshotGrantVersion, Token: strings.Split(prefix, "/")[3], Parent: r.Clone(),
		MemoryKey: c.Memory.StorageKey, VMStateKey: c.VMState.StorageKey, PrivateDriveKey: c.PrivateDrive.StorageKey,
		FCVersion: "1.12.1", Mode: "park", SourceStartedAtUnixNano: old.UnixNano(), IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	if r.Binding.Validate(now) == nil || c.Check(now) != nil || g.Validate(now) != nil {
		t.Fatal("serving history renewed its grant or refused a fresh capture")
	}
	wire := c.ToProto()
	got, err := SnapshotCaptureFromProto(wire)
	if err != nil || !got.Equal(c) || got.Check(now) != nil {
		t.Fatal("restored parent proof was lost over the wire", err)
	}
	wire.Parent.SnapshotConsumption.Memory.Digest = "reader-change"
	if !got.Equal(c) {
		t.Fatal("wire caller changed owned serving history")
	}
	for _, variant := range []string{"paused", "missing proof", "wrong process", "reused input", "reused compact warm input"} {
		t.Run(variant, func(t *testing.T) {
			bad, grant := c.Clone(), g.Clone()
			switch variant {
			case "paused":
				bad.Parent.Paused = true
			case "missing proof":
				bad.Parent.SnapshotConsumption = SnapshotConsumption{}
			case "wrong process":
				bad.Parent.ArtifactConsumption.ProcessStart = ""
			case "reused input", "reused compact warm input":
				bad.Memory, bad.VMState, bad.PrivateDrive = r.SnapshotConsumption.Memory, r.SnapshotConsumption.VMState, r.SnapshotConsumption.PrivateDrive
				grant.Token = r.SnapshotConsumption.CaptureToken
				if variant == "reused compact warm input" {
					for _, a := range []*CapturedArtifact{&bad.Memory, &bad.VMState, &bad.PrivateDrive} {
						a.StorageKey = strings.ReplaceAll(a.StorageKey, r.Binding.DeploymentID, strings.ReplaceAll(r.Binding.DeploymentID, "-", ""))
						if !strings.Contains(a.StorageKey, "/warm/") {
							a.StorageKey = strings.ReplaceAll(a.StorageKey, "/captures/", "/warm/captures/")
						}
					}
					grant.Mode = "warm"
				}
			}
			grant.Parent = bad.Parent.Clone()
			grant.MemoryKey, grant.VMStateKey, grant.PrivateDriveKey = bad.Memory.StorageKey, bad.VMState.StorageKey, bad.PrivateDrive.StorageKey
			if bad.Check(now) == nil || grant.Validate(now) == nil {
				t.Fatal("invalid serving lineage acquired fresh capture authority")
			}
		})
	}
}
