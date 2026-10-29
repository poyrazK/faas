package imaged

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A redeploy of unchanged code produces the same app artifact, and scanning it
// again repeats the previous answer: on production a build-cache-hit deploy
// spent 14–25 s in Grype on an unchanged 386 MB artifact (#5709). The
// per-deploy scan reuses an earlier deployment's result for the same app when
// the artifact digest matches and that result came from the scanner and
// vulnerability database installed now (currentScannerIdentity,
// ADR-20261028102540). That identity is re-read after every database refresh,
// so a result from an older database is never reused. Enforce-mode apps always
// scan: their evidence must be this deployment's own scan (ADR-075). The live
// re-scan (security_rescan.go) still re-evaluates every live deployment as the
// database moves on.

// scanReuseLookback is how many of the app's recent deployments are searched
// for a matching result.
const scanReuseLookback = 20

// scannedBy reports whether a result came from this scanner identity with a
// database Grype reported valid.
func scannedBy(r *ScanResult, id scannerIdentity) bool {
	return strings.EqualFold(r.ScannerDBStatus, "valid") && id.matches(r.ScannerVersion, r.ScannerDBVersion, r.ScannerDBBuiltAt)
}

// reusableDeployScan returns a copy of an earlier deployment's complete scan
// of the same artifact by the current scanner, or nil.
func (h *Handler) reusableDeployScan(ctx context.Context, app state.App, dep state.Deployment, artifactDigest string) *ScanResult {
	if app.SecurityPolicy == api.AppSecurityPolicyEnforce || artifactDigest == "" {
		return nil
	}
	scanner, err := h.currentScannerIdentity(ctx)
	if err != nil {
		return nil
	}
	recent, err := h.store.ListDeploymentsForApp(ctx, app.ID, scanReuseLookback, 0)
	if err != nil {
		h.log.Warn("imaged: list deployments for scan reuse", "deployment", dep.ID, "app", app.Slug, "err", err)
		return nil
	}
	for _, prior := range recent {
		if prior.ID == dep.ID || prior.ScanStatus != "complete" {
			continue
		}
		result := decodeScanEvidence(prior.ScanResult)
		if result == nil || result.Error != "" || result.ArtifactDigest != artifactDigest {
			continue
		}
		if !scannedBy(result, scanner) {
			continue
		}
		reused := *result
		reused.Vulnerabilities = append([]Vulnerability{}, result.Vulnerabilities...)
		reused.ReusedFrom = prior.ID
		return &reused
	}
	return nil
}
