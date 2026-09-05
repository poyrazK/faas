// commands_release.go — operator-side CLI for the cluster-shipped
// release bundle (issue #911 / ADR-110).
//
// `gregalectl release` is the operator surface that materialises a
// release bundle from a pre-built bin directory and installs it on
// the local box. The two subcommands map to the two halves of
// PR-3:
//
//   gregalectl release bundle --bin-dir PATH --git-sha SHA --manifest-hash HASH
//   gregalectl release install --git-sha SHA [--releases-root PATH] [--node NAME] [--role ROLE] [--defer-activation]
//   gregalectl release history [--daemon NAME] [--limit N]
//   gregalectl release inspect SHA
//
// Dispatcher shape mirrors commands_manifest.go:
// flag.Parse for the leaf's own flags, subcommand fan-out in
// cmdReleaseDispatch, --json / FAAS_JSON=1 honored.
//
// Materialise-from-git (`make build-sha256`, etc.) is intentionally
// OUT of scope: PR-3 only flips the symlink + writes the
// release_bundles row + stamps the first-write-wins applied_at.
// The operator runs the deterministic build target out-of-band and
// hands `gregalectl release bundle` the resulting bin directory.
//
// ADR-112 (role-image-collapse):
//   --role is a NEW flag on `release install`. First-boot flow:
//     `release install --role $FAAS_BOX_ROLE` (or the equivalent
//     implicit read from /etc/faas/first-boot.env when --role is
//     empty). PR-B (issue #935) extends --role to be the in-place
//     mutation flag: `release install --role compute-only` on an
//     already-running control-plane box transitions the subset.

package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
	"github.com/onebox-faas/faas/pkg/releaseretention"
	"github.com/onebox-faas/faas/pkg/roleTemplating"
	"github.com/onebox-faas/faas/pkg/state"
)

// release subcommands.
const (
	subReleaseBundle  = "bundle"
	subReleaseInstall = "install"
	subReleaseKGV     = "kgv"
	subReleaseHistory = "history"
	subReleaseInspect = "inspect"

	// Canonical release asset names. These are the names attached to a
	// GitHub Release and retained under each installed release directory.
	releaseTarballName = "release.tar.gz"
	releaseSigName     = "release.cosign.bundle"
	releaseSBOMName    = "release.sbom.json"
)

// cmdReleaseDispatch is the parent dispatcher.
func cmdReleaseDispatch(args []string) int {
	if len(args) == 0 {
		printReleaseUsage(os.Stderr)
		return 1
	}
	switch args[0] {
	case subReleaseBundle:
		return cmdReleaseBundle(args[1:])
	case subReleaseInstall:
		return cmdReleaseInstall(args[1:])
	case subReleaseKGV:
		return cmdReleaseKGV(args[1:])
	case subReleaseHistory:
		return cmdReleaseHistory(args[1:])
	case subReleaseInspect:
		return cmdReleaseInspect(args[1:])
	case flagHelpShort, flagHelpLong:
		printReleaseUsage(os.Stderr)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gregalectl release: unknown subcommand %q (expected: bundle | install | kgv | history | inspect)\n", args[0])
		return 1
	}
}

func printReleaseUsage(w io.Writer) {
	_, _ = fmt.Fprintf(w, `usage: gregalectl release <subcommand> [flags]

Subcommands:
  bundle    Materialise a release bundle from a pre-built bin directory.
            Writes <releases-root>/<git-sha>/release-manifest.json and
            INSERTs a row into release_bundles.
  install   Install a release on the local box (atomic symlink flip +
            release_bundles.applied_at first-write-wins stamp).
  history   Show durable daemon deployment history from PostgreSQL.
  inspect   Inspect one release bundle and reconcile it with /opt/faas/current.

Flags (bundle):
  --bin-dir PATH        Path to the directory holding the daemon binaries
                        (one file per daemon in the manifest catalog).
  --git-sha SHA         40-char lowercase hex git SHA (required).
  --manifest-hash HASH  Manifest hash as 'sha256:<64hex>' (required).
  --releases-root PATH  Releases root (default: /opt/faas/releases).

Flags (install):
  --git-sha SHA         40-char lowercase hex git SHA to install (required).
  --releases-root PATH  Releases root (default: /opt/faas/releases).
  --node NAME           compute_nodes.name to stamp (default:
                        FAAS_NODE_NAME, then hostname; compute-only
                        installs use NAME.faas).
  --role ROLE            control-plane or compute-only role template.
  --defer-activation     keep a compute row drained until readiness passes.
  --reason TEXT          operator reason recorded in the deployment ledger.

Flags (history):
  --daemon NAME          filter to one daemon (default: all).
  --limit N              maximum rows (default: 50, maximum: 500).
  --releases-root PATH   releases root (default: /opt/faas/releases).

Arguments (inspect):
  SHA                    40-char lowercase release git SHA.

Exit codes:
  0  success
  1  usage error / invalid input
  3  platform/infra (file missing, DB unreachable, symlink target invalid)

Examples:
  gregalectl release bundle --bin-dir=out/bin --git-sha=$(git rev-parse HEAD) \
      --manifest-hash=sha256:$(sha256sum manifest.yaml | cut -d' ' -f1)
  gregalectl release install --git-sha=$(git rev-parse HEAD)
  gregalectl release history --limit=25
`)
}

