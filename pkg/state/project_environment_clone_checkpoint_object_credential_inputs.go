package state

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Private issuance inputs from the entire frozen object binding catalogue. These
// contain definitions and secret key names, never source credential material.
// They are not a native grant claim or resource/publication authority.
type ProjectEnvironmentCloneCheckpointObjectCredentialInputs struct {
	OperationID, AccountID, ProjectID, TargetEnvironment, SourceRevisionHash string
	Buckets                                                                  []ProjectEnvironmentCloneCheckpointObjectCredentialBucket
	SHA256                                                                   string `json:"-"`
}
type ProjectEnvironmentCloneCheckpointObjectCredentialBucket struct {
	AppID, SourceScope, SourceBindingsSHA256                                     string
	Source                                                                       ProjectEnvironmentCloneObjectBucket
	Target                                                                       ProjectEnvironmentCloneCheckpointObjectTargetPlan
	VerificationPlanSHA256, VerificationObservationSHA256, CopyObservationSHA256 string
	Credentials                                                                  []ProjectEnvironmentCloneCheckpointObjectCredentialSlot
}
type ProjectEnvironmentCloneCheckpointObjectCredentialSlot struct {
	SourceCredentialID, Label, Permission, ManagedAppID, TargetScope, ManagedPrefix string
	SecretKeys                                                                      []string
}
type ProjectEnvironmentCloneCheckpointObjectCredentialInputStore interface {
	ProjectEnvironmentCloneCheckpointObjectCredentialInputs(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCredentialInputs, error)
}
type checkpointObjectPlacementKey struct{ BackendID, BackendFingerprint, PhysicalName string }

