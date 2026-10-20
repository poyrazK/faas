package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestCheckpointObjectVerificationProof(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	in := state.ProjectEnvironmentCloneCheckpointObjectVerification{State: "requested", RequestedAt: now.Add(-time.Second), Plan: state.ProjectEnvironmentCloneCheckpointObjectVerificationPlan{Target: state.ProjectEnvironmentCloneCheckpointObjectTargetPlan{OperationID: "owner", TargetBucketID: "target", PhysicalName: "private"}, Objects: []state.ProjectEnvironmentCloneObjectVersion{{Key: "empty", Size: 0}, {Key: "body", Size: 7}}, TotalBytes: 7}}
	until := now.Add(time.Minute)
	o := state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation{RequestID: state.ProjectEnvironmentCloneCheckpointObjectVerificationRequestID(in.Plan), PlanSHA256: state.CloneCheckpointSHA(in.Plan), OwnerToken: "owner", NativeResourceID: "private", SourceSHA256: strings.Repeat("a", 64), TargetSHA256: strings.Repeat("a", 64), Complete: true, MetadataComplete: true, UnsupportedObjectsAbsent: true, ReadOnly: true, PinnedVersionsOnly: true, Private: true, AllWritersClosed: true, NoCredentials: true, TargetContentsUnchanged: true, ObjectCount: 2, SourceReadBytes: 7, TargetReadBytes: 7, ComparedAt: now, ClosedThrough: until}
	if err := state.ValidateCheckpointObjectVerificationObservation(in, o, until); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation){
		"request": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.RequestID += "x" },
		"plan": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.PlanSHA256 = strings.Repeat("b", 64)
		},
		"owner": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.OwnerToken = "other" },
		"target": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.NativeResourceID = "other"
		},
		"body": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.TargetSHA256 = strings.Repeat("b", 64)
		},
		"metadata": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.MetadataComplete = false
		},
		"unsupported": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.UnsupportedObjectsAbsent = false
		},
		"partial": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.Complete = false },
		"write":   func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.ReadOnly = false },
		"live-source": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.PinnedVersionsOnly = false
		},
		"public": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.Private = false },
		"writers": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.AllWritersClosed = false
		},
		"grant": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.NoCredentials = false
		},
		"changed": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.TargetContentsUnchanged = false
		},
		"extra-key":    func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.ObjectCount++ },
		"source-bytes": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.SourceReadBytes-- },
		"target-bytes": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.TargetReadBytes++ },
		"expired": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.ClosedThrough = until.Add(-time.Microsecond)
		},
		"pre-claim": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
			p.ComparedAt = in.RequestedAt.Add(-time.Microsecond)
		},
		"future": func(p *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) { p.ComparedAt = until },
	} {
		t.Run(name, func(t *testing.T) {
			bad := o
			edit(&bad)
			if state.ValidateCheckpointObjectVerificationObservation(in, bad, until) == nil {
				t.Fatal("accepted invalid proof")
			}
		})
	}
	in.State = "verified"
	in.ObservationSHA256 = state.CheckpointObjectVerificationSHA(o)
	renewed := until.Add(time.Minute)
	o.ClosedThrough = renewed
	if err := state.ValidateCheckpointObjectVerificationObservation(in, o, renewed); err != nil {
		t.Fatal("fresh closure changed receipt", err)
	}
	o.ComparedAt = now.Add(-time.Microsecond)
	if state.ValidateCheckpointObjectVerificationObservation(in, o, renewed) == nil {
		t.Fatal("replacement receipt accepted")
	}
	in.State = "requested"
	in.Plan.Objects = []state.ProjectEnvironmentCloneObjectVersion{}
	in.Plan.TotalBytes = 0
	o.PlanSHA256 = state.CloneCheckpointSHA(in.Plan)
	o.ObjectCount = 0
	o.SourceReadBytes = 0
	o.TargetReadBytes = 0
	o.ComparedAt = now
	if err := state.ValidateCheckpointObjectVerificationObservation(in, o, renewed); err != nil {
		t.Fatal("empty inventory", err)
	}
}
