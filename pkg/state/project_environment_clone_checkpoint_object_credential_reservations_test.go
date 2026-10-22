package state_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/state"
	"reflect"
	"testing"
)

func TestCheckpointObjectCredentialReservationCompleteAndIsolated(t *testing.T) {
	op, views, proofs := state.CheckpointObjectCredentialInputFixture(t)
	inputs, err := state.CheckpointObjectCredentialInputs(op, views, proofs)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := state.NewCheckpointObjectCredentialReservation(inputs)
	if err != nil || len(plan.Members) != 4 {
		t.Fatal("complete reservation", err)
	}
	if err = state.ValidateCheckpointObjectCredentialReservation(plan, inputs); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var recovered state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation
	if err = json.Unmarshal(encoded, &recovered); err != nil {
		t.Fatal(err)
	}
	recovered.Inputs.SHA256 = state.CloneCheckpointSHA(recovered.Inputs)
	recovered.SHA256 = plan.SHA256
	if !reflect.DeepEqual(plan, recovered) {
		t.Fatal("journal round trip changed identities")
	}
	plan.Inputs.Buckets[0].Source.AccessGrants[0].Permission = "write"
	plan.Inputs.Buckets[0].Credentials[0].SecretKeys[0] = "CHANGED"
	if !reflect.DeepEqual(inputs, recovered.Inputs) {
		t.Fatal("reservation aliases input catalogue")
	}
}
func TestCheckpointObjectCredentialReservationRejectsTampering(t *testing.T) {
	for name, edit := range map[string]func(*state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation){
		"missing": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members = p.Members[:3]
		},
		"extra": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members = append(p.Members, p.Members[0])
		},
		"duplicate": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[1].TargetCredentialID = p.Members[0].TargetCredentialID
		},
		"source reuse": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].TargetCredentialID = p.Members[0].SourceCredentialID
		},
		"empty identity": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].TargetCredentialID = "00000000-0000-0000-0000-000000000000"
		},
		"app": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].AppID = "other"
		},
		"source bucket": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].SourceBucketID = "other"
		},
		"target bucket": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].TargetBucketID = "other"
		},
		"source credential": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].SourceCredentialID = "other"
		},
		"order": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0], p.Members[1] = p.Members[1], p.Members[0]
		},
		"grant": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Inputs.Buckets[0].Source.AccessGrants = nil
		},
		"permission": func(p *state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Inputs.Buckets[0].Credentials[0].Permission = "write"
		},
	} {
		t.Run(name, func(t *testing.T) {
			op, v, proofs := state.CheckpointObjectCredentialInputFixture(t)
			inputs, err := state.CheckpointObjectCredentialInputs(op, v, proofs)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := state.NewCheckpointObjectCredentialReservation(inputs)
			if err != nil {
				t.Fatal(err)
			}
			edit(&plan)
			plan.Inputs.SHA256 = state.CloneCheckpointSHA(plan.Inputs)
			plan.SHA256 = state.CloneCheckpointSHA(plan)
			if err = state.ValidateCheckpointObjectCredentialReservation(plan, inputs); err == nil {
				t.Fatal("tampered reservation accepted")
			}
		})
	}
}
func TestCheckpointObjectCredentialReservationEmptyAndUnauthenticated(t *testing.T) {
	op, _, _ := state.CheckpointObjectCredentialInputFixture(t)
	inputs, err := state.CheckpointObjectCredentialInputs(op, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := state.NewCheckpointObjectCredentialReservation(inputs)
	if err != nil || len(plan.Members) != 0 {
		t.Fatal("empty graph", err)
	}
	inputs.SHA256 = ""
	if _, err = state.NewCheckpointObjectCredentialReservation(inputs); err == nil {
		t.Fatal("unauthenticated inputs accepted")
	}
	plan.SHA256 = ""
	if err = state.ValidateCheckpointObjectCredentialReservation(plan, plan.Inputs); err == nil {
		t.Fatal("missing journal digest accepted")
	}
}
