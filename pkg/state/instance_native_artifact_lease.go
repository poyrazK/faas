package state

// adr: 431. Fresh approval is separate from immutable producer identity.

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func standardNativeArtifactRequirements(capture InstanceApplicationStandardAdmission) (bool, bool, error) {
	var input struct {
		Settings struct {
			Signed bool                  `json:"require_signed"`
			Policy api.AppSecurityPolicy `json:"security_policy"`
		} `json:"settings"`
	}
	if json.Unmarshal(capture.inputs, &input) != nil || !input.Settings.Policy.Valid() {
		return false, false, ErrApplicationStandardRuntimeStale
	}
	enforce := input.Settings.Policy == api.AppSecurityPolicyEnforce
	return input.Settings.Signed || enforce, enforce, nil
}

func standardNativeArtifactDeadline(capture InstanceApplicationStandardAdmission, evidence DeploymentArtifactScanEvidence) (time.Time, error) {
	required, enforce, err := standardNativeArtifactRequirements(capture)
	if err != nil {
		return time.Time{}, err
	}
	if capture.ArtifactInputHash == "" {
		if required || len(evidence.Components) != 0 {
			return time.Time{}, ErrApplicationStandardRuntimeStale
		}
		return time.Time{}, nil // Compatibility only; no producer approval is invented.
	}
	inputs, err := deploymentRuntimeArtifactInputs(evidence)
	if err != nil || inputs.InputHash != capture.ArtifactInputHash {
		return time.Time{}, ErrApplicationStandardRuntimeStale
	}
	if enforce {
		for _, scan := range evidence.Components {
			if standardNativeScanBlocks(scan.Result) {
				return time.Time{}, ErrApplicationStandardRuntimeStale
			}
		}
		for _, scan := range evidence.Bases {
			if standardNativeScanBlocks(scan.Result) {
				return time.Time{}, ErrApplicationStandardRuntimeStale
			}
		}
	}
	return inputs.ExpiresAt, nil
}

func standardNativeScanBlocks(report api.ScanResult) bool {
	c := report.SeverityCounts
	return c.Critical > 0 || c.High > 0 || c.Unknown > 0
}

func standardNativeGrantExpiry(now, artifactDeadline time.Time) (time.Time, error) {
	expires := now.Add(api.ApplicationStandardRuntimeAdmissionTTL)
	if !artifactDeadline.IsZero() && artifactDeadline.Before(expires) {
		expires = artifactDeadline
	}
	if !expires.After(now) {
		return time.Time{}, ErrApplicationStandardRuntimeStale
	}
	return expires, nil
}

func standardNativeGrantWithinArtifactLease(expires int64, deadline time.Time) bool {
	return deadline.IsZero() || expires <= deadline.UnixNano()
}
