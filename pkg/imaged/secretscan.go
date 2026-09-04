// Package imaged — imaged-layer secret scan (closes the v2 source-tree
// gap for OCI image bytes).
//
// The v2 server-side scanner (cmd/apid/secretscan.go::scanExtractedTreeSecrets)
// walks the customer's source-tree extract spool and rejects the upload at
// /v1/projects[/scan] on any candidate match. That path misses secrets
// baked into the OCI image itself:
//
//   - `ENV STRIPE_KEY=...` in a Dockerfile — Railpack/BuildKit inside the
//     builder microVM (ADR-003) bakes it into a layer;
//   - `--build-arg SECRET=...` to BuildKit — same;
//   - `COPY .env /app/.env` in a build step — same.
//
// imaged never sees the Dockerfile or the build args; it sees only the
// final merged layers (via aboveBaseLayers + the StorageBackend). The
// layer-secret-scan is therefore the post-build seam that closes the
// gap: re-stage the per-app ext4 via stageScanExt4 (same helper grype
// uses for ADR-075) and walk the resulting filesystem with
// pkg/secretscan.ScanFile — same engine the v2 source-tree path uses,
// same patterns, same providers, same Severity table.
//
// Posture (vs best-effort): a layer-side finding is a security
// boundary violation, not an observability artefact. Mirror
// StatefulDenyListMatch's loud-fail shape (base.go::errStatefulViolation):
// any finding stamps the audit row via state.Store.UpsertDeploymentSecretFindings
// and markDeployFailed with errImageSecretDetected. The grype CVE path
// (runDeployScan) is best-effort by design (ADR-075 AC #4 — don't block
// deploys on supply-chain metadata); the secret path is intentionally
// NOT — secrets are not metadata.
//
// Per-layer source: api.SecretFinding carries a `layer` field on the
// wire, populated with "app" for the main image and "sidecar-<slug>"
// for each sidecar. The walk is otherwise identical between paths.
package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretscan"
	"github.com/onebox-faas/faas/pkg/state"
)

// imagedLayerSecretScanMaxBytes caps per-file bytes read during the
// layer walk. Mirrors cmd/apid/secretscan.go::serverSecretScanMaxBytes
// (1 MiB) so the two paths agree on what is scanned. Strict (drop on
// overflow) — a truncated PEM block can fail to match the armour
// pattern and silently slip through, so we prefer skipping over
// truncating.
const imagedLayerSecretScanMaxBytes = 1 << 20

// imagedLayerSecretScanExcludedDirs is intentionally limited to the
// post-build image walk. Unlike the source-ingress scan, this pass runs
// against a clean per-app ext4 after the customer archive has crossed the
// trust boundary; skipping common build-artifact directories keeps image
// scanning bounded while the source scan remains exhaustive.
func imagedLayerSecretScanIsExcludedDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "__pycache__",
		".venv", "venv", "target", "dist", "build":
		return true
	}
	return false
}