// cmdReleaseBundle materialises a cluster-shipped release bundle:
// hashes every daemon binary in --bin-dir, writes the manifest, and
// INSERTs a row into release_bundles.
//
// This is the operator-side CLI for PR-3; it does NOT build the
// binaries (that is `make build-sha256`'s job, run out-of-band).
// The operator hands the CLI the already-built bin directory plus
// the git_sha + manifest_hash that go with it.
func cmdReleaseBundle(args []string) int {
	if len(args) > 0 && (args[0] == flagHelpLong || args[0] == flagHelpShort) {
		PrintUsage(os.Stderr, "usage: gregalectl release bundle --bin-dir PATH --git-sha SHA --manifest-hash HASH", "release")
		return 0
	}
	fs := flag.NewFlagSet("release bundle", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	binDir := fs.String("bin-dir", "", "path to the directory holding daemon binaries (required)")
	gitSHA := fs.String("git-sha", "", "40-char lowercase hex git SHA (required)")
	manifestHash := fs.String("manifest-hash", "", "manifest hash as 'sha256:<64hex>' (required)")
	releasesRoot := fs.String("releases-root", "/opt/faas/releases", "releases root directory")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *binDir == "" || *gitSHA == "" || *manifestHash == "" {
		_, _ = fmt.Fprintln(os.Stderr, "gregalectl release bundle: --bin-dir, --git-sha, and --manifest-hash are required")
		return 1
	}
	// Validate git_sha / manifest_hash shape BEFORE touching the
	// filesystem so a malformed CLI argument surfaces as a usage
	// error (exit 1), not as a platform/infra error (exit 3).
	if err := releaseinstall.ValidateBundleInputs(*gitSHA, *manifestHash); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release bundle: %v\n", err)
		return 1
	}

	// Resolve absolute bin dir so the manifest's per-release
	// directory walks the same paths the verifier (PR-4) will.
	absBin, err := filepath.Abs(*binDir)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release bundle: resolve bin-dir: %v\n", err)
		return 3
	}
	if _, err := os.Stat(absBin); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release bundle: bin-dir: %v\n", err)
		return 3
	}
	if err := copyBinIntoRelease(*releasesRoot, *gitSHA, absBin); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release bundle: stage bin: %v\n", err)
		return 3
	}
	now := time.Now().UTC()
	m, err := releaseinstall.Build(*releasesRoot, *gitSHA, *manifestHash, now)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release bundle: build manifest: %v\n", err)
		return 1
	}
	if err := releaseinstall.Write(*releasesRoot, m); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release bundle: write manifest: %v\n", err)
		return 3
	}
	// INSERT into release_bundles. If the DB is unreachable, we
	// still have the on-disk manifest — the operator can retry
	// the INSERT (release_bundles has no UNIQUE on git_sha so
	// retries would collide; the CLI surfaces that as a conflict).
	pool, dbErr := openPgPoolFromEnv(context.Background())
	if dbErr != nil {
		if jsonEnabled() {
			jsonEmit(os.Stdout, releaseBundleReport{
				GitSHA:       *gitSHA,
				ManifestHash: *manifestHash,
				ManifestPath: filepath.Join(releaseinstall.BundleRoot(*releasesRoot, *gitSHA), releaseinstall.ManifestName),
				DBError:      dbErr.Error(),
			})
		} else {
			_, _ = fmt.Fprintf(os.Stdout, "wrote %s for %s (DB unreachable: %v)\n",
				filepath.Join(releaseinstall.BundleRoot(*releasesRoot, *gitSHA), releaseinstall.ManifestName),
				*gitSHA, dbErr)
		}
		return 3
	}
	defer pool.Close()
	store := releaseinstall.NewStore(pool)
	id, err := store.Insert(context.Background(), releaseinstall.FromManifest(m))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release bundle: insert release_bundles: %v\n", err)
		return 3
	}
	if jsonEnabled() {
		jsonEmit(os.Stdout, releaseBundleReport{
			GitSHA:       *gitSHA,
			ManifestHash: *manifestHash,
			ManifestPath: filepath.Join(releaseinstall.BundleRoot(*releasesRoot, *gitSHA), releaseinstall.ManifestName),
			ID:           id,
		})
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "wrote %s for %s (id=%s)\n",
			filepath.Join(releaseinstall.BundleRoot(*releasesRoot, *gitSHA), releaseinstall.ManifestName),
			*gitSHA, id)
	}
	return 0
}

// copyBinIntoRelease copies release-catalog regular files in srcDir into
// <releasesRoot>/<gitSHA>/bin/<basename>. Symlinks and directories are
// skipped — the current release catalog is flat.
func copyBinIntoRelease(releasesRoot, gitSHA, srcDir string) error {
	bin := releaseinstall.BinDir(releasesRoot, gitSHA)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", bin, err)
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", srcDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// The normal build directory also contains CLIs and vmmd support
		// helpers. Keep only the release catalog; Build will still fail
		// closed if any daemon is missing.
		if !releaseinstall.IsReleaseBinaryName(e.Name()) {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(bin, e.Name())
		body, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", src, err)
		}
		//nolint:gosec // legacy path (sunset flag); e.Name() comes from
		// os.ReadDir which never returns "../". Filepath.Join escapes the
		// bin dir prefix on a "../" prefix in e.Name(), so the path is
		// always under bin/.
		if err := os.WriteFile(dst, body, 0o755); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}
	if info, err := os.Stat(filepath.Join(srcDir, "runners")); err == nil && info.IsDir() {
		for _, asset := range releaseinstall.RuntimeAssetNames() {
			src := filepath.Join(srcDir, filepath.FromSlash(asset))
			body, err := os.ReadFile(src)
			if err != nil {
				return fmt.Errorf("read runtime asset %s: %w", asset, err)
			}
			dst := filepath.Join(bin, filepath.FromSlash(asset))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("mkdir runtime asset %s: %w", asset, err)
			}
			if err := os.WriteFile(dst, body, 0o755); err != nil {
				return fmt.Errorf("write runtime asset %s: %w", asset, err)
			}
		}
	}
	return nil
}

