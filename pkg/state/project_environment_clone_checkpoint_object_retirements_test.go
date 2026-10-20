package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func checkpointObjectRetirementProofFixture() (state.ProjectEnvironmentCloneCheckpointObjectRetirement, state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	target := state.ProjectEnvironmentCloneCheckpointObjectTargetPlan{OperationID: "owner", AccountID: "account", AppID: "app", SourceBucketID: "source", TargetBucketID: "target", BackendID: "backend", BackendFingerprint: "fingerprint", Region: "region", PhysicalName: "private-target", ManifestSHA256: strings.Repeat("a", 64), CheckpointID: "checkpoint"}
	p := state.ProjectEnvironmentCloneCheckpointObjectRetirementPlan{
		SourceResourceID: "production-source",
		Creation:         state.ProjectEnvironmentCloneCheckpointObjectTarget{Plan: target, State: "ready", RequestedAt: now.Add(-3 * time.Hour), ObservationSHA256: strings.Repeat("b", 64)},
		Copy:             &state.ProjectEnvironmentCloneCheckpointObjectCopy{Plan: state.ProjectEnvironmentCloneCheckpointObjectCopyPlan{Inventory: state.ProjectEnvironmentCloneCheckpointObjectVerificationPlan{Target: target, CreationSHA256: strings.Repeat("b", 64), SourceResourceID: "production-source", Objects: []state.ProjectEnvironmentCloneObjectVersion{{Key: "body", VersionID: "original", Size: 7}}, TotalBytes: 7}, WriteUntil: now.Add(-time.Hour)}, State: "requested", RequestedAt: now.Add(-2 * time.Hour)},
	}
	in := state.ProjectEnvironmentCloneCheckpointObjectRetirement{Plan: p, State: "requested", RequestedAt: now.Add(-time.Second)}
	o := state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation{RequestID: state.ProjectEnvironmentCloneCheckpointObjectRetirementRequestID(p), PlanSHA256: state.CloneCheckpointSHA(p), OwnerToken: target.OperationID, SourceResourceID: "production-source", NativeResourceID: target.PhysicalName, CreationRequestID: state.ProjectEnvironmentCloneCheckpointObjectTargetRequestID(target), CreationPlanSHA256: state.CloneCheckpointSHA(target), CopyRequestID: state.ProjectEnvironmentCloneCheckpointObjectCopyRequestID(p.Copy.Plan), CopyPlanSHA256: state.CloneCheckpointSHA(p.Copy.Plan), CreationPermanentlyFenced: true, CopyPermanentlyFenced: true, AllWritersPermanentlyClosed: true, TargetAbsent: true, NoNativeGrants: true, SourceUnchanged: true, RetiredAt: now}
	return in, o
}

