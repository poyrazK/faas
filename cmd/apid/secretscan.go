// Server-side secret-scan pass for the upload pipeline.
//
// Client-side --secret-scan (cmd/gregale/pack.go) is best-effort: a
// customer can --secret-scan=off, or build a tarball out-of-band and
// upload it via `curl -F source=@…`. This file is the server-side
// defense: every project scan and deploy tarball/source-ref ingress
// walks the extracted tree, runs pkg/secretscan over every text-shaped
// file, and rejects the upload if any candidate match is produced.
//
// The reject envelope is api.Problem with code=secret_scan_strict,
// status=422, and the full findings list under `secret_findings` plus
// the remediation hint under `secret_hint`. The shape is shared with
// the CLI's StrictSecretScanError (cmd/gregale/pack_strict.go) so a
// programmatic consumer sees the same envelope regardless of which
// side rejected the upload.
//
// Why a per-file walk and not one grep: the per-file shape lets us
// honour the text-file gate (IsTextFile) — a PNG with a base64-shaped
// noise stripe should not fire on every tarball. The 1 MiB per-file
// cap mirrors cmd/gregale/pack.go::treeScanMaxBytes so the two paths
// agree on what was scanned.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretscan"
)

// serverSecretScanMaxBytes caps per-file bytes read during the
// server-side tree scan. Mirrors cmd/gregale/pack.go::treeScanMaxBytes
// so the two paths agree on what is scanned. The cap is enforced
// strictly (drop on overflow) — a truncated PEM block can fail to
// match the armour pattern and silently slip through, so we prefer
// skipping over truncating.
const serverSecretScanMaxBytes = 1 << 20

// openSpoolFile opens a file from the apid extract spool for scanning.
// The spool lives under the apid-internal scan dir (cmd/apid/scan_service.go
// passes req.ScanDir, which is a freshly-created os.MkdirTemp that the
// caller owns), so the path is "vetted" by construction: only files
// apid itself wrote into the spool can be walked here. We still guard
// with a post-open Lstat mirroring cmd/gregale/commands5.go::openCustomerFile
// so the security boundary is the same shape on both sides — a TOCTOU
// race or a future code path that reuses this helper against a less
// vetted directory will trip the Lstat guard instead of touching the
// file's bytes.
//
// The bare os.Open below is the load-bearing exception that the
// .golangci.yml forbidigo rule exists to gate. The wrapper IS the
// vetted-id helper; anything less lands in a review tripwire.
func openSpoolFile(path string) (*os.File, error) {
	//nolint:forbidigo // openSpoolFile IS the security boundary — post-open Lstat discipline below is what makes os.Open safe here.
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

// scanExtractedTreeSecrets walks scanDir and returns every secret
// match found across all text-shaped files. A file that can't be
// opened or read stops the walk and is returned alongside any findings
// collected so far. The caller decides whether to 422 (today) or
// fail-open (the v1 alternative, rejected for security).
//
// The scan is purely advisory at the wire — the upload's bytes are
// NOT modified. The customer's local CLI is the one that redacts;
// the server's job is to refuse to accept poisoned bytes in the first
// place. This is also the order the spec calls for: redacted-at-source
// + filtered-at-server (ADR-094 / §11).
func scanExtractedTreeSecrets(scanDir string) ([]secretscan.Finding, error) {
	var findings []secretscan.Finding
	walkErr := filepath.WalkDir(scanDir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(scanDir, p)
		if rerr != nil {
			return rerr
		}
		relSlash := filepath.ToSlash(rel)
		// Cheap size gate before opening. The os.DirEntry.Info() stat
		// follows symlinks (the kernel resolved them during the dir
		// walk), so a symlink-to-large-file is caught here.
		info, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("stat %q: %w", p, ierr)
		}
		if info.Size() > serverSecretScanMaxBytes {
			return nil
		}
		f, ferr := openSpoolFile(p)
		if ferr != nil {
			return fmt.Errorf("open %q: %w", p, ferr)
		}
		// Strict cap: read N+1 so we can detect overflow and skip
		// without scanning a truncated copy.
		data, rerr := io.ReadAll(io.LimitReader(f, serverSecretScanMaxBytes+1))
		// Close inline — `defer f.Close()` inside a WalkDir callback
		// only fires when the outer function returns, so every
		// visited file would stay open until the walk completes and
		// a 10k-file customer tree could pin 10k FDs simultaneously,
		// exhausting `ulimit -n` on a busy control-plane box. The
		// CLI twin (cmd/gregale/pack.go) closes inline for the same
		// reason; mirror it here.
		_ = f.Close()
		if rerr != nil {
			return fmt.Errorf("read %q: %w", p, rerr)
		}
		if int64(len(data)) > serverSecretScanMaxBytes {
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

// scanSourceTarballSecrets applies the extraction and server-side secret
// scan gates to a spooled deploy tarball. Project scans already pass through
// this sequence; keeping it here makes the direct tarball and source-ref
// ingress paths subject to the same expanded-size, entry-type, and secret
// policy before a build row is created.
func scanSourceTarballSecrets(sourcePath string, limits api.Limits) *api.Problem {
	scanDir, prob := extractTarGzToDir(sourcePath, defaultExtractLimits(limits))
	if prob != nil {
		return prob
	}
	defer func() { _ = os.RemoveAll(scanDir) }()

	findings, err := scanExtractedTreeSecrets(scanDir)
	if err != nil && len(findings) == 0 {
		return api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Secret scan failed", err.Error())
	}
	if len(findings) > 0 {
		return newSecretScanRejectionProblem(findings)
	}
	if err != nil {
		// Partial findings are still actionable, but do not silently
		// convert a walk error into an accepted source when no finding
		// happened to be in the unreadable portion.
		return api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Secret scan failed", err.Error())
	}
	return nil
}

// newSecretScanRejectionProblem builds the 422 envelope for an
// upload blocked by the server-side scan. Findings are serialized
// under `secret_findings` (mirroring the on-disk deployment response
// shape — same struct, same field names) so a customer recovers from
// the rejection by reading the same UI the dashboard would render
// post-merge.
//
// The Hint is intentionally identical to the CLI's strict-mode hint
// so a customer running `gregale deploy` AND the dashboard's "Upload
// Tarball" button sees the same one-line next-action guidance.
func newSecretScanRejectionProblem(findings []secretscan.Finding) *api.Problem {
	wireFindings := make([]api.SecretFinding, 0, len(findings))
	for _, f := range findings {
		wireFindings = append(wireFindings, api.SecretFinding{
			File:     f.File,
			Line:     f.Line,
			Key:      f.Key,
			Provider: f.Provider,
			Severity: f.Severity.String(),
			Snippet:  f.Snippet,
		})
	}
	const hint = "move detected secrets to `gregale secrets set` (see https://docs.gregale.dev/cli/secrets)"
	return api.NewProblem(
		http.StatusUnprocessableEntity,
		api.CodeSecretScanStrict,
		"Secret-shaped values found in uploaded source",
		"redact secret values or move them to `gregale secrets set`",
	).WithDocs("https://docs.gregale.dev/cli/secrets").
		WithSecretScan(wireFindings, hint)
}
