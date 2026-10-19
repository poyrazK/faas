package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestCheckpointObjectCopyProof(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	until := now.Add(time.Minute)
	in := state.ProjectEnvironmentCloneCheckpointObjectCopy{State: "requested", RequestedAt: now.Add(-time.Second), Plan: state.ProjectEnvironmentCloneCheckpointObjectCopyPlan{Inventory: state.ProjectEnvironmentCloneCheckpointObjectVerificationPlan{Target: state.ProjectEnvironmentCloneCheckpointObjectTargetPlan{OperationID: "owner", TargetBucketID: "target", PhysicalName: "private"}, Objects: []state.ProjectEnvironmentCloneObjectVersion{{Key: "empty"}, {Key: "body", Size: 7}}, TotalBytes: 7}, WriteUntil: until}}
	o := state.ProjectEnvironmentCloneCheckpointObjectCopyObservation{RequestID: state.ProjectEnvironmentCloneCheckpointObjectCopyRequestID(in.Plan), PlanSHA256: state.CloneCheckpointSHA(in.Plan), OwnerToken: "owner", NativeResourceID: "private", Complete: true, MetadataComplete: true, UnsupportedObjectsAbsent: true, PinnedVersionsOnly: true, Private: true, NoCredentials: true, WritersPermanentlyClosed: true, TargetContentsUnchanged: true, ObjectCount: 2, BytesCopied: 7, CopiedAt: now, ClosedThrough: until}
	if err := state.ValidateCheckpointObjectCopyObservation(in, o, until); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*state.ProjectEnvironmentCloneCheckpointObjectCopyObservation){
		"owner":    func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.OwnerToken = "other" },
		"request":  func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.RequestID += "x" },
		"plan":     func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.PlanSHA256 += "x" },
		"target":   func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.NativeResourceID = "other" },
		"partial":  func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.Complete = false },
		"metadata": func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.MetadataComplete = false },
		"unsupported": func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
			p.UnsupportedObjectsAbsent = false
		},
		"live-source": func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.PinnedVersionsOnly = false },
		"public":      func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.Private = false },
		"grant":       func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.NoCredentials = false },
		"late-writer": func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
			p.WritersPermanentlyClosed = false
		},
		"changed": func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
			p.TargetContentsUnchanged = false
		},
		"missing-key": func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.ObjectCount-- },
		"bytes":       func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.BytesCopied++ },
		"expired-closure": func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
			p.ClosedThrough = until.Add(-time.Microsecond)
		},
		"pre-claim": func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
			p.CopiedAt = in.RequestedAt.Add(-time.Microsecond)
		},
		"late-copy": func(p *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
			p.CopiedAt = until.Add(time.Microsecond)
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := o
			edit(&bad)
			if state.ValidateCheckpointObjectCopyObservation(in, bad, until) == nil {
				t.Fatal("invalid copy accepted")
			}
		})
	}
	in.State = "copied"
	in.ObservationSHA256 = state.CheckpointObjectCopySHA(o)
	renewed := until.Add(time.Minute)
	o.ClosedThrough = renewed
	if err := state.ValidateCheckpointObjectCopyObservation(in, o, renewed); err != nil {
		t.Fatal("handoff receipt", err)
	}
	o.CopiedAt = now.Add(-time.Microsecond)
	if state.ValidateCheckpointObjectCopyObservation(in, o, renewed) == nil {
		t.Fatal("changed terminal receipt")
	}
	in.State = "requested"
	in.Plan.Inventory.Objects = []state.ProjectEnvironmentCloneObjectVersion{}
	in.Plan.Inventory.TotalBytes = 0
	o.ObjectCount = 0
	o.BytesCopied = 0
	o.CopiedAt = now
	o.PlanSHA256 = state.CloneCheckpointSHA(in.Plan)
	if err := state.ValidateCheckpointObjectCopyObservation(in, o, renewed); err != nil {
		t.Fatal("empty copy", err)
	}
}
