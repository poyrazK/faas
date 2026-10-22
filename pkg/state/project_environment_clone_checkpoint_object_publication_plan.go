package state

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Private publication intent, derived from authenticated frozen bindings and
// sealed values. It confers no gateway rights or native publication authority.
type ProjectEnvironmentCloneCheckpointObjectPublicationPlan struct {
	OperationID, AccountID, ProjectID, TargetEnvironment string
	InputsSHA256, ReservationSHA256, ValuesSHA256        string
	Buckets                                              []ProjectEnvironmentCloneCheckpointObjectPublicationBucket
	SHA256                                               string `json:"-"`
}

type ProjectEnvironmentCloneCheckpointObjectPublicationBucket struct {
	AppID, SourceBucketID, TargetBucketID string
	Preparations                          []ProjectEnvironmentCloneObjectCredentialPreparation
	AccessGrants                          []ProjectEnvironmentCloneObjectAccessGrant
}

type ProjectEnvironmentCloneCheckpointObjectPublicationPlanStore interface {
	ProjectEnvironmentCloneCheckpointObjectPublicationPlan(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectPublicationPlan, error)
}

func checkpointObjectPublicationPlan(r ProjectEnvironmentCloneCheckpointObjectCredentialReservation, values ProjectEnvironmentCloneCheckpointObjectCredentialValues) (ProjectEnvironmentCloneCheckpointObjectPublicationPlan, error) {
	zero := ProjectEnvironmentCloneCheckpointObjectPublicationPlan{}
	if err := validateCheckpointObjectCredentialValues(values, r); err != nil {
		return zero, err
	}
	in := r.Inputs
	out := ProjectEnvironmentCloneCheckpointObjectPublicationPlan{OperationID: in.OperationID, AccountID: in.AccountID, ProjectID: in.ProjectID, TargetEnvironment: in.TargetEnvironment, InputsSHA256: in.SHA256, ReservationSHA256: r.SHA256, ValuesSHA256: values.SHA256, Buckets: []ProjectEnvironmentCloneCheckpointObjectPublicationBucket{}}
	n := 0
	for _, b := range in.Buckets {
		member := ProjectEnvironmentCloneCheckpointObjectPublicationBucket{AppID: b.AppID, SourceBucketID: b.Source.ID, TargetBucketID: b.Target.TargetBucketID, Preparations: []ProjectEnvironmentCloneObjectCredentialPreparation{}, AccessGrants: append([]ProjectEnvironmentCloneObjectAccessGrant{}, b.Source.AccessGrants...)}
		for range b.Credentials {
			member.Preparations = append(member.Preparations, values.Preparations[n])
			n++
		}
		sort.Slice(member.AccessGrants, func(i, j int) bool { return member.AccessGrants[i].APIKeyID < member.AccessGrants[j].APIKeyID })
		out.Buckets = append(out.Buckets, member)
	}
	// Own ciphertext and all nested slices; callers cannot mutate the sealed
	// inputs by changing the returned plan.
	encoded, err := json.Marshal(out)
	if err != nil {
		return zero, err
	}
	if len(encoded) > api.ObjectCheckpointVerificationPlanMaxBytes {
		return zero, ErrQuotaExceeded
	}
	out = ProjectEnvironmentCloneCheckpointObjectPublicationPlan{}
	if err = json.Unmarshal(encoded, &out); err != nil {
		return zero, err
	}
	out.SHA256 = cloneCheckpointSHA(out)
	return out, nil
}