// cmdReleaseInstall installs a release on the local box: flips
// /opt/faas/current to <git-sha>, UPSERTs compute_nodes.release_id
// + manifest_hash (PR-6), and runs the release_bundles.applied_at
// first-write-wins UPDATE.
//
// DB writes go through the releaseinstall.Store abstraction so
// tests can inject a fake; the production code path uses pgxpool
// directly to avoid spurious pgstore regen for the per-PR release
// table.
func cmdReleaseInstall(args []string) int {
	if len(args) > 0 && (args[0] == flagHelpLong || args[0] == flagHelpShort) {
		PrintUsage(os.Stderr, "usage: gregalectl release install --git-sha SHA [--role ROLE] [--defer-activation]", "release")
		return 0
	}
	fs := flag.NewFlagSet("release install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	gitSHA := fs.String("git-sha", "", "40-char lowercase hex git SHA to install (required)")
	releasesRoot := fs.String("releases-root", "/opt/faas/releases", "releases root directory")
	nodeName := fs.String("node", "", "compute_nodes.name to stamp (default: hostname)")
	// ADR-112: --role is the role-templating trigger. Empty means
	// "do nothing here" (legacy callers); when set, the binary
	// applies roleTemplating.ApplyFilesystem(role) after the
	// symlink flip.
	roleFlag := fs.String("role", "", "box role: control-plane|compute-only (ADR-112). Empty = no role templating.")
	deferActivation := fs.Bool("defer-activation", false, "keep the compute_nodes row drained after install; the deployment pipeline activates it only after readiness gates")
	reason := fs.String("reason", "", "operator reason recorded in the platform release ledger")
	// ADR-113: --legacy-bundle-dir is the sunset path for the old
	// `copyBinIntoRelease` flow. Empty (default) means use the new
	// tarball + cosign + SBoM-gated path. When set, the install
	// copies binaries from DIR straight into <releases-root>/<git-
	// sha>/bin via copyBinIntoRelease. Even on the legacy path,
	// the catalog check (releaseinstall.Verify) is run AFTER the
	// copy so a contaminated bin-dir (e.g., a leftover tarball
	// member) cannot become the active release. PR-B removes the
	// flag entirely.
	legacyBundleDir := fs.String("legacy-bundle-dir", "", "ADR-113 sunset path: copy binaries from DIR straight to <releases-root>/<git-sha>/bin. Catalog check still runs after copy. Empty = use the canonical tarball path.")
	// ADR-113: --tarball-path is the air-gap / load-bearing
	// trust-bit path. When set, the install reads the canonical
	// tarball from PATH, runs `cosign verify-blob` against it
	// (Tarball.Verify), parses the embedded SPDX-2.3 SBoM, and
	// runs the CVE-baseline diff BEFORE AtomicFlip. This is what
	// makes the cosign verifier (pkg/releaseinstall/cosign.go)
	// exercised in production — without this flag, the cosign
	// half is whitebox-only.
	tarballPath := fs.String("tarball-path", "", "ADR-113 canonical path: load release.tar.gz from PATH and run cosign verify-blob + SBoM CVE-baseline diff before install. Empty = use the legacy or bin-dir path.")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *gitSHA == "" {
		_, _ = fmt.Fprintln(os.Stderr, "gregalectl release install: --git-sha is required")
		return 1
	}
	if !releaseinstall.ValidGitSHA(*gitSHA) {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: --git-sha %q is not a 40-char lowercase hex\n", *gitSHA)
		return 2
	}
	if *legacyBundleDir != "" && *tarballPath != "" {
		_, _ = fmt.Fprintln(os.Stderr, "gregalectl release install: --legacy-bundle-dir and --tarball-path are mutually exclusive")
		return 2
	}
	// ADR-113: canonical tarball path. Reads the tarball from
	// PATH, runs cosign verify-blob + SBoM CVE-baseline diff
	// BEFORE AtomicFlip. This is the production exercise path
	// for Tarball.Verify (the cosign verifier in
	// pkg/releaseinstall/cosign.go).
	if *tarballPath != "" {
		if err := installViaTarball(*releasesRoot, *gitSHA, *tarballPath); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: %v\n", err)
			return 3
		}
		// Fall through to the role-templating + DB-write path
		// below; the load-bearing gates (cosign, SBoM, catalog)
		// ran inside installViaTarball.
	}
	// ADR-113: legacy path. If --legacy-bundle-dir is set, we run
	// copyBinIntoRelease AND the catalog check — a contaminated
	// bin-dir (extra files like a leftover tarball) cannot
	// become the active release. Review finding #4: previously
	// bypassed Verify; now runs Verify after the copy.
	if *legacyBundleDir != "" {
		if err := copyBinIntoRelease(*releasesRoot, *gitSHA, *legacyBundleDir); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: legacy-bundle-dir: %v\n", err)
			return 3
		}
		// Read + Verify (catalog check) run on the legacy path
		// too. A 0-row manifest is a hard error: copyBinIntoRelease
		// doesn't write a manifest, so the operator must pre-stage
		// release-manifest.json alongside the bin-dir (legacy
		// convention).
		m, err := releaseinstall.Read(*releasesRoot, *gitSHA)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: legacy read manifest: %v\n", err)
			return 3
		}
		if err := releaseinstall.Verify(*releasesRoot, m); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: legacy verify manifest: %v\n", err)
			return 3
		}
		if err := releaseinstall.AtomicFlip(*releasesRoot, *gitSHA); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: legacy flip symlink: %v\n", err)
			return 3
		}
		// role-flag handling on the legacy path is out of scope
		// for this commit; the legacy path is a stop-gap and the
		// operator is expected to re-install with --role after
		// they migrate to the canonical path.
		return 0
	}
	// ADR-112: if --role is empty AND /etc/faas/first-boot.env has
	// FAAS_BOX_ROLE set (the operator-supplied sentinel from
	// cloud-init user-data), adopt the raw value and let Validate
	// catch unknown / typo'd strings (post-#930 review Fix 5).
	// Unknown values used to be silently dropped here, leaving the
	// install to exit 0 with no role templating — a footgun.
	if *roleFlag == "" {
		if envRole, ok := readFirstBootRole(); ok {
			*roleFlag = envRole
		}
	}
	// Validate --role BEFORE any side-effects (no symlink flip if
	// role is bogus). Per the [[gregalectl-dispatch-manifest-
	// completeness]] lesson: stable exit codes; usage errors exit 2,
	// runtime errors exit ≥3.
	if *roleFlag != "" {
		if err := roleTemplating.Validate(roleTemplating.Role(*roleFlag)); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: %v\n", err)
			return 2
		}
	}
	// Verify the bundle on disk before flipping the symlink —
	// PR-4 doctor will do this same check; PR-3's install path
	// is just as strict, so a corrupted bundle never becomes the
	// active release.
	m, err := releaseinstall.Read(*releasesRoot, *gitSHA)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: read manifest: %v\n", err)
		return 3
	}
	if err := releaseinstall.Verify(*releasesRoot, m); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: verify manifest: %v\n", err)
		return 3
	}
	// ADR-113 commit 3: SBoM CVE-baseline gate. If the per-release
	// SBoM is present on disk (the canary post-process stamped it
	// there), parse it and run the regression check vs the prior
	// baseline.
	//
	// Three states, fail-closed posture (review finding #3):
	//
	//   (a) SBoM file absent  → skip gate (legacy operators continue).
	//   (b) SBoM file present but 0 bytes → skip gate with a log
	//       message (canary partial-failure mode — review finding #8:
	//       distinguish "not yet ready" from "malformed" so operators
	//       don't get a misleading parse error).
	//   (c) SBoM file present and parseable → run Diff vs baseline.
	//       Baseline MUST exist (fail-closed: missing baseline is
	//       ErrNilBaseline wrapped; the operator must run
	//       `release KGV rotate` from PR-B to accept the SBoM).
	//
	// The error message names the regression class so operators
	// can route the failure (roll back, rotate KGV, etc.).
	if sbomPath, sbomErr := sbomOnDiskPath(*releasesRoot, *gitSHA); sbomErr == nil {
		sbomBody, readErr := os.ReadFile(sbomPath)
		if readErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: read sbom: %v\n", readErr)
			return 3
		}
		if len(sbomBody) == 0 {
			// Review finding #8: 0-byte SBoM is the canary
			// partial-failure mode (canary wrote a file but
			// syft didn't finish). Skip the gate with a log
			// line so the operator doesn't see a misleading
			// 'malformed' error.
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: warning: SBoM is empty, skipping CVE-baseline gate (canary partial-failure; retry install once SBoM is non-empty)\n")
		} else {
			counts, parseErr := releaseinstall.ParseSPDXv2_3(sbomBody)
			if parseErr != nil {
				_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: parse sbom: %v\n", parseErr)
				return 3
			}
			baseline, baseErr := releaseinstall.ReadBaseline(*releasesRoot, *gitSHA)
			if baseErr != nil {
				// Review finding #3: missing baseline is
				// fail-closed. The operator must explicitly
				// accept the SBoM by writing a baseline
				// (PR-B's `release KGV rotate`); today the
				// operator can do it by running any prior
				// release once and rotating, OR by hand-
				// writing the baseline file. We surface the
				// underlying error to make the next step
				// obvious.
				if errors.Is(baseErr, releaseinstall.ErrNilBaseline) {
					_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: SBoM gate requires a prior baseline (none at %s).\n"+
						"  run: gregalectl release kgv rotate --git-sha %s [--from-zero]\n",
						releaseinstall.SBOMBaselinePath(releaseinstall.BundleRoot(*releasesRoot, *gitSHA)), *gitSHA)
					return 3
				}
				_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: read SBoM baseline at %s: %v\n",
					releaseinstall.SBOMBaselinePath(releaseinstall.BundleRoot(*releasesRoot, *gitSHA)), baseErr)
				return 3
			}
			if _, diffErr := baseline.Diff(counts); diffErr != nil {
				_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: %v\n", diffErr)
				return 3
			}
		}
	}
	currentBefore, currentBeforeErr := releaseinstall.CurrentGitSHA(*releasesRoot)
	if currentBeforeErr != nil {
		currentBefore = ""
	}
	if err := releaseinstall.AtomicFlip(*releasesRoot, *gitSHA); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: flip symlink: %v\n", err)
		return 3
	}
	// Open the DB pool BEFORE the role branch so PR-B's drain gate
	// and compute_nodes.role read can use it. The DB can fail
	// (no DSN, no row) — that's the legacy first-boot path; we
	// fall through to the env fallback. node is the canonical
	// compute_nodes.name for this box.
	openPool, dbErr := openPgPoolFromEnv(context.Background())
	if dbErr != nil {
		openPool = nil // legacy / --no-db mode
	} else {
		defer openPool.Close()
	}
	node := *nodeName
	if node == "" {
		// The daemon identity is deployment-owned and may differ from
		// the cloud-provider instance hostname (for example,
		// faas-compute-node-1 runs as fsn-2). Prefer the same env
		// contract consumed by vmmd/schedd before falling back to the
		// kernel hostname.
		node = strings.TrimSpace(os.Getenv("FAAS_NODE_NAME"))
		if node == "" {
			var herr error
			node, herr = os.Hostname()
			if herr != nil {
				node = "unknown"
			}
		}
	}
	// vmmd self-registration uses the TLS identity namespace and stores
	// compute-only nodes as <host>.faas (cmd/vmmd/register.go). Keep the
	// release-install writer on that same canonical key so a split-box
	// install updates the vmmd row instead of creating a short-name twin.
	// Control-plane rows are not rewritten: their names are operator-owned
	// and may describe a non-compute host identity.
	node = canonicalComputeNodeName(node, roleTemplating.Role(*roleFlag))
	// ADR-112: after the symlink flip (the load-bearing step),
	// apply role templating. The drop-ins + daemon-reload are
	// what materially makes FAAS_BOX_ROLE take effect on the box.
	//
	// PR-B (issue #935) extends `--role` to be the in-place role
	// mutation flag. On a running box with a different existing
	// role, the flow is: read current role from DB (or env fallback),
	// short-circuit on same-role, drain-gate on live instances,
	// then Mutate(stop old subset, start new subset) instead of
	// the blank-box Apply path. sealed.env / host.age / rclone.conf
	// / cosign keys / TLS leaves are NOT touched — the mutation is
	// purely a "what daemons run here" change.
	if *roleFlag != "" {
		target := roleTemplating.Role(*roleFlag)
		// Read the current role. PR-B prefers the DB (compute_nodes.role
		// by id) over the env fallback. The DB can fail (no DSN, no row)
		// — that's the legacy first-boot path; we fall through to env
		// or to the blank-box Apply.
		cnID, current := readCurrentRole(context.Background(), openPool, node)
		if current == target {
			// Idempotent re-run. No drop-in re-templating, no
			// daemon restarts, NO compute_nodes UPDATE — the
			// latter matters because every UPDATE fires the
			// compute_nodes trigger + pg_notify storm across the
			// cluster for no reason. Print a clear no-op line so
			// the operator sees their intent was honored.
			_, _ = fmt.Fprintf(os.Stderr,
				"gregalectl release install: already role=%s; no-op (sealed-env and daemons untouched, no DB write)\n",
				current)
		} else {
			// Drain gate. Hard block on live instances.
			if err := assertDrainStatus(context.Background(), openPool, node); err != nil {
				_, _ = fmt.Fprintf(os.Stderr,
					"gregalectl release install: %v\n"+
						"  run: gregalectl compute-nodes drain-status --node %s\n"+
						"  then: gregalectl compute-nodes force-drain --node %s --yes (operator override)\n",
					err, node, node)
				return 4
			}
			// Re-template drop-ins for the new role. Idempotent
			// write of identical bytes (the only thing that
			// differs is the FAAS_BOX_ROLE / FAAS_<DAEMON>_ROLE
			// values).
			if err := roleTemplating.ApplyFilesystem(target); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: apply role %s: %v\n", target, err)
				return 4
			}
			// Run the Mutate contract: stop the (from \ to) subset
			// in reverse dependency order with gatewayd-public last,
			// start the (to \ from) subset in forward dependency order.
			// Empty from (blank-box first-boot) or from == target
			// (idempotent) means no systemctl calls.
			stopped, started, err := roleTemplating.Mutate(current, target, systemctlExec)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr,
					"gregalectl release install: mutate role %s -> %s: %v\n",
					current, target, err)
				return 4
			}
			_, _ = fmt.Fprintf(os.Stderr,
				"gregalectl release install: re-rolled %s -> %s (stopped %v, started %v)\n",
				current, target, stopped, started)
			// PR-B (issue #935): stamp the post-mutation role on
			// compute_nodes.role keyed by id. Done HERE (inside the
			// else branch) so idempotent re-runs (current == target
			// above) do NOT fire a pg_notify storm across the cluster.
			// The runtime allow-list is narrower than the renderer's
			// (see pkg/state.pgstore docstring); a `single-box`
			// renderer write is fine but a runtime re-role to that
			// value is rejected — by design, post-#930.
			if cnID != "" {
				pgstore := state.NewPgStore(openPool)
				if err := pgstore.SetComputeNodeRole(context.Background(), cnID, string(target)); err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: set compute_nodes.role: %v\n", err)
					// Don't fail the install; the on-disk state is
					// the source of truth. Doctor will report the drift.
				}
			}
		}
	}
	// DB writes — best effort. The on-disk symlink flip is the
	// load-bearing side; the DB row records the audit trail and
	// first-write-wins mark. Reuse openPool (already opened above
	// for the PR-B role branch).
	if openPool == nil {
		if jsonEnabled() {
			jsonEmit(os.Stdout, releaseInstallReport{
				GitSHA:      *gitSHA,
				DBError:     "database DSN not set (FAAS_PG_DSN or DATABASE_URL)",
				SymlinkOnly: true,
			})
		} else {
			_, _ = fmt.Fprintf(os.Stdout, "flipped current -> %s (DB unreachable: database DSN not set)\n", *gitSHA)
		}
		return 3
	}
	store := releaseinstall.NewStore(openPool)
	first, err := store.MarkApplied(context.Background(), *gitSHA)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: mark applied: %v\n", err)
		return 3
	}
	// PR-6 (issue #911 / ADR-110): stamp the per-node release
	// membership on compute_nodes. The on-disk symlink flip is the
	// load-bearing side; the UPSERT is best-effort like the existing
	// MarkApplied write. Doctor (PR-4) reads release_id / manifest_hash
	// off this row to detect per-node drift across the cluster.
	cnID, cnErr := store.UpsertComputeNode(context.Background(), node, *gitSHA, m.ManifestHash)
	if cnErr != nil {
		if jsonEnabled() {
			jsonEmit(os.Stdout, releaseInstallReport{
				GitSHA:           *gitSHA,
				FirstApplied:     first,
				Node:             node,
				SymlinkOnly:      true,
				ComputeNodeError: cnErr.Error(),
			})
		} else {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: upsert compute_nodes: %v\n", cnErr)
		}
		return 3
	}
	if *deferActivation {
		// A newly installed node must not become schedulable merely
		// because its release is on disk. The provider-neutral join
		// pipeline starts services, runs readiness gates, and activates
		// this exact row as its final step. Use the returned UUID because
		// SetComputeNodeActive is intentionally id-keyed in state.Store.
		if err := state.NewPgStore(openPool).SetComputeNodeActive(context.Background(), cnID, false); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: defer activation: %v\n", err)
			return 3
		}
	}
	auditRecorded := false
	var auditErr string
	if err := recordReleaseInstallAudit(context.Background(), releaseinstall.NewDeploymentStore(openPool), *releasesRoot, m, currentBefore != *gitSHA, node, *reason); err != nil {
		auditErr = err.Error()
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: warning: record deployment history: %v\n", err)
	} else if currentBefore != *gitSHA {
		auditRecorded = true
	}
	if _, err := releaseretention.Prune(*releasesRoot, releaseinstall.CurrentSymlink(*releasesRoot), releaseretention.DefaultKeepPrevious); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release install: prune old releases: %v\n", err)
		return 3
	}
	// PR-B (issue #935): the post-mutation role write happens
	// INSIDE the role-branch else-block above (so idempotent re-runs
	// do NOT fire the compute_nodes UPDATE trigger + pg_notify storm).
	// Do not duplicate the write here.
	if jsonEnabled() {
		jsonEmit(os.Stdout, releaseInstallReport{
			GitSHA:        *gitSHA,
			FirstApplied:  first,
			Node:          node,
			ComputeNodeID: cnID,
			AuditRecorded: auditRecorded,
			AuditError:    auditErr,
		})
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "flipped current -> %s (first_applied=%v, node=%s, compute_node=%s)\n",
			*gitSHA, first, node, cnID)
	}
	return 0
}

