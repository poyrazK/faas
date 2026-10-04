package state

// adr: 435. A preview binds authenticated producer/scan history; runtime owners
// still verify consumed bytes and issue their own expiring admission grants.

import (
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type standardReviewArtifactSecurity struct {
	InputHash      string                     `json:"input_hash"`
	ScanID         string                     `json:"scan_id"`
	ScanHash       string                     `json:"scan_hash"`
	ExpiresAt      time.Time                  `json:"expires_at"`
	Publishers     []RuntimePublisherApproval `json:"publishers"`
	EnforceAllowed bool                       `json:"enforce_allowed"`
}

func prepareStandardReviewArtifactSecurity(inputs DeploymentRuntimeProducerInputs, scan DeploymentRuntimeScan) (*standardReviewArtifactSecurity, error) {
	evidence, err := finishRuntimeScanEvidence(scan, inputs)
	if err != nil {
		return nil, err
	}
	workloads := map[string]string{}
	for _, artifact := range inputs.Artifacts {
		if artifact.Kind != "base-image" {
			workloads[artifact.WorkloadName] = artifact.Kind
		}
	}
	if len(workloads) != len(inputs.Publishers) {
		return nil, ErrApplicationStandardRuntimeStale
	}
	for _, publisher := range inputs.Publishers {
		kind, exists := workloads[publisher.WorkloadName]
		source := kind == "source-app-layer" || kind == "function-layer"
		if !exists || !validStandardResourceRead(publisher.ID, publisher.ID) || len(publisher.InputHash) != 64 || len(publisher.PublisherKeySHA256) != 64 ||
			source && publisher.Kind != "build-export" || !source && publisher.Kind != "registry-image" {
			return nil, ErrApplicationStandardRuntimeStale
		}
		delete(workloads, publisher.WorkloadName)
	}
	allowed := true
	for _, view := range scan.Input.Reports {
		allowed = allowed && !standardNativeScanBlocks(view.Report)
	}
	return &standardReviewArtifactSecurity{InputHash: inputs.InputHash, ScanID: scan.ID, ScanHash: scan.InputHash,
		ExpiresAt: evidence.ExpiresAt, Publishers: slices.Clone(inputs.Publishers), EnforceAllowed: allowed}, nil
}

func standardReviewArtifactEvidenceUnavailable(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrApplicationStandardRuntimeStale) ||
		errors.Is(err, ErrApplicationStandardRuntimeBusy) || errors.Is(err, ErrApplicationStandardReviewBusy) ||
		errors.Is(err, ErrDeploymentArtifactScanEvidenceAbsent) || errors.Is(err, buildpublisher.ErrInvalid) ||
		errors.Is(err, imagepublisher.ErrSignatureInvalid)
}

func standardReviewSecurityChange(reviewed ApplicationStandardReviewedApp) bool {
	var signed bool
	var posture api.AppSecurityPolicy
	_ = json.Unmarshal(reviewed.Effective.Values[appstandards.RequireSigned], &signed)
	_ = json.Unmarshal(reviewed.Effective.Values[appstandards.SecurityPolicy], &posture)
	return (signed || posture.RequiresSignedImage()) && (slices.Contains(reviewed.ChangedFields, appstandards.RequireSigned) ||
		slices.Contains(reviewed.ChangedFields, appstandards.TrustedPublishers)) ||
		posture.RequiresSignedImage() && slices.Contains(reviewed.ChangedFields, appstandards.SecurityPolicy)
}

func standardReviewArtifactBlockers(resources []standardReviewResource, app standardReviewAppSnapshot, reviewed ApplicationStandardReviewedApp, now time.Time) []ApplicationStandardReviewBlocker {
	if !standardReviewSecurityChange(reviewed) {
		return nil
	}
	approved := standardReviewPublisherFingerprints(resources, app, standardReviewStrings(reviewed.Effective.Values[appstandards.TrustedPublishers]))
	for _, artifact := range app.Artifacts {
		field, code := appstandards.TrustedPublishers, "current_artifact_verification_required"
		if security := artifact.Security; security != nil && security.ExpiresAt.After(now) {
			code = ""
			for _, publisher := range security.Publishers {
				if !approved[publisher.PublisherKeySHA256] {
					code = "artifact_publisher_not_approved"
				}
			}
			if string(reviewed.Effective.Values[appstandards.SecurityPolicy]) == `"enforce"` && !security.EnforceAllowed {
				field, code = appstandards.SecurityPolicy, "artifact_security_policy_conflict"
			}
		}
		if code != "" {
			return []ApplicationStandardReviewBlocker{{AppID: app.AppID, Field: field, Code: code}}
		}
	}
	return nil
}

func standardReviewPublisherFingerprints(resources []standardReviewResource, app standardReviewAppSnapshot, selected []string) map[string]bool {
	byID := map[string]string{}
	for _, resource := range resources {
		byID[resource.ID] = resource.Fingerprint
	}
	for _, signer := range app.Signers {
		byID[standardLegacySignerID(app.AppID, signer.Name, signer.Fingerprint)] = signer.Fingerprint
	}
	for _, backup := range app.ArchivedResources {
		if backup.Field == appstandards.TrustedPublishers {
			byID[backup.ID] = backup.Fingerprint
		}
	}
	result := map[string]bool{}
	for _, id := range selected {
		if fingerprint := byID[id]; fingerprint != "" {
			result[fingerprint] = true
		}
	}
	return result
}

func standardReviewArtifactExpiry(expires time.Time, app standardReviewAppSnapshot, reviewed ApplicationStandardReviewedApp) time.Time {
	if standardReviewSecurityChange(reviewed) {
		for _, artifact := range app.Artifacts {
			if security := artifact.Security; security != nil && security.ExpiresAt.Before(expires) {
				expires = security.ExpiresAt
			}
		}
	}
	return expires
}

func normalizeStandardReviewArchivedPublishers(snapshot *standardReviewSnapshot) {
	for i := range snapshot.Applications {
		app := &snapshot.Applications[i]
		for j := range app.ArchivedResources {
			backup := &app.ArchivedResources[j]
			body := backup.Body
			backup.Body = nil
			if backup.Field != appstandards.TrustedPublishers {
				continue
			}
			var signer AppTrustedSigner
			if json.Unmarshal(body, &signer) != nil {
				continue
			}
			raw, _ := json.Marshal(signer)
			fingerprint := standardReviewBytesDigest(signer.CosignPublicKey)
			if standardReviewBytesDigest(raw) == backup.ConfigHash && sameStandardUUID(signer.AccountID, app.AccountID) && sameStandardUUID(signer.AppID, app.AppID) &&
				standardLegacySignerID(app.AppID, signer.SignerName, fingerprint) == backup.ID {
				backup.Fingerprint = fingerprint
			}
		}
	}
}
