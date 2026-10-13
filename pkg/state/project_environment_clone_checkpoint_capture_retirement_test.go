package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func captureRetirementFixture(captured bool, release string) (ProjectEnvironmentCloneCheckpointCaptureRetirement, ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
	s, proof := sourceCompensationFixture(captured, release)
	s.State, s.Observation, s.ObservationSHA256 = "compensated", proof, cloneCheckpointSHA(proof)
	in := ProjectEnvironmentCloneCheckpointCaptureRetirement{Plan: ProjectEnvironmentCloneCheckpointCaptureRetirementPlan{SourceCompensation: s}, State: "requested", RequestedAt: proof.CompensatedAt.Add(time.Minute)}
	o := ProjectEnvironmentCloneCheckpointCaptureRetirementObservation{RequestID: ProjectEnvironmentCloneCheckpointCaptureRetirementRequestID(s.Plan.Capture.OperationID), PlanSHA256: cloneCheckpointSHA(in.Plan), OwnerToken: s.Plan.Capture.OperationID, CaptureRequestID: s.Plan.Capture.OperationID, CapturePlanSHA256: cloneCheckpointSHA(s.Plan.Capture), CapturePermanentlyFenced: true, RetiredAt: in.RequestedAt.Add(time.Minute)}
	for _, source := range proof.Sources {
		o.Sources = append(o.Sources, ProjectEnvironmentCloneCheckpointRetiredCapture{Kind: source.Kind, ID: source.ID, SourceResourceID: source.SourceResourceID, CaptureIdentity: source.CaptureIdentity, OwnerToken: source.OwnerToken, NoOwnedArtifacts: true, NoNativeBorrowers: true, SourceUnchanged: true, SourceAccessRestored: true})
	}
	return in, o
}

func TestCheckpointCaptureRetirementOriginalCapturePhases(t *testing.T) {
	for _, phase := range []string{"planned", "unknown", "captured", "release-planned", "release-unknown", "released"} {
		t.Run(phase, func(t *testing.T) {
			captured, release := phase != "planned" && phase != "unknown", ""
			switch phase {
			case "release-planned":
				release = "planned"
			case "release-unknown":
				release = "requested"
			case "released":
				release = "released"
			}
			in, o := captureRetirementFixture(captured, release)
			if phase == "planned" {
				in.Plan.SourceCompensation.Plan.Intent.Status = "planned"
				in.Plan.SourceCompensation.Observation.PlanSHA256 = cloneCheckpointSHA(in.Plan.SourceCompensation.Plan)
				in.Plan.SourceCompensation.ObservationSHA256 = cloneCheckpointSHA(in.Plan.SourceCompensation.Observation)
				o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
			}
			if err := validateCheckpointCaptureRetirementObservation(in, o); err != nil {
				t.Fatal(err)
			}
			in.State, in.Observation, in.ObservationSHA256 = "retired", o, cloneCheckpointSHA(o)
			if err := validateCheckpointCaptureRetirementObservation(in, o); err != nil {
				t.Fatalf("terminal replay: %v", err)
			}
			o.RetiredAt = o.RetiredAt.Add(time.Microsecond)
			if err := validateCheckpointCaptureRetirementObservation(in, o); !errors.Is(err, ErrConflict) {
				t.Fatalf("changed terminal proof accepted: %v", err)
			}
		})
	}
}

func TestCheckpointCaptureRetirementRejectsIncompleteOrForeignEvidence(t *testing.T) {
	cases := []struct {
		name   string
		change func(*ProjectEnvironmentCloneCheckpointCaptureRetirement, *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation)
	}{
		{"source compensation pending", func(i *ProjectEnvironmentCloneCheckpointCaptureRetirement, _ *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			i.Plan.SourceCompensation.State = "requested"
		}},
		{"source proof changed", func(i *ProjectEnvironmentCloneCheckpointCaptureRetirement, _ *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			i.Plan.SourceCompensation.Observation.Sources[0].SourceUnchanged = false
		}},
		{"source digest changed", func(i *ProjectEnvironmentCloneCheckpointCaptureRetirement, _ *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			i.Plan.SourceCompensation.ObservationSHA256 = cloneCheckpointSHA("foreign")
		}},
		{"dispatch not claimed", func(i *ProjectEnvironmentCloneCheckpointCaptureRetirement, _ *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			i.State, i.RequestedAt = "planned", time.Time{}
		}},
		{"claim before reopening", func(i *ProjectEnvironmentCloneCheckpointCaptureRetirement, _ *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			i.RequestedAt = i.Plan.SourceCompensation.Observation.CompensatedAt.Add(-time.Microsecond)
		}},
		{"foreign request", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.RequestID += "foreign"
		}},
		{"foreign plan", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.PlanSHA256 = cloneCheckpointSHA("foreign")
		}},
		{"foreign owner", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.OwnerToken += "foreign"
		}},
		{"foreign capture request", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.CaptureRequestID += "foreign"
		}},
		{"foreign capture plan", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.CapturePlanSHA256 = cloneCheckpointSHA("foreign")
		}},
		{"capture not fenced", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.CapturePermanentlyFenced = false
		}},
		{"missing source", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources = o.Sources[:1]
		}},
		{"duplicate source", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[1] = o.Sources[0]
		}},
		{"reordered sources", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0], o.Sources[1] = o.Sources[1], o.Sources[0]
		}},
		{"foreign source kind", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].Kind = "object_storage"
		}},
		{"foreign source", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].ID += "foreign"
		}},
		{"foreign physical source", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].SourceResourceID += "foreign"
		}},
		{"replacement capture", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].CaptureIdentity += "foreign"
		}},
		{"foreign artifact owner", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].OwnerToken += "foreign"
		}},
		{"retained artifacts", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].NoOwnedArtifacts = false
		}},
		{"live borrower", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].NoNativeBorrowers = false
		}},
		{"source changed", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].SourceUnchanged = false
		}},
		{"source access changed", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].SourceAccessRestored = false
		}},
		{"before claim", func(i *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.RetiredAt = i.RequestedAt.Add(-time.Microsecond)
		}},
		{"future proof", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.RetiredAt = time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		}},
		{"noncanonical time", func(_ *ProjectEnvironmentCloneCheckpointCaptureRetirement, o *ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.RetiredAt = o.RetiredAt.Add(time.Nanosecond)
		}},
	}
	for _, captured := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.name+map[bool]string{false: "/unknown", true: "/captured"}[captured], func(t *testing.T) {
				in, o := captureRetirementFixture(captured, "")
				tc.change(&in, &o)
				if err := validateCheckpointCaptureRetirementObservation(in, o); !errors.Is(err, ErrConflict) {
					t.Fatalf("invalid retirement accepted: %v", err)
				}
			})
		}
	}
}

func TestCheckpointCaptureRetirementPlanRoundTrip(t *testing.T) {
	in, _ := captureRetirementFixture(true, "released")
	raw, err := json.Marshal(in.Plan)
	if err != nil {
		t.Fatal(err)
	}
	var replay ProjectEnvironmentCloneCheckpointCaptureRetirementPlan
	if err := json.Unmarshal(raw, &replay); err != nil {
		t.Fatal(err)
	}
	if validateCheckpointCaptureRetirementPlan(replay) != nil || cloneCheckpointSHA(replay) != cloneCheckpointSHA(in.Plan) {
		t.Fatal("saved plan changed")
	}
}
