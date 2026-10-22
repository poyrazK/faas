package state_test

import (
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func captureRetirementFixture(captured bool, release string) (state.ProjectEnvironmentCloneCheckpointCaptureRetirement, state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
	s, proof := state.SourceCompensationFixture(captured, release)
	s.State, s.Observation, s.ObservationSHA256 = "compensated", proof, state.CloneCheckpointSHA(proof)
	in := state.ProjectEnvironmentCloneCheckpointCaptureRetirement{Plan: state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan{SourceCompensation: s}, State: "requested", RequestedAt: proof.CompensatedAt.Add(time.Minute)}
	o := state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation{RequestID: state.ProjectEnvironmentCloneCheckpointCaptureRetirementRequestID(s.Plan.Capture.OperationID), PlanSHA256: state.CloneCheckpointSHA(in.Plan), OwnerToken: s.Plan.Capture.OperationID, CaptureRequestID: s.Plan.Capture.OperationID, CapturePlanSHA256: state.CloneCheckpointSHA(s.Plan.Capture), CapturePermanentlyFenced: true, RetiredAt: in.RequestedAt.Add(time.Minute)}
	for _, source := range proof.Sources {
		o.Sources = append(o.Sources, state.ProjectEnvironmentCloneCheckpointRetiredCapture{Kind: source.Kind, ID: source.ID, SourceResourceID: source.SourceResourceID, CaptureIdentity: source.CaptureIdentity, OwnerToken: source.OwnerToken, NoOwnedArtifacts: true, NoNativeBorrowers: true, SourceUnchanged: true, SourceAccessRestored: true})
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
				in.Plan.SourceCompensation.Observation.PlanSHA256 = state.CloneCheckpointSHA(in.Plan.SourceCompensation.Plan)
				in.Plan.SourceCompensation.ObservationSHA256 = state.CloneCheckpointSHA(in.Plan.SourceCompensation.Observation)
				o.PlanSHA256 = state.CloneCheckpointSHA(in.Plan)
			}
			if err := state.ValidateCheckpointCaptureRetirementObservation(in, o); err != nil {
				t.Fatal(err)
			}
			in.State, in.Observation, in.ObservationSHA256 = "retired", o, state.CloneCheckpointSHA(o)
			if err := state.ValidateCheckpointCaptureRetirementObservation(in, o); err != nil {
				t.Fatalf("terminal replay: %v", err)
			}
			o.RetiredAt = o.RetiredAt.Add(time.Microsecond)
			if err := state.ValidateCheckpointCaptureRetirementObservation(in, o); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("changed terminal proof accepted: %v", err)
			}
		})
	}
}

func TestCheckpointCaptureRetirementRejectsIncompleteOrForeignEvidence(t *testing.T) {
	cases := []struct {
		name   string
		change func(*state.ProjectEnvironmentCloneCheckpointCaptureRetirement, *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation)
	}{
		{"source compensation pending", func(i *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, _ *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			i.Plan.SourceCompensation.State = "requested"
		}},
		{"source proof changed", func(i *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, _ *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			i.Plan.SourceCompensation.Observation.Sources[0].SourceUnchanged = false
		}},
		{"source digest changed", func(i *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, _ *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			i.Plan.SourceCompensation.ObservationSHA256 = state.CloneCheckpointSHA("foreign")
		}},
		{"dispatch not claimed", func(i *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, _ *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			i.State, i.RequestedAt = "planned", time.Time{}
		}},
		{"claim before reopening", func(i *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, _ *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			i.RequestedAt = i.Plan.SourceCompensation.Observation.CompensatedAt.Add(-time.Microsecond)
		}},
		{"foreign request", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.RequestID += "foreign"
		}},
		{"foreign plan", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.PlanSHA256 = state.CloneCheckpointSHA("foreign")
		}},
		{"foreign owner", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.OwnerToken += "foreign"
		}},
		{"foreign capture request", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.CaptureRequestID += "foreign"
		}},
		{"foreign capture plan", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.CapturePlanSHA256 = state.CloneCheckpointSHA("foreign")
		}},
		{"capture not fenced", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.CapturePermanentlyFenced = false
		}},
		{"missing source", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources = o.Sources[:1]
		}},
		{"duplicate source", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[1] = o.Sources[0]
		}},
		{"reordered sources", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0], o.Sources[1] = o.Sources[1], o.Sources[0]
		}},
		{"foreign source kind", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].Kind = "object_storage"
		}},
		{"foreign source", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].ID += "foreign"
		}},
		{"foreign physical source", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].SourceResourceID += "foreign"
		}},
		{"replacement capture", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].CaptureIdentity += "foreign"
		}},
		{"foreign artifact owner", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].OwnerToken += "foreign"
		}},
		{"retained artifacts", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].NoOwnedArtifacts = false
		}},
		{"live borrower", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].NoNativeBorrowers = false
		}},
		{"source changed", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].SourceUnchanged = false
		}},
		{"source access changed", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.Sources[0].SourceAccessRestored = false
		}},
		{"before claim", func(i *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.RetiredAt = i.RequestedAt.Add(-time.Microsecond)
		}},
		{"future proof", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.RetiredAt = time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		}},
		{"noncanonical time", func(_ *state.ProjectEnvironmentCloneCheckpointCaptureRetirement, o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
			o.RetiredAt = o.RetiredAt.Add(time.Nanosecond)
		}},
	}
	for _, captured := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.name+map[bool]string{false: "/unknown", true: "/captured"}[captured], func(t *testing.T) {
				in, o := captureRetirementFixture(captured, "")
				tc.change(&in, &o)
				if err := state.ValidateCheckpointCaptureRetirementObservation(in, o); !errors.Is(err, state.ErrConflict) {
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
	var replay state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan
	if err := json.Unmarshal(raw, &replay); err != nil {
		t.Fatal(err)
	}
	if state.ValidateCheckpointCaptureRetirementPlan(replay) != nil || state.CloneCheckpointSHA(replay) != state.CloneCheckpointSHA(in.Plan) {
		t.Fatal("saved plan changed")
	}
}