// cmdReleaseHistory is the read-only operator view over the platform release
// ledger. It deliberately reads the database and the local current pointer in
// one command so an operator can spot a host that activated a release without
// producing the corresponding audit rows.
func cmdReleaseHistory(args []string) int {
	if len(args) > 0 && (args[0] == flagHelpLong || args[0] == flagHelpShort) {
		PrintUsage(os.Stderr, "usage: gregalectl release history [--daemon NAME] [--limit N]", "release")
		return 0
	}
	fs := flag.NewFlagSet("release history", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	daemon := fs.String("daemon", "", "filter to one daemon")
	limit := fs.Int("limit", 50, "maximum history rows (1-500)")
	releasesRoot := fs.String("releases-root", "/opt/faas/releases", "releases root directory")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(os.Stderr, "gregalectl release history: unexpected positional argument")
		return 1
	}
	if *limit < 1 || *limit > 500 {
		_, _ = fmt.Fprintln(os.Stderr, "gregalectl release history: --limit must be between 1 and 500")
		return 1
	}
	pool, err := openPgPoolFromEnv(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release history: %v\n", err)
		return 3
	}
	defer pool.Close()
	rows, err := releaseinstall.NewDeploymentStore(pool).List(context.Background(), *daemon, *limit)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release history: %v\n", err)
		return 3
	}
	current, currentErr := releaseinstall.CurrentGitSHA(*releasesRoot)
	report := releaseHistoryReport{
		CurrentRelease: current,
		Daemon:         strings.TrimSpace(*daemon),
		Deployments:    rows,
	}
	if currentErr != nil {
		report.CurrentError = currentErr.Error()
	}
	if jsonEnabled() {
		jsonEmit(os.Stdout, report)
		return 0
	}
	if report.CurrentRelease == "" {
		_, _ = fmt.Fprintln(os.Stdout, "current: <none>")
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "current: %s\n", report.CurrentRelease)
	}
	if report.CurrentError != "" {
		_, _ = fmt.Fprintf(os.Stdout, "current pointer: error: %s\n", report.CurrentError)
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "no daemon deployment history")
		return 0
	}
	_, _ = fmt.Fprintln(os.Stdout, "deployed_at                 daemon             commit_sha                                status        kind       deployed_by")
	for _, row := range rows {
		_, _ = fmt.Fprintf(os.Stdout, "%-27s %-18s %-40s %-13s %-10s %s\n",
			row.DeployedAt.UTC().Format(time.RFC3339), row.Daemon, row.CommitSHA,
			row.Status, row.DeployKind, row.DeployedBy)
	}
	return 0
}

