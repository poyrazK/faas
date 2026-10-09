package state

import (
	"strings"
	"testing"
	"time"
)

func checkpointObjectRetirementProofFixture() (ProjectEnvironmentCloneCheckpointObjectRetirement, ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	target := ProjectEnvironmentCloneCheckpointObjectTargetPlan{OperationID: "owner", AccountID: "account", AppID: "app", SourceBucketID: "source", TargetBucketID: "target", BackendID: "backend", BackendFingerprint: "fingerprint", Region: "region", PhysicalName: "private-target", ManifestSHA256: strings.Repeat("a", 64), CheckpointID: "checkpoint"}
	p := ProjectEnvironmentCloneCheckpointObjectRetirementPlan{
		SourceResourceID: "production-source",
		Creation:         ProjectEnvironmentCloneCheckpointObjectTarget{Plan: target, State: "ready", RequestedAt: now.Add(-3 * time.Hour), ObservationSHA256: strings.Repeat("b", 64)},
		Copy:             &ProjectEnvironmentCloneCheckpointObjectCopy{Plan: ProjectEnvironmentCloneCheckpointObjectCopyPlan{Inventory: ProjectEnvironmentCloneCheckpointObjectVerificationPlan{Target: target, CreationSHA256: strings.Repeat("b", 64), SourceResourceID: "production-source", Objects: []ProjectEnvironmentCloneObjectVersion{{Key: "body", VersionID: "original", Size: 7}}, TotalBytes: 7}, WriteUntil: now.Add(-time.Hour)}, State: "requested", RequestedAt: now.Add(-2 * time.Hour)},
	}
	in := ProjectEnvironmentCloneCheckpointObjectRetirement{Plan: p, State: "requested", RequestedAt: now.Add(-time.Second)}
	o := ProjectEnvironmentCloneCheckpointObjectRetirementObservation{RequestID: ProjectEnvironmentCloneCheckpointObjectRetirementRequestID(p), PlanSHA256: cloneCheckpointSHA(p), OwnerToken: target.OperationID, SourceResourceID: "production-source", NativeResourceID: target.PhysicalName, CreationRequestID: ProjectEnvironmentCloneCheckpointObjectTargetRequestID(target), CreationPlanSHA256: cloneCheckpointSHA(target), CopyRequestID: ProjectEnvironmentCloneCheckpointObjectCopyRequestID(p.Copy.Plan), CopyPlanSHA256: cloneCheckpointSHA(p.Copy.Plan), CreationPermanentlyFenced: true, CopyPermanentlyFenced: true, AllWritersPermanentlyClosed: true, TargetAbsent: true, NoNativeGrants: true, SourceUnchanged: true, RetiredAt: now}
	return in, o
}

func TestCheckpointObjectRetirementProof(t *testing.T) {
	in, o := checkpointObjectRetirementProofFixture()
	if err := validateCheckpointObjectRetirementObservation(in, o); err != nil {
		t.Fatal("unknown copy after expired write deadline must remain retireable:", err)
	}
	for name, edit := range map[string]func(*ProjectEnvironmentCloneCheckpointObjectRetirementObservation){
		"foreign-source": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.SourceResourceID = "other" },
		"wrong-request":  func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.RequestID += "x" },
		"wrong-plan":     func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.PlanSHA256 += "x" },
		"foreign-owner":  func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.OwnerToken = "other" },
		"source-deletion": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.NativeResourceID = "production-source"
		},
		"foreign-create": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.CreationRequestID += "x" },
		"changed-create": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.CreationPlanSHA256 += "x" },
		"delayed-create": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.CreationPermanentlyFenced = false
		},
		"foreign-copy": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.CopyRequestID += "x" },
		"changed-copy": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.CopyPlanSHA256 += "x" },
		"delayed-copy": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.CopyPermanentlyFenced = false },
		"other-writer": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.AllWritersPermanentlyClosed = false
		},
		"target-present": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.TargetAbsent = false },
		"native-grant":   func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.NoNativeGrants = false },
		"source-mutated": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.SourceUnchanged = false },
		"before-claim": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.RetiredAt = in.RequestedAt.Add(-time.Microsecond)
		},
		"future-proof": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.RetiredAt = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		},
		"zero-proof": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.RetiredAt = time.Time{} },
		"sub-microsecond": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.RetiredAt = o.RetiredAt.Add(time.Nanosecond)
		},
		"non-utc": func(o *ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.RetiredAt = o.RetiredAt.In(time.FixedZone("other", 0))
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := o
			edit(&bad)
			if validateCheckpointObjectRetirementObservation(in, bad) == nil {
				t.Fatal("unsafe cleanup proof accepted")
			}
		})
	}
	in.State = "retired"
	in.ObservationSHA256 = cloneCheckpointSHA(o)
	if err := validateCheckpointObjectRetirementObservation(in, o); err != nil {
		t.Fatal("exact replay:", err)
	}
	o.RetiredAt = o.RetiredAt.Add(-time.Microsecond)
	if validateCheckpointObjectRetirementObservation(in, o) == nil {
		t.Fatal("changed terminal proof accepted")
	}
}