func TestCheckpointObjectRetirementProof(t *testing.T) {
	in, o := checkpointObjectRetirementProofFixture()
	if err := state.ValidateCheckpointObjectRetirementObservation(in, o); err != nil {
		t.Fatal("unknown copy after expired write deadline must remain retireable:", err)
	}
	for name, edit := range map[string]func(*state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation){
		"foreign-source": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.SourceResourceID = "other"
		},
		"wrong-request": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.RequestID += "x" },
		"wrong-plan":    func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.PlanSHA256 += "x" },
		"foreign-owner": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.OwnerToken = "other" },
		"source-deletion": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.NativeResourceID = "production-source"
		},
		"foreign-create": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.CreationRequestID += "x"
		},
		"changed-create": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.CreationPlanSHA256 += "x"
		},
		"delayed-create": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.CreationPermanentlyFenced = false
		},
		"foreign-copy": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.CopyRequestID += "x" },
		"changed-copy": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.CopyPlanSHA256 += "x" },
		"delayed-copy": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.CopyPermanentlyFenced = false
		},
		"other-writer": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.AllWritersPermanentlyClosed = false
		},
		"target-present": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.TargetAbsent = false },
		"native-grant":   func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) { o.NoNativeGrants = false },
		"source-mutated": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.SourceUnchanged = false
		},
		"before-claim": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.RetiredAt = in.RequestedAt.Add(-time.Microsecond)
		},
		"future-proof": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.RetiredAt = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		},
		"zero-proof": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.RetiredAt = time.Time{}
		},
		"sub-microsecond": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.RetiredAt = o.RetiredAt.Add(time.Nanosecond)
		},
		"non-utc": func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
			o.RetiredAt = o.RetiredAt.In(time.FixedZone("other", 0))
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := o
			edit(&bad)
			if state.ValidateCheckpointObjectRetirementObservation(in, bad) == nil {
				t.Fatal("unsafe cleanup proof accepted")
			}
		})
	}
	in.State = "retired"
	in.ObservationSHA256 = state.CloneCheckpointSHA(o)
	if err := state.ValidateCheckpointObjectRetirementObservation(in, o); err != nil {
		t.Fatal("exact replay:", err)
	}
	o.RetiredAt = o.RetiredAt.Add(-time.Microsecond)
	if state.ValidateCheckpointObjectRetirementObservation(in, o) == nil {
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
				o.PlanSHA256 = state.CloneCheckpointSHA(in.Plan)
				err := state.ValidateCheckpointObjectRetirementObservation(in, o)
				valid := copyState == "absent" || creationState == "ready"
				if (err == nil) != valid {
					t.Fatalf("valid=%v: %v", valid, err)
				}
				if valid {
					o.CreationPermanentlyFenced = false
					if state.ValidateCheckpointObjectRetirementObservation(in, o) == nil {
						t.Fatal("even an unacknowledged creation requires permanent fencing")
					}
				}
			})
		}
	}
}

func TestCheckpointObjectRetirementPlanTampering(t *testing.T) {
	for name, edit := range map[string]func(*state.ProjectEnvironmentCloneCheckpointObjectRetirement){
		"source-native-as-target": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.SourceResourceID = in.Plan.Creation.Plan.PhysicalName
		},
		"retirement-before-create": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.RequestedAt = in.Plan.Creation.RequestedAt.Add(-time.Microsecond)
		},
		"retirement-before-copy": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.RequestedAt = in.Plan.Copy.RequestedAt.Add(-time.Microsecond)
		},
		"source-as-target": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Creation.Plan.TargetBucketID = in.Plan.Creation.Plan.SourceBucketID
		},
		"missing-owner": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Creation.Plan.AccountID = ""
		},
		"invalid-manifest": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Creation.Plan.ManifestSHA256 = "not-a-hash"
		},
		"foreign-copy-target": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.Inventory.Target.BackendID = "foreign"
		},
		"changed-creation-receipt": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.Inventory.CreationSHA256 = strings.Repeat("d", 64)
		},
		"target-as-copy-source": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.Inventory.SourceResourceID = in.Plan.Creation.Plan.PhysicalName
		},
		"missing-source": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.Inventory.SourceResourceID = ""
		},
		"verification-as-copy": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.Inventory.CopySHA256 = strings.Repeat("d", 64)
		},
		"missing-copy-deadline": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.Plan.WriteUntil = time.Time{}
		},
		"unknown-creation-state": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) { in.Plan.Creation.State = "deleted" },
		"unknown-copy-state":     func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) { in.Plan.Copy.State = "deleted" },
		"missing-creation-digest": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Creation.ObservationSHA256 = ""
		},
		"unclaimed-copy": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.RequestedAt = time.Time{}
		},
		"unclaimed-retirement": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.State = "planned"
			in.RequestedAt = time.Time{}
		},
		"invalid-retirement-digest": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.State = "retired"
			in.ObservationSHA256 = "invalid"
		},
		"unexpected-copy-digest": func(in *state.ProjectEnvironmentCloneCheckpointObjectRetirement) {
			in.Plan.Copy.ObservationSHA256 = strings.Repeat("d", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			in, o := checkpointObjectRetirementProofFixture()
			edit(&in)
			// Recomputing the request digest cannot make inconsistent provenance valid.
			o.PlanSHA256 = state.CloneCheckpointSHA(in.Plan)
			if state.ValidateCheckpointObjectRetirementObservation(in, o) == nil {
				t.Fatal("inconsistent original journal accepted")
			}
		})
	}
	in, o := checkpointObjectRetirementProofFixture()
	in.Plan.Copy = nil
	o.PlanSHA256 = state.CloneCheckpointSHA(in.Plan)
	if state.ValidateCheckpointObjectRetirementObservation(in, o) == nil {
		t.Fatal("copy evidence without an original copy journal accepted")
	}
}