// cmdReleaseInspect combines the durable release_bundles row with the local
// manifest and current symlink. It is intentionally read-only and remains
// useful even when no daemon deployment rows exist yet (for example, during a
// migration rollout).
func cmdReleaseInspect(args []string) int {
	if len(args) > 0 && (args[0] == flagHelpLong || args[0] == flagHelpShort) {
		PrintUsage(os.Stderr, "usage: gregalectl release inspect SHA [--releases-root PATH]", "release")
		return 0
	}
	fs := flag.NewFlagSet("release inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	releasesRoot := fs.String("releases-root", "/opt/faas/releases", "releases root directory")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(os.Stderr, "gregalectl release inspect: exactly one release SHA is required")
		return 1
	}
	gitSHA := fs.Arg(0)
	if !releaseinstall.ValidGitSHA(gitSHA) {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release inspect: %q is not a 40-character lowercase git SHA\n", gitSHA)
		return 1
	}
	pool, err := openPgPoolFromEnv(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release inspect: %v\n", err)
		return 3
	}
	defer pool.Close()
	row, err := releaseinstall.NewStore(pool).GetByGitSHA(context.Background(), gitSHA)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release inspect: %v\n", err)
		return 3
	}
	report := releaseInspectReport{Bundle: row}
	report.CurrentRelease, err = releaseinstall.CurrentGitSHA(*releasesRoot)
	if err != nil {
		report.CurrentError = err.Error()
	}
	report.IsCurrent = report.CurrentError == "" && report.CurrentRelease == gitSHA
	report.OnDisk, err = releaseinstall.IsBundleOnDisk(*releasesRoot, gitSHA)
	if err != nil {
		report.OnDiskError = err.Error()
	} else if report.OnDisk {
		manifest, readErr := releaseinstall.Read(*releasesRoot, gitSHA)
		if readErr != nil {
			report.ManifestError = readErr.Error()
		} else {
			report.Manifest = &manifest
		}
	}
	if jsonEnabled() {
		jsonEmit(os.Stdout, report)
		return 0
	}
	_, _ = fmt.Fprintf(os.Stdout, "release: %s\nmanifest_hash: %s\napplied_at: %s\non_disk: %t\ncurrent: %t\n",
		row.GitSHA, row.ManifestHash, formatOptionalTime(row.AppliedAt), report.OnDisk, report.IsCurrent)
	if report.CurrentError != "" {
		_, _ = fmt.Fprintf(os.Stdout, "current pointer: error: %s\n", report.CurrentError)
	}
	if report.OnDiskError != "" {
		_, _ = fmt.Fprintf(os.Stdout, "on-disk: error: %s\n", report.OnDiskError)
	}
	if report.ManifestError != "" {
		_, _ = fmt.Fprintf(os.Stdout, "manifest: error: %s\n", report.ManifestError)
	}
	return 0
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return "<never>"
	}
	return value.UTC().Format(time.RFC3339)
}

