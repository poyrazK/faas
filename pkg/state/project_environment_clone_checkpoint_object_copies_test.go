package state

import (
	"testing"
	"time"
)

func TestCheckpointObjectCopyProof(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	until := now.Add(time.Minute)
	in := ProjectEnvironmentCloneCheckpointObjectCopy{State: "requested", RequestedAt: now.Add(-time.Second), Plan: ProjectEnvironmentCloneCheckpointObjectCopyPlan{Inventory: ProjectEnvironmentCloneCheckpointObjectVerificationPlan{Target: ProjectEnvironmentCloneCheckpointObjectTargetPlan{OperationID: "owner", TargetBucketID: "target", PhysicalName: "private"}, Objects: []ProjectEnvironmentCloneObjectVersion{{Key: "empty"}, {Key: "body", Size: 7}}, TotalBytes: 7}, WriteUntil: until}}
	o := ProjectEnvironmentCloneCheckpointObjectCopyObservation{RequestID: ProjectEnvironmentCloneCheckpointObjectCopyRequestID(in.Plan), PlanSHA256: cloneCheckpointSHA(in.Plan), OwnerToken: "owner", NativeResourceID: "private", Complete: true, MetadataComplete: true, UnsupportedObjectsAbsent: true, PinnedVersionsOnly: true, Private: true, NoCredentials: true, WritersPermanentlyClosed: true, TargetContentsUnchanged: true, ObjectCount: 2, BytesCopied: 7, CopiedAt: now, ClosedThrough: until}
	if err := validateCheckpointObjectCopyObservation(in, o, until); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*ProjectEnvironmentCloneCheckpointObjectCopyObservation){
		"owner":       func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.OwnerToken = "other" },
		"request":     func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.RequestID += "x" },
		"plan":        func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.PlanSHA256 += "x" },
		"target":      func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.NativeResourceID = "other" },
		"partial":     func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.Complete = false },
		"metadata":    func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.MetadataComplete = false },
		"unsupported": func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.UnsupportedObjectsAbsent = false },
		"live-source": func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.PinnedVersionsOnly = false },
		"public":      func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.Private = false },
		"grant":       func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.NoCredentials = false },
		"late-writer": func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.WritersPermanentlyClosed = false },
		"changed":     func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.TargetContentsUnchanged = false },
		"missing-key": func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.ObjectCount-- },
		"bytes":       func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) { p.BytesCopied++ },
		"expired-closure": func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
			p.ClosedThrough = until.Add(-time.Microsecond)
		},
		"pre-claim": func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
			p.CopiedAt = in.RequestedAt.Add(-time.Microsecond)
		},
		"late-copy": func(p *ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
			p.CopiedAt = until.Add(time.Microsecond)
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := o
			edit(&bad)
			if validateCheckpointObjectCopyObservation(in, bad, until) == nil {
				t.Fatal("invalid copy accepted")
			}
		})
	}
	in.State = "copied"
	in.ObservationSHA256 = checkpointObjectCopySHA(o)
	renewed := until.Add(time.Minute)
	o.ClosedThrough = renewed
	if err := validateCheckpointObjectCopyObservation(in, o, renewed); err != nil {
		t.Fatal("handoff receipt", err)
	}
	o.CopiedAt = now.Add(-time.Microsecond)
	if validateCheckpointObjectCopyObservation(in, o, renewed) == nil {
		t.Fatal("changed terminal receipt")
	}
	in.State = "requested"
	in.Plan.Inventory.Objects = []ProjectEnvironmentCloneObjectVersion{}
	in.Plan.Inventory.TotalBytes = 0
	o.ObjectCount = 0
	o.BytesCopied = 0
	o.CopiedAt = now
	o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
	if err := validateCheckpointObjectCopyObservation(in, o, renewed); err != nil {
		t.Fatal("empty copy", err)
	}
}
