package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"reflect"
	"testing"
)

func TestCheckpointObjectPublicationPlanCompleteAndOwned(t *testing.T) {
	r, values := state.CheckpointObjectValueFixture(t)
	plan, err := state.CheckpointObjectPublicationPlan(r, values)
	if err != nil || len(plan.Buckets) != 2 || plan.SHA256 != state.CloneCheckpointSHA(plan) || plan.ValuesSHA256 != values.SHA256 || plan.InputsSHA256 != r.Inputs.SHA256 || plan.ReservationSHA256 != r.SHA256 {
		t.Fatal("complete authenticated plan", err)
	}
	n := 0
	for i, b := range plan.Buckets {
		if b.TargetBucketID != r.Inputs.Buckets[i].Target.TargetBucketID || !reflect.DeepEqual(b.AccessGrants, r.Inputs.Buckets[i].Source.AccessGrants) || len(b.Preparations) != 2 {
			t.Fatal("bucket or grant matrix lost")
		}
		for _, p := range b.Preparations {
			if !reflect.DeepEqual(p, values.Preparations[n]) {
				t.Fatal("sealed material changed")
			}
			n++
		}
	}
	for _, b := range plan.Buckets {
		for _, p := range b.Preparations {
			p.Credential.SecretSealed[0] ^= 1
			if len(p.Secrets) > 0 {
				p.Secrets[0].Ciphertext[0] ^= 1
				p.Secrets[0].Key = "changed"
			}
		}
		b.AccessGrants[0].Permission = "write"
	}
	if err = state.ValidateCheckpointObjectCredentialValues(values, r); err != nil {
		t.Fatal("returned plan aliases sealed values", err)
	}
	again, err := state.CheckpointObjectPublicationPlan(r, values)
	if err != nil || again.SHA256 != plan.SHA256 {
		t.Fatal("plan mutation changed original inputs", err)
	}
}

func TestCheckpointObjectPublicationPlanEmptyAndGrantOnly(t *testing.T) {
	for _, empty := range []bool{false, true} {
		r, _ := state.CheckpointObjectValueFixture(t)
		if empty {
			r.Inputs.Buckets = nil
		} else {
			for i := range r.Inputs.Buckets {
				r.Inputs.Buckets[i].Credentials = nil
			}
		}
		r.Inputs.SHA256 = state.CloneCheckpointSHA(r.Inputs)
		r.Members = nil
		r.SHA256 = state.CloneCheckpointSHA(r)
		values := state.ProjectEnvironmentCloneCheckpointObjectCredentialValues{OperationID: r.Inputs.OperationID, ReservationSHA256: r.SHA256}
		values.SHA256 = state.CloneCheckpointSHA(values)
		plan, err := state.CheckpointObjectPublicationPlan(r, values)
		if err != nil || len(plan.Buckets) != len(r.Inputs.Buckets) {
			t.Fatal("empty/grant-only plan", err)
		}
		for _, b := range plan.Buckets {
			if len(b.AccessGrants) != 1 || len(b.Preparations) != 0 {
				t.Fatal("grant-only bucket omitted")
			}
		}
		values.ReservationSHA256 = "changed"
		values.SHA256 = state.CloneCheckpointSHA(values)
		if out, err := state.CheckpointObjectPublicationPlan(r, values); err == nil || out.SHA256 != "" {
			t.Fatal("unbound sealed matrix returned publication intent")
		}
	}
}