func recordReleaseInstallAudit(ctx context.Context, store releaseinstall.DeploymentStore, releasesRoot string, manifest releaseinstall.Manifest, changed bool, node, reason string) error {
	if !changed {
		return nil
	}
	actor := operatorIdentity()
	sbomHash := ""
	if body, err := os.ReadFile(filepath.Join(releaseinstall.BundleRoot(releasesRoot, manifest.GitSHA), releaseSBOMName)); err == nil && len(body) > 0 {
		digest := sha256.Sum256(body)
		sbomHash = "sha256:" + hex.EncodeToString(digest[:])
	}
	daemons := make([]string, 0, len(manifest.DaemonHashes))
	for daemon := range manifest.DaemonHashes {
		daemons = append(daemons, daemon)
	}
	sort.Strings(daemons)
	records := make([]releaseinstall.DeploymentRecord, 0, len(daemons))
	for _, daemon := range daemons {
		records = append(records, releaseinstall.DeploymentRecord{
			Daemon: daemon, Version: manifest.DaemonHashes[daemon], CommitSHA: manifest.GitSHA,
			SignedBy: manifest.Signature, SBOMSHA256: sbomHash, DeployedBy: actor,
			DeployKind: releaseinstall.DeploymentInstall,
			Notes:      map[string]any{"node": node, "reason": reason, "source": "gregalectl release install"},
		})
	}
	return store.RecordSucceeded(ctx, records)
}