// runDeployLayerSecretScan is the imaged-side default
// implementation of the post-build layer walker. Mirrors
// cmd/apid/secretscan.go::scanExtractedTreeSecrets exactly — same
// open-with-Lstat-guard, same 1 MiB strict cap, same IsTextFile
// gate, same ScanFile entry. The "layer" argument is purely
// bookkeeping: it appears in slog so the operator can attribute
// "scanned N files in layer=sidecar-redis" in production logs.
//
// Errors are non-fatal at the per-file level (logged via slog,
// walk continues) but the returned error is the walk-level error
// if the walk itself was unrecoverable (e.g. permission denied on
// the staged root). The caller (handler.go::runDeployLayerSecretScan)
// decides whether a walk-level error is fatal for the deploy.
func runDeployLayerSecretScan(ctx context.Context, dir, layer string) ([]secretscan.Finding, error) {
	if dir == "" {
		return nil, errors.New("imaged: layer secret scan called with empty dir")
	}
	var findings []secretscan.Finding
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			if p != dir && imagedLayerSecretScanIsExcludedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		relSlash := filepath.ToSlash(rel)
		// Cheap size gate before opening. d.Info() follows symlinks
		// (kernel resolved them during the dir walk), so a
		// symlink-to-large-file is caught here.
		info, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("stat %q: %w", p, ierr)
		}
		if info.Size() > imagedLayerSecretScanMaxBytes {
			return nil
		}
		f, ferr := openLayerFile(p)
		if ferr != nil {
			return fmt.Errorf("open %q: %w", p, ferr)
		}
		// Strict cap: read N+1 so we can detect overflow and skip
		// without scanning a truncated copy. Mirror the apid
		// source-tree pattern.
		data, rerr := io.ReadAll(io.LimitReader(f, imagedLayerSecretScanMaxBytes+1))
		_ = f.Close()
		if rerr != nil {
			return fmt.Errorf("read %q: %w", p, rerr)
		}
		if int64(len(data)) > imagedLayerSecretScanMaxBytes {
			return nil
		}
		if !secretscan.IsTextFile(relSlash, data) {
			return nil
		}
		findings = append(findings, secretscan.ScanFile(relSlash, data)...)
		return nil
	})
	if walkErr != nil {
		return findings, walkErr
	}
	return findings, nil
}

// RunDeployLayerSecretScan is the package-exported alias of
// runDeployLayerSecretScan — cmd/imaged wires it via
// WithSecretScanRun so the production daemon has a way to inject
// the default walker without the package-private name leaking.
// Mirrors the exported RunGrype / RunSyft aliases that hint
// grype/syft subprocesses to the With* setters. Tests typically
// bypass this and inject a stub via WithSecretScanRun directly;
// the wrapper exists for the cmd/imaged default-wiring site.
func RunDeployLayerSecretScan(ctx context.Context, dir, layer string) ([]secretscan.Finding, error) {
	return runDeployLayerSecretScan(ctx, dir, layer)
}

// openLayerFile opens a file inside an imaged-staged ext4 mount for
// scanning. The staged root lives at a path returned by
// stageScanExt4 — a tempfile os.MkdirTemp'd by the imaged handler —
// so the path is "vetted" by construction: only files the imaged
// build pipeline wrote into the mount can be walked here. We still
// guard with a post-open Lstat mirroring cmd/apid/secretscan.go::openSpoolFile
// so the security boundary is the same shape on both sides.
//
// The bare os.Open below is the load-bearing exception that the
// .golangci.yml forbidigo rule exists to gate. The wrapper IS the
// vetted-id helper; anything less lands in a review tripwire.
func openLayerFile(path string) (*os.File, error) {
	//nolint:forbidigo // openLayerFile IS the security boundary — post-open Lstat discipline below is what makes os.Open safe here.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("post-open stat %q: %w", path, err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("refusing non-regular file at %q (mode %s)", path, info.Mode())
	}
	return f, nil
}

// errImageSecretDetected is the typed sentinel runDeployLayerSecretScan
// returns to markDeployFailed on a finding. Mirrors
// base.go::errStatefulViolation — a sentinel SentinelToCode lifts to
// the wire-stable code "image_secret_detected" on the deployment's
// error_code column. ADR-075 ships the same mapping for grype-side
// failures; this is the secret-side analog. Declared in handler.go
// alongside handler.runDeployLayerSecretScan so the sentinel lives
// next to the method that emits it.