func checkpointObjectCredentialInputs(op ProjectEnvironmentCloneOperation, views []ProjectEnvironmentCloneBindings, proofs []ProjectEnvironmentCloneCheckpointObjectVerification) (ProjectEnvironmentCloneCheckpointObjectCredentialInputs, error) {
	zero := ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}
	if op.ID == "" || op.AccountID == "" || op.ProjectID == "" || op.TargetEnvironment == "" || !validCloneObjectSHA256(op.SourceRevisionHash) {
		return zero, ErrConflict
	}
	bySource := map[string]ProjectEnvironmentCloneCheckpointObjectVerification{}
	for _, p := range proofs {
		t := p.Plan.Target
		if p.State != "verified" || p.RequestedAt.IsZero() || !validCloneObjectSHA256(p.ObservationSHA256) || !validCloneObjectSHA256(p.Plan.CopySHA256) || !validCloneObjectSHA256(p.Plan.CreationSHA256) || !validCloneObjectSHA256(t.ManifestSHA256) || t.OperationID != op.ID || t.AccountID != op.AccountID || t.SourceBucketID == "" || t.TargetBucketID == "" || t.PhysicalName == "" || t.Region == "" || t.BackendID == "" || !cloneBindingFingerprintRE.MatchString(t.BackendFingerprint) || t.CheckpointID == "" {
			return zero, ErrConflict
		}
		if _, duplicate := bySource[t.SourceBucketID]; duplicate {
			return zero, ErrConflict
		}
		bySource[t.SourceBucketID] = p
	}
	out := ProjectEnvironmentCloneCheckpointObjectCredentialInputs{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID, TargetEnvironment: op.TargetEnvironment, SourceRevisionHash: op.SourceRevisionHash, Buckets: []ProjectEnvironmentCloneCheckpointObjectCredentialBucket{}}
	apps, sources, credentials := map[string]bool{}, map[string]bool{}, map[string]bool{}
	sourceOwners := map[checkpointObjectPlacementKey]bool{}
	for _, v := range views {
		hash, err := cloneBindingDefinitionsHash(v.ProjectEnvironmentCloneBindingDefinitions)
		if err != nil || hash != v.Hash || v.AppID == "" || v.SourceScope == "" || apps[v.AppID] {
			return zero, ErrConflict
		}
		apps[v.AppID] = true
		for _, b := range v.Buckets {
			if b.ID == "" || sources[b.ID] || !cloneBindingNameRE.MatchString(b.Name) || b.Region == "" || b.BackendID == "" || !cloneBindingFingerprintRE.MatchString(b.BackendFingerprint) || b.PhysicalName == "" || !validCloneBucketPublicPolicy(b.PublicRead, b.ServeAt) {
				return zero, ErrConflict
			}
			sources[b.ID] = true
			sourceOwner := checkpointObjectPlacementKey{b.BackendID, b.BackendFingerprint, b.PhysicalName}
			if sourceOwners[sourceOwner] {
				return zero, ErrConflict
			}
			sourceOwners[sourceOwner] = true
			p, ok := bySource[b.ID]
			if !ok || p.Plan.Target.AppID != v.AppID || p.Plan.SourceResourceID != b.PhysicalName {
				return zero, ErrConflict
			}
			edge := ProjectEnvironmentCloneCheckpointObjectCredentialBucket{AppID: v.AppID, SourceScope: v.SourceScope, SourceBindingsSHA256: v.Hash, Source: b, Target: p.Plan.Target, VerificationPlanSHA256: cloneCheckpointSHA(p.Plan), VerificationObservationSHA256: p.ObservationSHA256, CopyObservationSHA256: p.Plan.CopySHA256, Credentials: []ProjectEnvironmentCloneCheckpointObjectCredentialSlot{}}
			edge.Source.Credentials = append([]ProjectEnvironmentCloneObjectCredential{}, b.Credentials...)
			edge.Source.AccessGrants = append([]ProjectEnvironmentCloneObjectAccessGrant{}, b.AccessGrants...)
			slots := map[string]bool{}
			for _, c := range b.Credentials {
				if c.ID == "" || credentials[c.ID] || len(c.Label) < 1 || len(c.Label) > 64 || !validObjectBucketPermission(c.Permission) {
					return zero, ErrConflict
				}
				credentials[c.ID] = true
				slot := ProjectEnvironmentCloneCheckpointObjectCredentialSlot{SourceCredentialID: c.ID, Label: c.Label, Permission: c.Permission, ManagedAppID: c.ManagedAppID, ManagedPrefix: c.ManagedPrefix, SecretKeys: []string{}}
				if c.ManagedAppID == "" {
					if c.ManagedScope != "" || c.ManagedPrefix != "" {
						return zero, ErrConflict
					}
				} else {
					if c.ManagedAppID != v.AppID || c.ManagedScope != v.SourceScope || !cloneBindingPrefixRE.MatchString(c.ManagedPrefix) {
						return zero, ErrConflict
					}
					slot.TargetScope = op.TargetEnvironment
					for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
						key := c.ManagedPrefix + suffix
						if slots[key] {
							return zero, ErrConflict
						}
						slots[key] = true
						slot.SecretKeys = append(slot.SecretKeys, key)
					}
					sort.Strings(slot.SecretKeys)
				}
				edge.Credentials = append(edge.Credentials, slot)
			}
			grants := map[string]bool{}
			for _, g := range b.AccessGrants {
				if g.APIKeyID == "" || grants[g.APIKeyID] || !validObjectBucketPermission(g.Permission) {
					return zero, ErrConflict
				}
				grants[g.APIKeyID] = true
			}
			sort.Slice(edge.Credentials, func(i, j int) bool {
				return edge.Credentials[i].SourceCredentialID < edge.Credentials[j].SourceCredentialID
			})
			out.Buckets = append(out.Buckets, edge)
		}
	}
	if len(sources) != len(bySource) {
		return zero, ErrConflict
	}
	targets := map[string]bool{}
	targetOwners := map[checkpointObjectPlacementKey]bool{}
	appSlots := map[string]bool{}
	for _, b := range out.Buckets {
		t := b.Target
		k := checkpointObjectPlacementKey{t.BackendID, t.BackendFingerprint, t.PhysicalName}
		if sources[t.TargetBucketID] || targets[t.TargetBucketID] || sourceOwners[k] || targetOwners[k] {
			return zero, ErrConflict
		}
		targets[t.TargetBucketID] = true
		targetOwners[k] = true
		for _, c := range b.Credentials {
			for _, key := range c.SecretKeys {
				slot := b.AppID + "/" + key
				if appSlots[slot] {
					return zero, ErrConflict
				}
				appSlots[slot] = true
			}
		}
	}
	sort.Slice(out.Buckets, func(i, j int) bool {
		if out.Buckets[i].AppID != out.Buckets[j].AppID {
			return out.Buckets[i].AppID < out.Buckets[j].AppID
		}
		return out.Buckets[i].Source.ID < out.Buckets[j].Source.ID
	})
	encoded, err := json.Marshal(out)
	if err != nil {
		return zero, err
	}
	if len(encoded) > api.ObjectCheckpointVerificationPlanMaxBytes {
		return zero, ErrQuotaExceeded
	}
	out.SHA256 = cloneCheckpointSHA(out)
	return out, nil
}
