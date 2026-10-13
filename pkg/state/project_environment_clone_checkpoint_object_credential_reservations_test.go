package state

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCheckpointObjectCredentialReservationCompleteAndIsolated(t *testing.T) {
	op, views, proofs := checkpointObjectCredentialInputFixture(t)
	inputs, err := checkpointObjectCredentialInputs(op, views, proofs)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := newCheckpointObjectCredentialReservation(inputs)
	if err != nil || len(plan.Members) != 4 {
		t.Fatal("complete reservation", err)
	}
	if err = validateCheckpointObjectCredentialReservation(plan, inputs); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var recovered ProjectEnvironmentCloneCheckpointObjectCredentialReservation
	if err = json.Unmarshal(encoded, &recovered); err != nil {
		t.Fatal(err)
	}
	recovered.Inputs.SHA256 = cloneCheckpointSHA(recovered.Inputs)
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
	for name, edit := range map[string]func(*ProjectEnvironmentCloneCheckpointObjectCredentialReservation){
		"missing": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) { p.Members = p.Members[:3] },
		"extra": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members = append(p.Members, p.Members[0])
		},
		"duplicate": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[1].TargetCredentialID = p.Members[0].TargetCredentialID
		},
		"source reuse": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].TargetCredentialID = p.Members[0].SourceCredentialID
		},
		"empty identity": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].TargetCredentialID = "00000000-0000-0000-0000-000000000000"
		},
		"app": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) { p.Members[0].AppID = "other" },
		"source bucket": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].SourceBucketID = "other"
		},
		"target bucket": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].TargetBucketID = "other"
		},
		"source credential": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0].SourceCredentialID = "other"
		},
		"order": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Members[0], p.Members[1] = p.Members[1], p.Members[0]
		},
		"grant": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Inputs.Buckets[0].Source.AccessGrants = nil
		},
		"permission": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialReservation) {
			p.Inputs.Buckets[0].Credentials[0].Permission = "write"
		},
	} {
		t.Run(name, func(t *testing.T) {
			op, v, proofs := checkpointObjectCredentialInputFixture(t)
			inputs, err := checkpointObjectCredentialInputs(op, v, proofs)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := newCheckpointObjectCredentialReservation(inputs)
			if err != nil {
				t.Fatal(err)
			}
			edit(&plan)
			plan.Inputs.SHA256 = cloneCheckpointSHA(plan.Inputs)
			plan.SHA256 = cloneCheckpointSHA(plan)
			if err = validateCheckpointObjectCredentialReservation(plan, inputs); err == nil {
				t.Fatal("tampered reservation accepted")
			}
		})
	}
}
func TestCheckpointObjectCredentialReservationEmptyAndUnauthenticated(t *testing.T) {
	op, _, _ := checkpointObjectCredentialInputFixture(t)
	inputs, err := checkpointObjectCredentialInputs(op, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := newCheckpointObjectCredentialReservation(inputs)
	if err != nil || len(plan.Members) != 0 {
		t.Fatal("empty graph", err)
	}
	inputs.SHA256 = ""
	if _, err = newCheckpointObjectCredentialReservation(inputs); err == nil {
		t.Fatal("unauthenticated inputs accepted")
	}
	plan.SHA256 = ""
	if err = validateCheckpointObjectCredentialReservation(plan, plan.Inputs); err == nil {
		t.Fatal("missing journal digest accepted")
	}
}