// withSecretScanFindings marshals []secretscan.Finding into the
// jsonb payload state.Store.UpsertDeploymentSecretFindings expects,
// and stamps the per-finding "layer" attribute from the supplied
// label. The wire shape matches api.SecretFinding one-to-one so
// the dashboard / `gregale deployment --show-secret-scan` can
// render the row without an extra translation pass.
//
// status is the scan_status enum value —
// "complete_with_redactions" matches the v2 widening (migration
// 00264 widened deployments_scan_status_chk to include this
// value); "complete" stamps the clean-walk case (no findings;
// audit row says "we scanned this layer, nothing found"). The
// jsonb payload's own Status field mirrors the column so a
// /v1/deployments/{id}/secret-scan GET needn't consult a
// separate column to render the dashboard pill.
//
// imageDigest is the OCI digest the scan ran against. It is
// stamped on the payload (api.SecretScanResult.ImageDigest)
// so the dashboard can render "scanned <digest> on <ts>" —
// same shape as the grype ScanResult.ImageDigest column.
func withSecretScanFindings(findings []secretscan.Finding, layer, status, imageDigest string, scannedAt time.Time) ([]byte, string, error) {
	wireFindings := make([]api.SecretFinding, 0, len(findings))
	for _, f := range findings {
		wireFindings = append(wireFindings, api.SecretFinding{
			File:     f.File,
			Line:     f.Line,
			Key:      f.Key,
			Provider: f.Provider,
			Severity: f.Severity.String(),
			Snippet:  f.Snippet,
			Layer:    layer,
		})
	}
	payload := api.SecretScanResult{
		Status:      status,
		ScannedAt:   scannedAt.UTC().Format(time.RFC3339Nano),
		Findings:    wireFindings,
		ImageDigest: imageDigest,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("marshal secret-findings: %w", err)
	}
	return b, status, nil
}

// upsertDeploymentSecretFindings is the thin imaged-side wrapper
// that stamps the audit row. PR-A behaviour: stamp every scan,
// clean or hit — the audit row is the source of truth for "did
// we scan this layer" and a clean stamp lets the dashboard
// render "scan complete · no findings" rather than "scan
// pending" once the deploy lands. The hit case still fails the
// deploy loudly (runDeployLayerSecretScan handles that on its
// own); this helper ONLY writes the audit row.
//
// Not a method on Handler because the underlying state.Store is
// the shared DI seam — Handler just hands its store through.
//
// Best-effort on the audit-row write: if
// UpsertDeploymentSecretFindings fails (db blip, schema drift),
// we log at WARN and return without affecting the deploy path.
// For the hit case the caller has ALREADY failed the deploy via
// markDeployFailed before calling this helper, so a write failure
// here doesn't double-fail.
func upsertDeploymentSecretFindings(
	ctx context.Context,
	st state.Store,
	depID string,
	findings []secretscan.Finding,
	layer, imageDigest string,
	scannedAt time.Time,
	log Logger,
) {
	status := layerSecretScanStatusCompleteWithRedactions
	if len(findings) == 0 {
		status = layerSecretScanStatusComplete
	}
	payload, _, err := withSecretScanFindings(findings, layer, status, imageDigest, scannedAt)
	if err != nil {
		log.Warn("imaged: marshal secret findings",
			"deployment", depID, "layer", layer, "err", err)
		return
	}
	if writeErr := st.UpsertDeploymentSecretFindings(ctx, depID, payload, status, scannedAt); writeErr != nil {
		log.Warn("imaged: stamp secret findings audit row",
			"deployment", depID, "layer", layer, "err", writeErr)
	}
}

// Logger is the slog-shaped seam the imaged secretscan wrapper takes
// — Handler.log is a *slog.Logger; the interface lets us pass either
// the production handler or a test stub without forcing the imaged
// package to depend on slog directly. Matches the Log interface
// defined in pkg/imaged/handler.go for grype/syft stubs.
type Logger interface {
	Warn(msg string, args ...any)
}

// layerSecretScanStatusComplete is the scan_status value for a
// clean layer walk (no findings). PR-A stamps the row on every
// walk — clean or hit — so the dashboard renders "scan complete ·
// 0 findings" instead of "scan pending" once the deploy lands.
// Mirrors the grype path's "complete" sentinel.
const layerSecretScanStatusComplete = "complete"

// layerSecretScanStatusCompleteWithRedactions is the scan_status
// value when the layer walk emitted one or more findings. The
// v2 migration (00264) widened deployments_scan_status_chk to
// accept this value so the redactions case is distinct on the
// dashboard pill + the same shape the apid-side source-tree
// 422 path produces.
const layerSecretScanStatusCompleteWithRedactions = "complete_with_redactions"
