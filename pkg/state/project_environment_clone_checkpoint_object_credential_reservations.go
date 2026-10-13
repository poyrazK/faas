package state

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Reservation fixes fresh identities for the whole authenticated matrix. It
// contains no keys and grants no native issuance or publication authority.
type ProjectEnvironmentCloneCheckpointObjectCredentialReservation struct {
	Inputs  ProjectEnvironmentCloneCheckpointObjectCredentialInputs
	Members []ProjectEnvironmentCloneCheckpointObjectCredentialReservationMember
	SHA256  string `json:"-"`
}
type ProjectEnvironmentCloneCheckpointObjectCredentialReservationMember struct {
	AppID, SourceBucketID, TargetBucketID, SourceCredentialID, TargetCredentialID string
}
type ProjectEnvironmentCloneCheckpointObjectCredentialReservationStore interface {
	ReserveProjectEnvironmentCloneCheckpointObjectCredentials(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCredentialReservation, error)
}

func newCheckpointObjectCredentialReservation(inputs ProjectEnvironmentCloneCheckpointObjectCredentialInputs) (ProjectEnvironmentCloneCheckpointObjectCredentialReservation, error) {
	zero := ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}
	if !validCloneObjectSHA256(inputs.SHA256) || inputs.SHA256 != cloneCheckpointSHA(inputs) {
		return zero, ErrConflict
	}
	// Own the complete graph, including access-grant definitions and secret slots.
	encoded, err := json.Marshal(inputs)
	if err != nil {
		return zero, err
	}
	out := ProjectEnvironmentCloneCheckpointObjectCredentialReservation{Members: []ProjectEnvironmentCloneCheckpointObjectCredentialReservationMember{}}
	if err = json.Unmarshal(encoded, &out.Inputs); err != nil {
		return zero, err
	}
	out.Inputs.SHA256 = inputs.SHA256
	for _, b := range out.Inputs.Buckets {
		for _, c := range b.Credentials {
			out.Members = append(out.Members, ProjectEnvironmentCloneCheckpointObjectCredentialReservationMember{AppID: b.AppID, SourceBucketID: b.Source.ID, TargetBucketID: b.Target.TargetBucketID, SourceCredentialID: c.SourceCredentialID, TargetCredentialID: uuid.NewString()})
		}
	}
	out.SHA256 = cloneCheckpointSHA(out)
	if err = validateCheckpointObjectCredentialReservation(out, inputs); err != nil {
		return zero, err
	}
	return out, nil
}

func validateCheckpointObjectCredentialReservation(in ProjectEnvironmentCloneCheckpointObjectCredentialReservation, inputs ProjectEnvironmentCloneCheckpointObjectCredentialInputs) error {
	if !validCloneObjectSHA256(inputs.SHA256) || inputs.SHA256 != cloneCheckpointSHA(inputs) || !reflect.DeepEqual(in.Inputs, inputs) || in.SHA256 != cloneCheckpointSHA(in) {
		return ErrConflict
	}
	used := map[string]bool{}
	for _, b := range inputs.Buckets {
		used[b.Source.ID], used[b.Target.TargetBucketID] = true, true
		for _, c := range b.Credentials {
			used[c.SourceCredentialID] = true
		}
	}
	n := 0
	for _, b := range inputs.Buckets {
		for _, c := range b.Credentials {
			if n >= len(in.Members) {
				return ErrConflict
			}
			m := in.Members[n]
			id, err := uuid.Parse(m.TargetCredentialID)
			if err != nil || id == uuid.Nil || id.String() != m.TargetCredentialID || used[m.TargetCredentialID] || m.AppID != b.AppID || m.SourceBucketID != b.Source.ID || m.TargetBucketID != b.Target.TargetBucketID || m.SourceCredentialID != c.SourceCredentialID {
				return ErrConflict
			}
			used[m.TargetCredentialID] = true
			n++
		}
	}
	if n != len(in.Members) {
		return ErrConflict
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if len(encoded) > api.ObjectCheckpointVerificationPlanMaxBytes {
		return ErrQuotaExceeded
	}
	return nil
}
