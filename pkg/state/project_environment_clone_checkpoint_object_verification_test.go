package state

import (
	"strings"
	"testing"
	"time"
)

func TestCheckpointObjectVerificationProof(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	in := ProjectEnvironmentCloneCheckpointObjectVerification{State: "requested", RequestedAt: now.Add(-time.Second), Plan: ProjectEnvironmentCloneCheckpointObjectVerificationPlan{Target: ProjectEnvironmentCloneCheckpointObjectTargetPlan{OperationID: "owner", TargetBucketID: "target", PhysicalName: "private"}, Objects: []ProjectEnvironmentCloneObjectVersion{{Key: "empty", Size: 0}, {Key: "body", Size: 7}}, TotalBytes: 7}}
	until := now.Add(time.Minute)
	o := ProjectEnvironmentCloneCheckpointObjectVerificationObservation{RequestID: ProjectEnvironmentCloneCheckpointObjectVerificationRequestID(in.Plan), PlanSHA256: cloneCheckpointSHA(in.Plan), OwnerToken: "owner", NativeResourceID: "private", SourceSHA256: strings.Repeat("a", 64), TargetSHA256: strings.Repeat("a", 64), Complete: true, MetadataComplete: true, UnsupportedObjectsAbsent: true, ReadOnly: true, PinnedVersionsOnly: true, Private: true, AllWritersClosed: true, NoCredentials: true, TargetContentsUnchanged: true, ObjectCount: 2, SourceReadBytes: 7, TargetReadBytes: 7, ComparedAt: now, ClosedThrough: until}
	if err := validateCheckpointObjectVerificationObservation(in, o, until); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*ProjectEnvironmentCloneCheckpointObjectVerificationObservation){
		"request": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.RequestID += "x" },
		"plan": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.PlanSHA256 = strings.Repeat("b", 64)
		},
		"owner":  func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.OwnerToken = "other" },
		"target": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.NativeResourceID = "other" },
		"body": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.TargetSHA256 = strings.Repeat("b", 64)
		},
		"metadata": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.MetadataComplete = false },
		"unsupported": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.UnsupportedObjectsAbsent = false
		},
		"partial":     func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.Complete = false },
		"write":       func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.ReadOnly = false },
		"live-source": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.PinnedVersionsOnly = false },
		"public":      func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.Private = false },
		"writers":     func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.AllWritersClosed = false },
		"grant":       func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.NoCredentials = false },
		"changed": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.TargetContentsUnchanged = false
		},
		"extra-key":    func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.ObjectCount++ },
		"source-bytes": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.SourceReadBytes-- },
		"target-bytes": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.TargetReadBytes++ },
		"expired": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.ClosedThrough = until.Add(-time.Microsecond)
		},
		"pre-claim": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.ComparedAt = in.RequestedAt.Add(-time.Microsecond)
		},
		"future": func(p *ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.ComparedAt = until },
	} {
		t.Run(name, func(t *testing.T) {
			bad := o
			edit(&bad)
			if validateCheckpointObjectVerificationObservation(in, bad, until) == nil {
				t.Fatal("accepted invalid proof")
			}
		})
	}
	in.State = "verified"
	in.ObservationSHA256 = checkpointObjectVerificationSHA(o)
	renewed := until.Add(time.Minute)
	o.ClosedThrough = renewed
	if err := validateCheckpointObjectVerificationObservation(in, o, renewed); err != nil {
		t.Fatal("fresh closure changed receipt", err)
	}
	o.ComparedAt = now.Add(-time.Microsecond)
	if validateCheckpointObjectVerificationObservation(in, o, renewed) == nil {
		t.Fatal("replacement receipt accepted")
	}
	in.State = "requested"
	in.Plan.Objects = []ProjectEnvironmentCloneObjectVersion{}
	in.Plan.TotalBytes = 0
	o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
	o.ObjectCount = 0
	o.SourceReadBytes = 0
	o.TargetReadBytes = 0
	o.ComparedAt = now
	if err := validateCheckpointObjectVerificationObservation(in, o, renewed); err != nil {
		t.Fatal("empty inventory", err)
	}
}