func operatorIdentity() string {
	for _, key := range []string{"SUDO_USER", "USER", "LOGNAME"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "gregalectl"
}

// canonicalComputeNodeName returns the database identity used by vmmd for a
// compute-only box. It is intentionally narrow: control-plane names are
// operator-owned and must not be rewritten by release installation.
func canonicalComputeNodeName(name string, role roleTemplating.Role) string {
	if role != roleTemplating.RoleComputeOnly || strings.HasSuffix(name, ".faas") {
		return name
	}
	return name + ".faas"
}

// sbomOnDiskPath returns the canonical SBoM-on-disk path for the
// per-release bundle. Returns a non-nil error when the file does
// not exist (os.IsNotExist) so the caller can treat absence as
// "skip the gate" (the legacy on-disk flow is SBoM-less by
// definition — the canary path is what writes the SBoM).
//
// ADR-113 PR-A commit 3 keeps the SBoM-on-disk shape opt-in:
// legacy operators (no canary, no `make build-sha256`) continue
// to install without the gate. PR-B makes the gate mandatory for
// any release shipped via the canary.
func sbomOnDiskPath(releasesRoot, gitSHA string) (string, error) {
	p := filepath.Join(releaseinstall.BundleRoot(releasesRoot, gitSHA), "release.sbom.json")
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

// installViaTarball is the canonical ADR-113 install path. It
// loads the canonical triple (tarball + cosign bundle + SBoM) from
// the on-disk path the operator supplies via --tarball-path, runs
// the cosign verifier (the load-bearing trust bit from PR-A
// commit 2), extracts the tarball, and writes the verified release
// triple to <releases-root>/<git-sha>/. The caller performs the final
// on-disk verification, SBoM gate, role validation, and atomic flip.
//
// Review finding #1: this is the production caller of
// releaseinstall.Tarball.Verify. Without this function the
// cosign verifier (cosign.go) is whitebox-only — issue #597's
// "verify on the host before install" never actually verifies.
//
// On the production host the cosign binary is on PATH (the Packer
// image bakes it; ADR-113 cluster table). For air-gap boxes the
// flag's complement is --tarball-path pointing at a pre-staged
// triple on the operator's media.
func installViaTarball(releasesRoot, gitSHA, tarballPath string) error {
	return installViaTarballWithVerifier(
		releasesRoot,
		gitSHA,
		tarballPath,
		releaseinstall.NewExecCosignVerifier(releaseinstall.DefaultGitHubOIDC()),
	)
}

// installViaTarballWithVerifier is split from installViaTarball so the
// complete extraction + retention path can be tested without weakening the
// production default, which always uses the strict GitHub OIDC verifier.
func installViaTarballWithVerifier(releasesRoot, gitSHA, tarballPath string, verifier releaseinstall.CosignVerifier) error {
	// 1. Read the on-disk triple.
	tbBody, err := os.ReadFile(tarballPath)
	if err != nil {
		return fmt.Errorf("read tarball: %w", err)
	}
	sigBody, err := readCanonicalReleaseAsset(tarballPath, releaseSigName)
	if err != nil {
		return fmt.Errorf("read cosign bundle: %w", err)
	}
	sbomBody, err := readCanonicalReleaseAsset(tarballPath, releaseSBOMName)
	if err != nil {
		return fmt.Errorf("read sbom: %w", err)
	}

	// 2. Build the Tarball. Commit 1's BuildTarball needs a bin
	// dir on disk; the canonical path doesn't have one yet (the
	// bin dir is what we're about to extract into). Construct
	// the Tarball directly with the loaded bytes — the manifest
	// is embedded in the tarball's release-manifest.json member
	// and the per-binary sha256 walk in Tarball.Verify uses the
	// tarball bytes, not the bin dir.
	manifestBytes, err := extractTarballMember(tbBody, "release-manifest.json")
	if err != nil {
		return fmt.Errorf("extract manifest: %w", err)
	}
	var m releaseinstall.Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if err := releaseinstall.ValidateManifest(m); err != nil {
		return fmt.Errorf("validate manifest: %w", err)
	}
	if m.GitSHA != gitSHA {
		return fmt.Errorf("manifest git_sha=%s does not match --git-sha=%s", m.GitSHA, gitSHA)
	}
	tb := &releaseinstall.Tarball{
		GitSHA:     gitSHA,
		Manifest:   m,
		Packed:     tbBody,
		Sig:        sigBody,
		SBOM:       sbomBody,
		BinSHA256:  manifestHashMap(m.DaemonHashes),
		ToolSHA256: manifestHashMap(m.ToolHashes),
	}

	// 3. Cosign verify-blob. THIS is the load-bearing trust bit
	// from PR-A commit 2 (and the line item closing issue #597).
	// On a successful verify the cert identity is stamped onto
	// tb.Manifest.Signature for audit trails.
	if _, err := tb.Verify(context.Background(), verifier); err != nil {
		return fmt.Errorf("tarball verify: %w", err)
	}
	// Verify stamps the trusted CI identity onto the manifest. Persist that
	// identity with the extracted release so `doctor` can audit which CI
	// workflow authorized the bytes currently installed on the host.
	m = tb.Manifest

	// 4. Extract the tarball into <releases-root>/<git-sha>/bin/.
	// On success the on-disk bin tree matches the manifest's
	// per-binary sha256.
	if err := tb.Extract(releasesRoot); err != nil {
		return fmt.Errorf("tarball extract: %w", err)
	}

	// 5. Write the manifest + the SBoM to the canonical
	// on-disk locations so the rest of the install (Verify,
	// SBoM gate, doctor) sees the same shape as the
	// pre-canonical path.
	if err := releaseinstall.Write(releasesRoot, m); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	// Retain the exact verified release triple with the extracted
	// release. Apart from making the install self-describing, this is
	// what lets `gregalectl doctor --deep` repeat the cosign + SBoM
	// verification without relying on an operator's staging directory.
	assets := map[string][]byte{
		releaseTarballName: tbBody,
		releaseSigName:     sigBody,
		releaseSBOMName:    sbomBody,
	}
	for name, body := range assets {
		if err := os.WriteFile(filepath.Join(releaseinstall.BundleRoot(releasesRoot, gitSHA), name), body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	// The caller performs the final on-disk Verify, SBoM gate, role
	// validation, and AtomicFlip. Keeping the flip outside this staging
	// helper is essential: a post-extraction catalog or policy failure must
	// never activate a partially prepared release.
	return nil
}

// readCanonicalReleaseAsset reads an asset staged next to release.tar.gz,
// which is the layout emitted by the release workflow. The appended
// sidecar spelling is accepted as a compatibility path for older air-gap
// staging scripts that used release.tar.gz.cosign.bundle and
// release.tar.gz.sbom.json. The canonical sibling name is preferred.
func readCanonicalReleaseAsset(tarballPath, name string) ([]byte, error) {
	candidates := []string{
		filepath.Join(filepath.Dir(tarballPath), name),
		tarballPath + "." + strings.TrimPrefix(name, "release."),
	}
	var lastErr error
	for _, path := range candidates {
		body, err := os.ReadFile(path)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !errors.Is(err, os.ErrNotExist) {
			break
		}
	}
	return nil, fmt.Errorf("%s (tried %s): %w", name, strings.Join(candidates, ", "), lastErr)
}

// manifestHashMap converts manifest values from "sha256:<hex>" to the
// hashWalk representation used by Tarball. The shape was deliberately kept
// separate from the on-disk manifest so the verifier never has to infer a
// digest format from a tar entry.
func manifestHashMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for name, value := range values {
		out[name] = strings.TrimPrefix(value, "sha256:")
	}
	return out
}

// extractTarballMember pulls one regular-file member out of a
// tar+gz blob without unpacking the rest. Used by
// installViaTarball to fetch release-manifest.json before doing
// the full extract.
func extractTarballMember(packed []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar next: %w", err)
		}
		if hdr.Name == name && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("member %q not found in tarball", name)
}

// openPgPoolFromEnv returns a pgxpool.Pool wired from the operator's
// database environment. FAAS_PG_DSN is the explicit CLI override;
// DATABASE_URL is the name emitted by the control-plane sealed.env
// and is therefore the production fallback. Returns an error when
// neither variable is set.
func openPgPoolFromEnv(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("FAAS_PG_DSN")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		// Split compute boxes deliberately keep DATABASE_URL in a
		// root-only systemd EnvironmentFile. Make the operator CLI
		// honor that deployment contract when invoked as root, while
		// gracefully ignoring the file for ordinary local users.
		for _, path := range []string{"/etc/faas/compute-db.env", "/etc/faas/sealed.env"} {
			if v, ok := readDatabaseEnvFile(path); ok {
				dsn = v
				break
			}
		}
	}
	if dsn == "" {
		return nil, fmt.Errorf("database DSN not set (FAAS_PG_DSN or DATABASE_URL)")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database DSN: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pgxpool: %w", err)
	}
	return pool, nil
}

// readDatabaseEnvFile reads only DATABASE_URL from a systemd-style env file.
// The file contents stay in memory; callers receive only the parsed value.
// This is intentionally small rather than a shell parser: deploy-managed
// files contain KEY=value lines, comments, and optional single/double quotes.
func readDatabaseEnvFile(path string) (string, bool) {
	// path is the deploy-managed systemd environment file selected by the
	// local release-install flow, not a customer upload path.
	//nolint:forbidigo // vetted deploy-managed environment file.
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "DATABASE_URL" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
		} else if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		}
		if value != "" {
			return value, true
		}
	}
	return "", false
}

// readFirstBootRole parses /etc/faas/first-boot.env (the file the
// cloud-init user-data wrote at server-create time, ADR-112) and
// returns the FAAS_BOX_ROLE value (raw, unvalidated). Returns
// ok=false ONLY when the file is missing OR FAAS_BOX_ROLE is unset
// OR its value is the operator-override sentinel
// `__SET_BY_OPERATOR_AT_LAUNCH__` (the marker the cloud-init
// runcmd's assert-first-boot-env.sh detects and fails loud on).
//
// UNKNOWN values (typos like "control-plan") are returned with
// ok=true so cmdReleaseInstall's Validate() surfaces the error.
// Pre-Fix-5 the function returned ("", false) for unknowns, leaving
// `*roleFlag == ""` and silently skipping role templating — the
// install exited 0 with no daemons templated, which is the worst
// possible failure mode. The post-Fix-5 contract: be loud about
// unknowns, fall back to legacy-only on absent/sentinel.
func readFirstBootRole() (string, bool) {
	const path = "/etc/faas/first-boot.env"
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "FAAS_BOX_ROLE=") {
			continue
		}
		value := strings.TrimPrefix(line, "FAAS_BOX_ROLE=")
		value = strings.Trim(value, "\"'")
		if value == "" || value == "__SET_BY_OPERATOR_AT_LAUNCH__" {
			return "", false
		}
		return value, true
	}
	return "", false
}