func TestCheckpointObjectRetirementOriginalRequests(t *testing.T) {
	for _, creationState := range []string{"planned", "requested", "ready"} {
		for _, copyState := range []string{"absent", "planned", "requested", "copied"} {
			t.Run(creationState+"/"+copyState, func(t *testing.T) {
				in, o := checkpointObjectRetirementProofFixture()
				in.Plan.Creation.State = creationState
				if creationState != "ready" {
					in.Plan.Creation.ObservationSHA256 = ""
				}
				if creationState == "planned" {
					in.Plan.Creation.RequestedAt = time.Time{}
				}
				if copyState == "absent" {
					in.Plan.Copy = nil
					o.CopyRequestID, o.CopyPlanSHA256, o.CopyPermanentlyFenced = "", "", false
				} else {
					in.Plan.Copy.State = copyState
					if copyState == "planned" {
						in.Plan.Copy.RequestedAt = time.Time{}
					}
					if copyState == "copied" {
						in.Plan.Copy.ObservationSHA256 = strings.Repeat("c", 64)
					}
				}
				o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
				err := validateCheckpointObjectRetirementObservation(in, o)
				valid := copyState == "absent" || creationState == "ready"
				if (err == nil) != valid {
					t.Fatalf("valid=%v: %v", valid, err)
				}
				if valid {
					o.CreationPermanentlyFenced = false
					if validateCheckpointObjectRetirementObservation(in, o) == nil {
						t.Fatal("even an unacknowledged creation requires permanent fencing")
					}
				}
			})
		}
	}
}

func TestCheckpointObjectRetirementPlanTampering(t *testing.T) {
	for name, edit := range map[string]func(*ProjectEnvironmentCloneCheckpointObjectRetirement){
		"source-native-as-target": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.SourceResourceID = in.Plan.Creation.Plan.PhysicalName
		},
		"retirement-before-create": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.RequestedAt = in.Plan.Creation.RequestedAt.Add(-time.Microsecond)
		},
		"retirement-before-copy": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.RequestedAt = in.Plan.Copy.RequestedAt.Add(-time.Microsecond)
		},
		"source-as-target": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Creation.Plan.TargetBucketID = in.Plan.Creation.Plan.SourceBucketID
		},
		"missing-owner": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) { in.Plan.Creation.Plan.AccountID = "" },
		"invalid-manifest": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Creation.Plan.ManifestSHA256 = "not-a-hash"
		},
		"foreign-copy-target": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.Inventory.Target.BackendID = "foreign"
		},
		"changed-creation-receipt": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.Inventory.CreationSHA256 = strings.Repeat("d", 64)
		},
		"target-as-copy-source": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.Inventory.SourceResourceID = in.Plan.Creation.Plan.PhysicalName
		},
		"missing-source": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.Inventory.SourceResourceID = ""
		},
		"verification-as-copy": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.Inventory.CopySHA256 = strings.Repeat("d", 64)
		},
		"missing-copy-deadline": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.WriteUntil = time.Time{}
		},
		"unknown-creation-state":  func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) { in.Plan.Creation.State = "deleted" },
		"unknown-copy-state":      func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) { in.Plan.Copy.State = "deleted" },
		"missing-creation-digest": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) { in.Plan.Creation.ObservationSHA256 = "" },
		"unclaimed-copy":          func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) { in.Plan.Copy.RequestedAt = time.Time{} },
		"unclaimed-retirement": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.State = "planned"
			in.RequestedAt = time.Time{}
		},
		"invalid-retirement-digest": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.State = "retired"
			in.ObservationSHA256 = "invalid"
		},
		"unexpected-copy-digest": func(in *ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.ObservationSHA256 = strings.Repeat("d", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			in, o := checkpointObjectRetirementProofFixture()
			edit(&in)
			// Recomputing the request digest cannot make inconsistent provenance valid.
			o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
			if validateCheckpointObjectRetirementObservation(in, o) == nil {
				t.Fatal("inconsistent original journal accepted")
			}
		})
	}
	in, o := checkpointObjectRetirementProofFixture()
	in.Plan.Copy = nil
	o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
	if validateCheckpointObjectRetirementObservation(in, o) == nil {
		t.Fatal("copy evidence without an original copy journal accepted")
	}
}
