package state

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"

	"github.com/onebox-faas/faas/pkg/api"
)

// Private sealed gateway material. No credential or app-secret rows are created
// by this journal, so a desired active status confers no usable gateway rights.
type ProjectEnvironmentCloneCheckpointObjectCredentialValues struct {
	OperationID, ReservationSHA256, Endpoint, Region string
	Preparations                                     []ProjectEnvironmentCloneObjectCredentialPreparation
	SHA256                                           string `json:"-"`
}
type ProjectEnvironmentCloneCheckpointObjectCredentialValueStore interface {
	ProjectEnvironmentCloneCheckpointObjectCredentialValues(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCredentialValues, error)
	PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointObjectCredentialValues, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCredentialValues, error)
}

var checkpointObjectAccessKeyRE = regexp.MustCompile(`^GRGA[A-Z2-7]{16}$`)
var checkpointObjectPublicRegionRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func ProjectEnvironmentCloneCheckpointObjectCredentialValuesSHA256(in ProjectEnvironmentCloneCheckpointObjectCredentialValues) string {
	return cloneCheckpointSHA(in)
}

func validateCheckpointObjectCredentialValues(in ProjectEnvironmentCloneCheckpointObjectCredentialValues, r ProjectEnvironmentCloneCheckpointObjectCredentialReservation) error {
	if err := validateCheckpointObjectCredentialReservation(r, r.Inputs); err != nil {
		return err
	}
	if in.OperationID != r.Inputs.OperationID || in.ReservationSHA256 != r.SHA256 || in.SHA256 != cloneCheckpointSHA(in) || len(in.Preparations) != len(r.Members) {
		return ErrConflict
	}
	if len(r.Members) == 0 {
		if in.Endpoint != "" || in.Region != "" {
			return ErrConflict
		}
		return nil
	}
	u, err := url.Parse(in.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || !checkpointObjectPublicRegionRE.MatchString(in.Region) {
		return ErrConflict
	}
	n := 0
	accessKeys := map[string]bool{}
	for _, b := range r.Inputs.Buckets {
		for _, slot := range b.Credentials {
			p := in.Preparations[n]
			m := r.Members[n]
			c := p.Credential
			n++
			if p.OperationID != in.OperationID || p.AppID != b.AppID || p.SourceBucketID != b.Source.ID || p.SourceCredentialID != slot.SourceCredentialID || c.ID != m.TargetCredentialID || c.AccountID != r.Inputs.AccountID || c.BucketID != b.Target.TargetBucketID || c.Label != slot.Label || c.Permission != slot.Permission || c.ManagedAppID != slot.ManagedAppID || c.ManagedScope != slot.TargetScope || c.ManagedPrefix != slot.ManagedPrefix || !validObjectS3Credential(c) || !checkpointObjectAccessKeyRE.MatchString(c.AccessKeyID) || accessKeys[c.AccessKeyID] || !c.CreatedAt.IsZero() || c.LastUsedAt != nil || c.RevokedAt != nil || c.RotationParentID != "" || c.RotationWakeID != "" || c.RotationStampedAt != nil {
				return ErrConflict
			}
			accessKeys[c.AccessKeyID] = true
			if c.ManagedAppID == "" {
				if len(p.Secrets) != 0 {
					return ErrConflict
				}
				continue
			}
			if len(slot.SecretKeys) != len(p.Secrets) || !validObjectS3ComputeBindingCreateRequest(ObjectS3ComputeBindingCreateRequest{Credential: c, Secrets: p.Secrets, MaxCredentialsPerBucket: api.MaxObjectS3CredentialsPerBucket, MaxSecretsPerApp: len(p.Secrets)}) {
				return ErrConflict
			}
			for i, s := range p.Secrets {
				if s.Key != slot.SecretKeys[i] || s.ManagedPostgresBindingID != "" || s.ManagedCredentialRef != "" || s.ManagedCredentialGeneration != 0 || s.SecretClass != SecretClassPersistent || s.SecretVersion != 1 || len(s.ValueHash) > 16 {
					return ErrConflict
				}
			}
		}
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
