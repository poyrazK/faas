// imaged SBOM populator (issue #299 / ADR-038 Phase 3).
//
// After a successful build, builderd's markSucceeded path is the
// canonical owner of the build_provenance row — builderd has the
// BuildID, the buildkit / railpack versions, the base digest, and
// the source SHA-256 already in scope, all of which MUST land on the
// provenance row in the same transaction.
//
// imaged's role is narrower: on each successful layer write, run
// syft over the layer's on-disk tree, marshal the resulting CycloneDX
// 1.5 JSON to bytes, and Put it under the canonical
// `regions/<region>/sboms/<buildID>.cdx.json` storage key (unprefixed for
// legacy apps). Imaged updates build_provenance.sbom_storage_key after the
// storage write succeeds; builderd creates the rest of the provenance row.
//
// This split keeps the new plumbing localised: imaged owns the
// filesystem artefact and the storage write; builderd owns the
// provenance-row insert; apid owns the read-side GET /v1/builds/{id}/sbom
// resolver. No new cross-component dependency.
//
// Fail-closed: a syft error does NOT fail the build (the build is
// observability metadata only — schema §4.2 lets the deployment
// succeed without a provenance row). It does, however, leave the
// sbom_storage_key empty for that build so the apid GET surfaces
// 503 build_sbom_unavailable rather than 404. Operators see a
// missing SBOM and re-run imaged to populate the column for
// future builds.

package imaged

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// BuildSBOMKey is the canonical storage key for a build's CycloneDX
// SBOM (issue #299 / ADR-038 Phase 3). The shape mirrors the digest
// sidecar and the scan sidecar — relative to the storage backend's
// root, with the `sboms/` prefix so a future remote OCI driver can
// route the prefix without parsing the filename.
//
// The 32-hex build id is the second component; the suffix is fixed
// to `.cdx.json` so the apid handler's `Content-Type:
// application/vnd.cyclonedx+json` is unambiguous and the operator can
// `faas storage cat sboms/<id>.cdx.json` straight from the local
// filesystem on a single-box deploy.
func BuildSBOMKey(buildID string) string {
	return region.BuildSBOMKey("", buildID)
}

// defaultSyftRun shells out to syft and returns the raw CycloneDX
// JSON bytes. Mirrors defaultGrypeRun's subprocess shape: command-
// context-bound, no inherited env beyond PATH, no shell. Errors are
// surfaced to the caller so the helper at writeBuildSBOM can fail
// closed (write nothing to storage; the build still succeeds).
//
// Production wires this default; tests inject a stub via
// WithSyftRun returning canned CycloneDX JSON so the storage write
// is hermetic and doesn't require syft on PATH.
func defaultSyftRun(ctx context.Context, dir string) ([]byte, error) {
	return runSyftImpl(ctx, "syft", dir)
}

// RunSyft is the production entry point for cmd/imaged when the
// operator pins syft to PATH (FAAS_SYFT_BIN is empty). Same shape
// as defaultSyftRun; exported so cmd/imaged's makeSyftRunner can
// bind a single function value to WithSyftRun without a closure
// wrapper.
func RunSyft(ctx context.Context, dir string) ([]byte, error) {
	return defaultSyftRun(ctx, dir)
}

// RunSyftAt is the operator-pinned variant: when the ansible role
// installs syft at a non-PATH location, cmd/imaged passes the
// absolute path via FAAS_SYFT_BIN and makeSyftRunner binds it to
// RunSyftAt. Dispatch: runSyftImpl with the explicit binary.
func RunSyftAt(ctx context.Context, bin, dir string) ([]byte, error) {
	return runSyftImpl(ctx, bin, dir)
}

// runSyftImpl is the shared body used by defaultSyftRun +
// RunSyft + RunSyftAt. Parameterised on the binary path so the
// JSON parse contract and the empty-output guard are not
// duplicated.
func runSyftImpl(ctx context.Context, bin, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, "dir:"+dir, "-o", "cyclonedx-json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("imaged: syft: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("imaged: syft: empty output for %q", dir)
	}
	return out, nil
}

// runSyft dispatches to the injected syftRun or falls back to the
// default subprocess runner. Mirrors runGrype's dispatch shape so a
// future With* family can be added with consistent semantics.
func (h *Handler) runSyft(ctx context.Context, dir string) ([]byte, error) {
	if h.syftRun != nil {
		return h.syftRun(ctx, dir)
	}
	return defaultSyftRun(ctx, dir)
}

// writeBuildSBOM runs syft against the build-output directory and
// persists the CycloneDX 1.5 JSON to the canonical storage key.
//
// computeRegion is the app's immutable placement; buildID is the Build.ID
// whose provenance imaged updates after publication. outDir houses the built
// layer ext4 + customer handler — syft's `dir:` source reads it
// directly.
//
// Errors are surfaced via the WARN log + return so the call site can
// choose between "best-effort" (let the build succeed without an
// SBOM, log a WARN for the operator) and "strict" (fail the build).
// The intended call site is best-effort: the build's success/fail
// transition is independent of the SBOM artefact, but a non-empty
// sbom_storage_key on the provenance row is required for the apid
// SDK's `faas build sbom <id>` to return a usable file.
//
// The storage backend resolution reuses h.storageFor() (mirrors
// writeScanSidecar's pattern in base_stage.go). When no backend is
// configured (the test default), the storageFor helper returns a
// per-handler LocalStorageBackend rooted at appsRoot so the test
// sees real round-tripped bytes.
func (h *Handler) writeBuildSBOM(ctx context.Context, computeRegion, buildID, outDir string) (string, error) {
	be, err := h.storageFor()
	if err != nil {
		return "", fmt.Errorf("imaged: writeBuildSBOM storageFor: %w", err)
	}
	sbomKey := region.BuildSBOMKey(computeRegion, buildID)
	if !region.ValidBuildSBOMKey(computeRegion, buildID, sbomKey) {
		return "", fmt.Errorf("imaged: invalid build SBOM identity")
	}
	if err := storage.ValidateArtifactRoute(be, sbomKey); err != nil {
		h.log.Warn("imaged: build SBOM route unavailable", "key", sbomKey, "err", err)
		return "", nil
	}
	blob, scanErr := h.runSyft(ctx, outDir)
	if scanErr != nil || len(blob) == 0 {
		h.log.Warn("imaged: syft failed; SBOM not persisted",
			"build_id", buildID, "out_dir", outDir, "err", scanErr)
		return "", nil
	}
	// Sanity-check the JSON shape at the parse-edge so a broken sbom
	// (e.g. syft's CLI parsing misfired and produced a partial doc)
	// doesn't get persisted as a "valid" SBOM that downstream
	// validators (cyclonedx-cli) will reject on the customer side.
	if !json.Valid(blob) {
		h.log.Warn("imaged: syft output is not valid JSON; SBOM not persisted",
			"build_id", buildID, "bytes", len(blob))
		return "", nil
	}
	if err := be.Put(ctx, sbomKey, bytes.NewReader(blob)); err != nil {
		h.log.Warn("imaged: write build SBOM to storage",
			"key", sbomKey, "err", err)
		return "", nil
	}
	return sbomKey, nil
}

// (compile-time assertion that wire is referenced — keeps imports
// stable when other steps move helpers here.)
var _ = wire.ScanKeyForBaseKey