func TestCheckpointObjectRetirementReceiptAuthenticationDoesNotGrantCaptureAuthority(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	bucket := state.ProjectEnvironmentCloneObjectBucket{ID: "source", BackendID: "backend", BackendFingerprint: strings.Repeat("b", 64), PhysicalName: "production-source"}
	views := []state.ProjectEnvironmentCloneBindings{{AppID: "app", SourceScope: "production", ProjectEnvironmentCloneBindingDefinitions: state.ProjectEnvironmentCloneBindingDefinitions{Buckets: []state.ProjectEnvironmentCloneObjectBucket{bucket}}}}
	op := state.ProjectEnvironmentCloneOperation{ID: "owner", AccountID: "account", ProjectID: "project", SourceEnvironment: "production", SourceRevisionHash: strings.Repeat("a", 64)}
	plan := state.ProjectEnvironmentCloneCheckpointPlan{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID, SourceEnvironment: op.SourceEnvironment, SourceRevisionHash: op.SourceRevisionHash, ConfigurationGeneration: 2, ConfigurationHeldAt: now.Add(-time.Second), Sources: []state.ProjectEnvironmentCloneCheckpointSource{{Kind: "object_storage", ID: bucket.ID, AppID: "app", Scope: "production", BackendID: bucket.BackendID, BackendFingerprint: bucket.BackendFingerprint, ProviderResourceID: bucket.PhysicalName, DefinitionSHA256: state.CloneCheckpointSHA(bucket)}}}
	receipt := state.ProjectEnvironmentCloneCheckpointReceipt{RequestID: op.ID, PlanSHA256: state.CloneCheckpointSHA(plan), CheckpointID: "checkpoint", CapturedAt: now, CompleteWriterCoverage: true, Sources: []state.ProjectEnvironmentCloneCheckpointSourceReceipt{{Kind: "object_storage", ID: bucket.ID, CaptureIdentity: "retained"}}}
	point := now.Format(time.RFC3339Nano)
	op.Resources = []state.ProjectEnvironmentCloneResource{{Kind: "data_checkpoint", Name: op.SourceEnvironment, SourceID: op.ID, TargetID: receipt.CheckpointID, SourceVersion: receipt.PlanSHA256, CapturePoint: point, Status: "captured"}, {Kind: "data_checkpoint_receipt", Name: op.SourceEnvironment, SourceID: receipt.CheckpointID, SourceVersion: state.CloneCheckpointSHA(receipt), CapturePoint: point, Status: "captured"}}
	in := state.ProjectEnvironmentCloneCheckpointRelease{Plan: plan, Receipt: receipt, State: "released"}
	adoption := state.ProjectEnvironmentCloneCheckpointAdoption{Receipt: receipt}
	for _, status := range []string{state.CloneOperationCapturing, state.CloneOperationCompensating, state.CloneOperationFailed, state.CloneOperationReady} {
		op.Status = status
		if err := state.ValidateCloneCheckpointAdoptionReceiptContents(op, adoption); err != nil {
			t.Fatal("immutable receipt authentication", status, err)
		}
		if err := state.ValidateCloneCheckpointReleasePlanContents(op, in, views); err != nil {
			t.Fatal("immutable release authentication", status, err)
		}
		capture := status == state.CloneOperationCapturing
		if (state.ValidateCloneCheckpointAdoptionReceipt(op, adoption) == nil) != capture || (state.ValidateCloneCheckpointReleasePlan(op, in, views) == nil) != capture {
			t.Fatal("frozen receipt granted capture authority", status)
		}
	}
	op.Resources[1].SourceVersion = strings.Repeat("f", 64)
	if state.ValidateCloneCheckpointAdoptionReceiptContents(op, adoption) == nil || state.ValidateCloneCheckpointReleasePlanContents(op, in, views) == nil {
		t.Fatal("changed frozen receipt accepted")
	}
}