// readCurrentRole resolves the box's current role (ADR-112 PR-B).
// Returns the compute_nodes.id alongside the role so the caller
// can re-use it for the post-mutation SetComputeNodeRole write
// (pkg/state keys by id, not by name — the renderer side keys by
// name, but the runtime path is id-keyed). The id is the same row
// that the existing UpsertComputeNode returns later in
// cmdReleaseInstall, so callers can pass it through to
// SetComputeNodeRole directly and skip the duplicate lookup.
//
// Source-of-truth ordering:
//
//  1. compute_nodes.role via pgxpool (DB truth).
//  2. FAAS_BOX_ROLE from /etc/faas/first-boot.env (legacy
//     first-boot path, no DB row yet).
//  3. Empty Role, empty id — "blank-box first-boot" (no current
//     role to compare against). The caller runs the blank-box
//     Apply path (PR-A behaviour, unchanged).
//
// All failure modes are silent-by-design: PR-B is best-effort.
// The DB read errors are treated as "no row" — the env fallback
// kicks in. The env fallback returning "" is the blank-box path.
func readCurrentRole(ctx context.Context, pool *pgxpool.Pool, nodeName string) (cnID string, current roleTemplating.Role) {
	if pool != nil && nodeName != "" {
		var id, role *string
		err := pool.QueryRow(ctx,
			`SELECT id, role FROM compute_nodes WHERE name = $1`, nodeName).Scan(&id, &role)
		if err == nil && id != nil {
			cnID = *id
			if role != nil && *role != "" {
				return cnID, roleTemplating.Role(*role)
			}
			return cnID, "" // row exists, role unset — env fallback below
		}
	}
	if envRole, ok := readFirstBootRole(); ok && envRole != "" {
		return "", roleTemplating.Role(envRole)
	}
	return "", ""
}

// assertDrainStatus fails PR-B's drain gate. Returns nil if the
// node has no live instances (drain-safe), or a loud error pointing
// at the operator-override path otherwise.
//
// Walks the same SQL as cmdComputeNodesDrainStatus (the dedicated
// compute-nodes subcommand) for parity, but inlined here so the
// gate is a single function call without forking the CLI. The
// Postgres query counts rows in (WAKING, COLD_BOOTING, RUNNING)
// keyed by the node_id column on the instances table.
//
// Three terminal states, all disambiguated explicitly:
//
//  1. compute_nodes row MISSING for this name — return an explicit
//     error. The previous shape used
//     `WHERE node_id = (SELECT id ...)` which evaluates the
//     subquery to NULL and the predicate to UNKNOWN (not FALSE),
//     silently treating "no row" as "zero live instances" and
//     bypassing the gate. Fail-closed is the right default: a
//     half-mutated box whose row was deleted out-of-band must NOT
//     re-role without operator acknowledgement.
//  2. DB error reading the row or the count — fail-closed (block
//     the re-role).
//  3. live > 0 — return a loud error pointing at force-drain.
//
// The legacy first-boot path (no DB / empty node name) keeps its
// pass-through behaviour because there is literally no compute_nodes
// row to disambiguate against.
func assertDrainStatus(ctx context.Context, pool *pgxpool.Pool, nodeName string) error {
	if pool == nil || nodeName == "" {
		return nil // no DB / no node: skip the gate (legacy first-boot)
	}
	// 1. Resolve the row by name. ErrNoRows is treated as an explicit
	// gate failure, not as "zero live instances".
	var cnID string
	err := pool.QueryRow(ctx,
		`SELECT id FROM compute_nodes WHERE name = $1`, nodeName).Scan(&cnID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("drain gate: compute_nodes row missing for name %q; cannot verify live instances (refusing to re-role)", nodeName)
		}
		return fmt.Errorf("drain gate: cannot read compute_nodes row: %w", err)
	}
	// 2. Count live instances keyed by id (the safe join shape — no
	// NULL coercion of the subquery).
	var live int
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM instances
		 WHERE node_id = $1
		   AND state IN ('WAKING', 'COLD_BOOTING', 'RUNNING')
	`, cnID).Scan(&live)
	if err != nil {
		return fmt.Errorf("drain gate: cannot read live-instance count: %w", err)
	}
	if live > 0 {
		return fmt.Errorf("node %q has %d live instances; re-role would kill them mid-request", nodeName, live)
	}
	return nil
}

// systemctlExec is the production execCommand adapter for
// roleTemplating.Mutate. Returns the combined stdout+stderr string
// (so Mutate's error path can include systemctl's diagnostic
// output) or an error. Mutate swallows the output on the success
// path; the error path's string is what the operator sees.
func systemctlExec(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// releaseBundleReport is the JSON wire shape for `gregalectl release
// bundle --json`.
type releaseBundleReport struct {
	GitSHA       string `json:"git_sha"`
	ManifestHash string `json:"manifest_hash"`
	ManifestPath string `json:"manifest_path,omitempty"`
	ID           string `json:"id,omitempty"`
	DBError      string `json:"db_error,omitempty"`
}

// releaseInstallReport is the JSON wire shape for `gregalectl release
// install --json`.
type releaseInstallReport struct {
	GitSHA       string `json:"git_sha"`
	FirstApplied bool   `json:"first_applied"`
	Node         string `json:"node,omitempty"`
	SymlinkOnly  bool   `json:"symlink_only,omitempty"`
	DBError      string `json:"db_error,omitempty"`
	// PR-6: compute_nodes UPSERT result. ComputeNodeID is the row id
	// from gen_random_uuid(); ComputeNodeError surfaces best-effort
	// UPSERT failures without dropping the load-bearing symlink flip.
	ComputeNodeID    string `json:"compute_node_id,omitempty"`
	ComputeNodeError string `json:"compute_node_error,omitempty"`
	AuditRecorded    bool   `json:"audit_recorded,omitempty"`
	AuditError       string `json:"audit_error,omitempty"`
}

type releaseHistoryReport struct {
	CurrentRelease string                         `json:"current_release,omitempty"`
	CurrentError   string                         `json:"current_error,omitempty"`
	Daemon         string                         `json:"daemon,omitempty"`
	Deployments    []releaseinstall.DeploymentRow `json:"deployments"`
}

type releaseInspectReport struct {
	Bundle         releaseinstall.BundleRow `json:"bundle"`
	CurrentRelease string                   `json:"current_release,omitempty"`
	CurrentError   string                   `json:"current_error,omitempty"`
	IsCurrent      bool                     `json:"is_current"`
	OnDisk         bool                     `json:"on_disk"`
	OnDiskError    string                   `json:"on_disk_error,omitempty"`
	Manifest       *releaseinstall.Manifest `json:"manifest,omitempty"`
	ManifestError  string                   `json:"manifest_error,omitempty"`
}

// _ keeps bytes imported even if the future JSON marshaller is
// reformatted; avoids the "imported and not used" lint when
// callers swap to streaming JSON.
var _ = bytes.NewBuffer
