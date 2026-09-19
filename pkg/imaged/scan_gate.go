package imaged

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// errSecurityScanBlocked is lifted to the stable deployment error code by
// markDeployFailed. Keeping the sentinel in imaged lets scan failures carry
// their detailed reason without coupling the scanner to the persistence API.
var errSecurityScanBlocked = errors.New("imaged: verified security scan blocked deployment")

const (
	verifiedScanMaxAge   = 5 * time.Minute
	verifiedScannerDBAge = 30 * 24 * time.Hour
)

// verifiedScanFailure preserves the historical best-effort scan posture for
// off/warn apps while making every scan uncertainty terminal for enforce apps.
func verifiedScanFailure(policy api.AppSecurityPolicy, detail string) error {
	if policy != api.AppSecurityPolicyEnforce {
		return nil
	}
	return fmt.Errorf("%w: %s", errSecurityScanBlocked, detail)
}

// checkVerifiedScanGate is the promotion decision for security_policy=enforce.
// A complete scan is evidence about one exact deployment reference; a missing,
// failed, stale, or differently identified result is therefore unknown and
// must not be promoted. UNKNOWN findings are also blocked because the scanner
// did not establish that the image is free of high/critical risk.
func checkVerifiedScanGate(policy api.AppSecurityPolicy, dep state.Deployment, status string, result *ScanResult) error {
	return checkVerifiedScanGateAt(policy, dep, status, result, time.Now().UTC())
}

func checkVerifiedScanGateAt(policy api.AppSecurityPolicy, dep state.Deployment, status string, result *ScanResult, now time.Time) error {
	if policy != api.AppSecurityPolicyEnforce {
		return nil
	}
	if status != "complete" {
		return verifiedScanFailure(policy, fmt.Sprintf("scan status %q is not complete", status))
	}
	if result == nil {
		return verifiedScanFailure(policy, "scan result is missing")
	}
	wantDigest := strings.TrimSpace(dep.ImageDigest)
	gotDigest := strings.TrimSpace(result.ImageDigest)
	if wantDigest == "" {
		return verifiedScanFailure(policy, "deployment image digest is empty")
	}
	if gotDigest == "" || gotDigest != wantDigest {
		return verifiedScanFailure(policy, fmt.Sprintf("scan digest %q does not match deployment digest %q", gotDigest, wantDigest))
	}
	if result.ArtifactDigest == "" {
		return verifiedScanFailure(policy, "scan artifact digest is missing")
	}
	if result.ScannerVersion == "" {
		return verifiedScanFailure(policy, "scanner version is missing")
	}
	if result.ScannerDBVersion == "" || result.ScannerDBBuiltAt == "" || !strings.EqualFold(result.ScannerDBStatus, "valid") {
		return verifiedScanFailure(policy, "scanner vulnerability database metadata is missing or invalid")
	}
	scannedAt, err := parseScanEvidenceTime(result.ScannedAt)
	if err != nil {
		return verifiedScanFailure(policy, "scan completion time is invalid")
	}
	if scannedAt.After(now) || now.Sub(scannedAt) > verifiedScanMaxAge {
		return verifiedScanFailure(policy, fmt.Sprintf("scan completed at %s and is older than %s", scannedAt.Format(time.RFC3339), verifiedScanMaxAge))
	}
	dbBuiltAt, err := parseScanEvidenceTime(result.ScannerDBBuiltAt)
	if err != nil {
		return verifiedScanFailure(policy, "scanner vulnerability database timestamp is invalid")
	}
	if dbBuiltAt.After(now) || now.Sub(dbBuiltAt) > verifiedScannerDBAge {
		return verifiedScanFailure(policy, fmt.Sprintf("scanner vulnerability database is older than %s", verifiedScannerDBAge))
	}
	counts := result.SeverityCounts
	if counts.Critical > 0 || counts.High > 0 {
		return verifiedScanFailure(policy, fmt.Sprintf("scan found %d critical and %d high vulnerabilities", counts.Critical, counts.High))
	}
	if counts.Unknown > 0 {
		return verifiedScanFailure(policy, fmt.Sprintf("scan returned %d unknown-severity vulnerabilities", counts.Unknown))
	}
	return nil
}

func parseScanEvidenceTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
