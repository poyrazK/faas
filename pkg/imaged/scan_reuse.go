package imaged

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A redeploy of unchanged code produces the same app artifact, and scanning it
// again repeats the previous answer: on production a build-cache-hit deploy
// spent 14–25 s in Grype on an unchanged 386 MB artifact (#5709). The
// per-deploy scan reuses an earlier deployment's result for the same app when
// the artifact digest matches and that result came from the scanner and
// vulnerability database the last real scan on this node used, within
// scanReuseScannerWindow. Enforce-mode apps always scan: their evidence must
// be this deployment's own scan (ADR-075). The live re-scan
// (security_rescan.go) still re-evaluates every live deployment as the
// database moves on.
const (
	// scanReuseScannerWindow bounds how old the last real scan may be: past
	// it, the database may have been updated without imaged seeing it.
	scanReuseScannerWindow = time.Hour
	// scanReuseLookback is how many of the app's recent deployments are
	// searched for a matching result.
	scanReuseLookback = 20
)

// scannerIdentity names the scanner and vulnerability database behind a
// result.
type scannerIdentity struct {
	version, dbVersion, dbBuiltAt string
}

// scannerOf returns the identity of a result whose database was valid.
func scannerOf(r *ScanResult) (scannerIdentity, bool) {
	id := scannerIdentity{
		version:   strings.TrimSpace(r.ScannerVersion),
		dbVersion: strings.TrimSpace(r.ScannerDBVersion),
		dbBuiltAt: strings.TrimSpace(r.ScannerDBBuiltAt),
	}
	ok := id.version != "" && id.dbVersion != "" && id.dbBuiltAt != "" && strings.EqualFold(r.ScannerDBStatus, "valid")
	return id, ok
}

// scannerMemo remembers the scanner of the last real per-deploy scan.
type scannerMemo struct {
	mu sync.Mutex
	id scannerIdentity
	at time.Time
}

// record notes the scanner of a real scan; a failed scan forgets it.
func (m *scannerMemo) record(r *ScanResult, err error, now time.Time) {
	var id scannerIdentity
	ok := false
	if err == nil && r != nil {
		id, ok = scannerOf(r)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !ok {
		m.id, m.at = scannerIdentity{}, time.Time{}
		return
	}
	m.id, m.at = id, now
}

func (m *scannerMemo) current(now time.Time) (scannerIdentity, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.at.IsZero() || now.Sub(m.at) > scanReuseScannerWindow {
		return scannerIdentity{}, false
	}
	return m.id, true
}

// reusableDeployScan returns a copy of an earlier deployment's complete scan
// of the same artifact by the current scanner, or nil.
func (h *Handler) reusableDeployScan(ctx context.Context, app state.App, dep state.Deployment, artifactDigest string) *ScanResult {
	if app.SecurityPolicy == api.AppSecurityPolicyEnforce || artifactDigest == "" {
		return nil
	}
	scanner, ok := h.lastDeployScanner.current(time.Now())
	if !ok {
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
		if id, ok := scannerOf(result); !ok || id != scanner {
			continue
		}
		reused := *result
		reused.Vulnerabilities = append([]Vulnerability{}, result.Vulnerabilities...)
		reused.ReusedFrom = prior.ID
		return &reused
	}
	return nil
}
