package state

// adr: 435. Fresh approval is separate from immutable producer identity.

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

func standardNativeArtifactDeadline(capture InstanceApplicationStandardAdmission, evidence DeploymentRuntimeScanEvidence) (time.Time, error) {
	required, enforce, err := standardNativeArtifactRequirements(capture)
	if err != nil {
		return time.Time{}, err
	}
	if capture.ArtifactInputHash == "" {
		if required || evidence.Scan.ID != "" {
			return time.Time{}, ErrApplicationStandardRuntimeStale
		}
		return capture.ExceptionExpiresAt, nil // Compatibility only; no producer approval is invented.
	}
	if evidence.Scan.ID == "" || evidence.Scan.Input.Status != "complete" || evidence.Scan.Input.Facts.InputHash != capture.ArtifactInputHash || evidence.CheckedAt.IsZero() || !evidence.ExpiresAt.After(evidence.CheckedAt) || evidence.ExpiresAt.After(evidence.Scan.ExpiresAt) {
		return time.Time{}, ErrApplicationStandardRuntimeStale
	}
	if enforce {
		for _, view := range evidence.Scan.Input.Reports {
			if standardNativeScanBlocks(view.Report) {
				return time.Time{}, ErrApplicationStandardRuntimeStale
			}
		}
	}
	deadline := evidence.ExpiresAt
	if !capture.ExceptionExpiresAt.IsZero() && capture.ExceptionExpiresAt.Before(deadline) {
		deadline = capture.ExceptionExpiresAt
	}
	return deadline, nil
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
