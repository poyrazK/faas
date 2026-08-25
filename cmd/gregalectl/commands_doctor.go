// commands_doctor.go — operator-side cluster-shipped release
// diagnostic (issue #911 / ADR-110 PR-4).
//
// `gregalectl doctor` is the load-bearing read-only diagnostic that
// verifies the cluster-shipped release bundle is consistent across
// three surfaces: the on-disk release tree at /opt/faas/releases,
// the release_bundles table, and the compute_nodes table.
//
// It NEVER writes anything. No symlink flip, no UPSERT, no DB
// mutation. The output is a structured report (text or --json) plus
// a process exit code:
//
//	0  healthy (no error findings, or only warnings below fail-on)
//	1  usage error (bad flag, mutually-exclusive flag combo)
//	3  drift detected (per UX §3.2 platform/infra)
//
// The six checks run in order:
//
//	symlink         /opt/faas/current read; missing / broken / stale
//	bundle          manifest on disk + Verify against bin/
//	lockstep        manifest.daemon_hashes == catalog daemon count
//	nodes           per-node release_id / manifest_hash drift
//	bundle-orphans  unapplied release_bundles rows whose bin/ is gone
//	node-hashes     --deep only: per-node re-hash against the bundle
//
// The DB is optional for checks 1-3 (omitted database DSN emits a
// warn finding and skips checks 4-5). --deep requires the DB.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// doctorFindingsCap caps the number of findings emitted per run.
// Anything beyond is dropped and replaced with a single error
// finding telling the operator to narrow the filter. Loose
// upper bound — a fleet of 50 boxes with 6 checks each produces
// well under 100, but a misconfigured PR-3 install could
// trivially fan out 10× that.
const doctorFindingsCap = 1000

// Check name constants. The same name appears as the JSON field
// value and as the label in the per-check roll-up, so each one
// is a single const that's referenced everywhere. Matches the
// patterns in commands_release.go (subReleaseBundle etc.) and
// keeps goconst quiet.
const (
	doctorCheckSymlink       = "symlink"
	doctorCheckBundle        = "bundle"
	doctorCheckLockstep      = "lockstep"
	doctorCheckNodes         = "nodes"
	doctorCheckBundleOrphans = "bundle-orphans"
	doctorCheckNodeHashes    = "node-hashes"
	doctorCheckDB            = "db"
	doctorCheckTruncate      = "truncate"
	// PR-X (issue #911 / ADR-110): doctor's secrets check verifies
	// the on-disk host.age/box-age-key/session.key/rclone.conf/
	// archive-creds posture matches the row's stored cert_fingerprint.
	// Walks fs only — does NOT require a DB connection (the DB is
	// consulted only when the on-disk side already passes; a
	// missing file is a finding regardless).
	doctorCheckSecrets = "secrets"
	// PR-B (ADR-113 day-2): verify-tarball-sbom walks the on-disk
	// canonical triple (release.tar.gz + release.cosign.bundle +
	// release.sbom.json) per release and runs cosign verify-blob +
	// the SBoM CVE-baseline diff. Deep-only because the verifier
	// shells out to cosign; legacy operators (no canary) see a
	// clean "triple missing" warn-finding and continue.
	doctorCheckVerifyTarballSBOM = "verify-tarball-sbom"
	// Issue #938 / PR-B / ADR-114: verify the on-disk builder base
	// ext4 contains /usr/local/bin/faas-guest-init (the kernel-cmdline
	// PID1 binary every builder microVM hands control to). Walks the
	// ext4 via debugfs when available; emits a warn-finding on macOS /
	// non-debugfs hosts. A debugfs-found-but-file-absent case is
	// SeverityError because the build pipeline silently produced a
	// wrong rootfs — fail closed so the operator sees it before imaged
	// tries to spawn a builder VM. Path is configurable via
	// FAAS_BUILDER_BASE_PATH (mirroring cmd/imaged/main.go:403); empty
	// keeps the canonical /srv/fc/base/runner-builder-<arch>.ext4.
	doctorCheckBuilderBaseExt4 = "builder-base-ext4"
)

// doctor severity constants. Match the wire shape so JSON consumers
// don't have to translate.
const (
	doctorSeverityOK    = "ok"
	doctorSeverityWarn  = "warn"
	doctorSeverityError = "error"
)

// doctor check status constants. Distinct from severity because
// "skipped" (DB down + --deep) is neither ok nor error.
const (
	doctorStatusOK      = "ok"
	doctorStatusWarn    = "warn"
	doctorStatusError   = "error"
	doctorStatusSkipped = "skipped"
)

// fail-on threshold constants. UX §3.2 + ADR-110 §63 use the
// canonical "warn" / "error" labels.
const (
	doctorFailOnWarn  = "warn"
	doctorFailOnError = "error"
)

// validGitSHAPattern lives in pkg/releaseinstall as ValidGitSHA. The
// doctor delegates to it so the per-node release_id check stays in
// one place (mirrors the DB CHECK constraint from migration 00272).

// doctorFinding is one row in the JSON report. The check name ties
// the row to a check; severity is from the {ok, warn, error} closed
// set. target is the per-finding object (node name, git_sha, etc.)
// and is omitted when the finding is a global one (e.g. "DB down").
type doctorFinding struct {
	Check    string `json:"check"`
	Severity string `json:"severity"`
	Target   string `json:"target,omitempty"`
	Message  string `json:"message"`
	Detail   string `json:"detail,omitempty"`
}

// doctorCounts is the headline summary. Total = OK + Warn + Error.
type doctorCounts struct {
	OK    int `json:"ok"`
	Warn  int `json:"warn"`
	Error int `json:"error"`
	Total int `json:"total"`
}

// doctorCheckSum is one per-check roll-up. Notes is the number of
// findings (of any severity) attributed to that check.
type doctorCheckSum struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Notes      int    `json:"notes"`
}

// doctorReport is the full JSON wire shape. Designed to be round-
// trippable by apid admin observers (PR-X) and downstream tooling.
type doctorReport struct {
	ReleasesRoot  string           `json:"releases_root"`
	NodeFilter    string           `json:"node_filter,omitempty"`
	ReleaseFilter string           `json:"release_filter,omitempty"`
	StartedAt     time.Time        `json:"started_at"`
	FinishedAt    time.Time        `json:"finished_at"`
	Healthy       bool             `json:"healthy"`
	Counts        doctorCounts     `json:"counts"`
	Findings      []doctorFinding  `json:"findings"`
	Checks        []doctorCheckSum `json:"checks"`
}

// doctorDeps is the cross-check bundle. checkSymlink writes
// currentGitSHA so the downstream checks can compare against the
// active release without re-reading the symlink. bundlesBySHA is
// populated once by runDoctorChecks when the store is wired, so
// the DB-touching checks (checkNodes + checkNodeHashes) share a
// single SELECT instead of issuing one per node.
type doctorDeps struct {
	releasesRoot  string
	nodeFilter    string
	releaseFilter string
	deep          bool
	store         releaseinstall.Store
	// currentGitSHA is set by checkSymlink (the first check) and
	// read by checkBundle. nil-safe via the doctor.medianScoped
	// pattern — checks that don't need it just ignore it.
	currentGitSHA string
	// bundlesBySHA is keyed by release_bundles.git_sha. Populated
	// lazily by the first DB check that needs it; nil means
	// "not yet loaded". The map's value carries the row's
	// manifest_hash so the deep check can do a single map
	// lookup per node (no N+1 SELECT).
	bundlesBySHA map[string]releaseinstall.BundleRow
	// verifier is the cosign verifier (PR-B / ADR-113 day-2)
	// used by the verify-tarball-sbom probe. Lazily constructed
	// in cmdDoctorDispatch via releaseinstall.NewExecCosignVerifier
	// (production) or held nil for tests that inject a Fixture.
	// Field is on doctorDeps so it can be swapped in whitebox
	// tests without monkey-patching the package globals.
	verifier releaseinstall.CosignVerifier
}

// cmdDoctorDispatch is the entry point for `gregalectl doctor`.
// Mirrors commands_release.go's dispatcher shape: empty args
// prints the usage block and returns 1; flag.Parse handles the
// remainder.
func cmdDoctorDispatch(args []string) int {
	if len(args) > 0 && (args[0] == flagHelpLong || args[0] == flagHelpShort) {
		printDoctorUsage(os.Stderr)
		return 0
	}
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	nodeFilter := fs.String("node", "", "compute_nodes.name filter (default: all)")
	releaseFilter := fs.String("release", "", "release_bundles.git_sha filter (default: all)")
	releasesRoot := fs.String("releases-root", "/opt/faas/releases", "releases root directory")
	deep := fs.Bool("deep", false, "re-hash on-disk daemons per-node (slow on large fleets)")
	failOn := fs.String("fail-on", doctorFailOnError, "exit non-zero threshold: warn | error (default error)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	switch *failOn {
	case doctorFailOnWarn, doctorFailOnError:
		// ok
	default:
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl doctor: --fail-on=%q is not warn|error\n", *failOn)
		return 1
	}

	deps := &doctorDeps{
		releasesRoot:  *releasesRoot,
		nodeFilter:    *nodeFilter,
		releaseFilter: *releaseFilter,
		deep:          *deep,
	}

	// Open the DB pool lazily so checks 1-3 can run without one.
	// Doctor must work on a fresh box with no DB reachable.
	pool, dbErr := openPgPoolFromEnv(context.Background())
	if dbErr != nil {
		if *deep {
			// Deep requires DB (check 6 walks ListComputeNodes).
			report := doctorReport{
				ReleasesRoot: deps.releasesRoot,
				NodeFilter:   deps.nodeFilter,
				StartedAt:    time.Now(),
				Healthy:      false,
				Counts:       doctorCounts{},
				Findings: []doctorFinding{{
					Check:    doctorCheckDB,
					Severity: doctorSeverityError,
					Message:  "database DSN not set; --deep requires the DB",
					Detail:   dbErr.Error(),
				}},
				Checks: []doctorCheckSum{},
			}
			report.FinishedAt = time.Now()
			report.Counts.Error = 1
			report.Counts.Total = 1
			emitDoctorReport(os.Stdout, report)
			return 3
		}
		// Without DB, the store is nil and the DB checks short-circuit.
	} else {
		defer pool.Close()
		deps.store = releaseinstall.NewStore(pool)
		// Pre-load the release_bundles table once so the
		// DB-touching checks (nodes + bundle-orphans + node-hashes)
		// share a single SELECT instead of issuing one per node.
		// Loaded here (not in each check) so all three reads
		// stay consistent — a concurrent INSERT between
		// ListAllBundles and ListComputeNodes would otherwise
		// produce a false-positive orphan-release_id finding.
		bundles, err := deps.store.ListAllBundles(context.Background())
		if err == nil {
			deps.bundlesBySHA = make(map[string]releaseinstall.BundleRow, len(bundles))
			for _, b := range bundles {
				deps.bundlesBySHA[b.GitSHA] = b
			}
		}
	}

	// PR-B (ADR-113 day-2): build the cosign verifier lazily. The
	// ExecCosignVerifier holds no state and is safe to share across
	// releases; DefaultGitHubOIDC() pins the issuer + identity regex
	// from PR-A. Whitebox tests inject a FixtureCosignVerifier via
	// deps.verifier before runDoctorChecks is called.
	if deps.verifier == nil {
		deps.verifier = releaseinstall.NewExecCosignVerifier(releaseinstall.DefaultGitHubOIDC())
	}

	report := runDoctorChecks(context.Background(), deps)
	emitDoctorReport(os.Stdout, report)

	switch *failOn {
	case doctorFailOnWarn:
		if report.Counts.Error+report.Counts.Warn > 0 {
			return 3
		}
	case doctorFailOnError:
		if report.Counts.Error > 0 {
			return 3
		}
	}
	return 0
}

func printDoctorUsage(w io.Writer) {
	_, _ = fmt.Fprintf(w, `usage: gregalectl doctor [flags]

Reads the cluster-shipped release bundle's three surfaces and
reports drift. NEVER writes; safe to run on production.

Surfaces:
  on-disk       /opt/faas/releases/<git-sha>/bin + manifest
  release_bundles  the per-release INSERT row + applied_at
  compute_nodes    per-node release_id + manifest_hash

Flags:
  --node NAME           compute_nodes.name filter (default: all)
  --release SHA         release_bundles.git_sha filter (default: all)
  --releases-root PATH  releases root (default /opt/faas/releases)
  --deep                re-hash on-disk daemons per-node (slow)
  --fail-on MODE        exit non-zero threshold: warn | error (default error)

Exit codes:
  0  no drift (or only warnings below --fail-on)
  1  usage error
  3  drift detected (per UX §3.2 platform/infra)

Examples:
  gregalectl doctor
  gregalectl doctor --node=test-node
  gregalectl doctor --release=0123456789abcdef0123456789abcdef01234567
  gregalectl doctor --deep --json
`)
	_, _ = fmt.Fprintf(w, "  Docs: %sdoctor\n", docsURLBase)
}

// runDoctorChecks drives the six checks in order and gathers
// findings + per-check summaries. Order matters: checkSymlink
// populates deps.currentGitSHA for checkBundle.
func runDoctorChecks(ctx context.Context, deps *doctorDeps) doctorReport {
	startedAt := time.Now()
	report := doctorReport{
		ReleasesRoot:  deps.releasesRoot,
		NodeFilter:    deps.nodeFilter,
		ReleaseFilter: deps.releaseFilter,
		StartedAt:     startedAt,
		Counts:        doctorCounts{},
		Findings:      []doctorFinding{},
		Checks:        []doctorCheckSum{},
	}

	runCheck := func(name string, fn func() ([]doctorFinding, error)) {
		t := time.Now()
		findings, err := fn()
		dur := time.Since(t)
		if err != nil {
			// Synthesize a single error finding before the
			// roll-up runs so Counts stays consistent with
			// len(Findings). The check summary's status is
			// pinned to error since the runner only emits a
			// finding when the check itself failed.
			findings = append(findings, doctorFinding{
				Check:    name,
				Severity: doctorSeverityError,
				Message:  "check failed",
				Detail:   err.Error(),
			})
		}
		sum := doctorCheckSum{
			Name:       name,
			Status:     maxSeverity(findings),
			DurationMS: dur.Milliseconds(),
			Notes:      len(findings),
		}
		report.Counts.OK += rollupOK(findings)
		report.Counts.Warn += rollupWarn(findings)
		report.Counts.Error += rollupError(findings)
		report.Findings = append(report.Findings, findings...)
		report.Checks = append(report.Checks, sum)
	}

	runCheck(doctorCheckSymlink, func() ([]doctorFinding, error) {
		return checkSymlink(deps)
	})
	runCheck(doctorCheckBundle, func() ([]doctorFinding, error) {
		return checkBundle(deps)
	})
	runCheck(doctorCheckLockstep, func() ([]doctorFinding, error) {
		return checkLockstep(deps)
	})
	runCheck(doctorCheckNodes, func() ([]doctorFinding, error) {
		return checkNodes(ctx, deps)
	})
	runCheck(doctorCheckBundleOrphans, func() ([]doctorFinding, error) {
		return checkBundleOrphans(ctx, deps)
	})
	// PR-X (issue #911 / ADR-110): secrets check verifies the
	// on-disk host.age posture matches the row's stored
	// cert_fingerprint. Runs after bundle/nodes (the DB row
	// existence is needed for the cert match) but before
	// node-hashes (the deep check is the slowest; secrets
	// short-circuits on file-mode / fingerprint issues).
	runCheck(doctorCheckSecrets, func() ([]doctorFinding, error) {
		return checkSecrets(ctx, deps)
	})
	// Issue #938 / PR-B / ADR-114: builder-base-ext4 check verifies
	// the staged ext4 contains /usr/local/bin/faas-guest-init. Always-
	// on because the failure mode is silent (alpine placeholder boots
	// to busybox and every build times out at vmmd's waitReady=30s).
	// Degrades to SeverityWarn when debugfs is absent so macOS dev boxes
	// and minimal container images still produce a useful report.
	runCheck(doctorCheckBuilderBaseExt4, func() ([]doctorFinding, error) {
		return checkBuilderBaseExt4(ctx, deps)
	})
	// node-hashes is --deep-only. When the flag is unset, don't
	// append the per-check summary at all — the JSON / text
	// output should clearly show the check was not run. apid
	// admin observers (PR-X) read the checks array as the
	// authoritative source for "what did we run", so a skipped
	// check must be absent, not present-with-zero-findings.
	if deps.deep {
		runCheck(doctorCheckNodeHashes, func() ([]doctorFinding, error) {
			return checkNodeHashes(ctx, deps)
		})
		// PR-B (ADR-113 day-2): verify-tarball-sbom is the canonical
		// triple check (cosign + SBoM-baseline diff). Deep-only
		// because the verifier shells out to cosign. Skipped on
		// legacy operators (no triple on disk) with a single
		// warn-finding per release.
		runCheck(doctorCheckVerifyTarballSBOM, func() ([]doctorFinding, error) {
			return checkVerifyTarballSBOM(ctx, deps)
		})
	}

	// Truncate findings if exceeded the cap. Counts is then
	// RE-DERIVED from the truncated slice so the wire shape
	// stays consistent: len(Findings) == sum(Counts).
	if len(report.Findings) > doctorFindingsCap {
		dropped := len(report.Findings) - doctorFindingsCap
		report.Findings = report.Findings[:doctorFindingsCap]
		report.Findings = append(report.Findings, doctorFinding{
			Check:    doctorCheckTruncate,
			Severity: doctorSeverityError,
			Message:  fmt.Sprintf("findings truncated at %d; use --node / --release to narrow", doctorFindingsCap),
			Detail:   fmt.Sprintf("dropped %d additional findings", dropped),
		})
	}
	report.Counts.OK = 0
	report.Counts.Warn = 0
	report.Counts.Error = 0
	for _, f := range report.Findings {
		switch f.Severity {
		case doctorSeverityOK:
			report.Counts.OK++
		case doctorSeverityWarn:
			report.Counts.Warn++
		case doctorSeverityError:
			report.Counts.Error++
		}
	}
	report.Counts.Total = report.Counts.OK + report.Counts.Warn + report.Counts.Error
	report.FinishedAt = time.Now()
	report.Healthy = report.Counts.Error == 0
	return report
}

// checkSymlink reads /opt/faas/current. Missing → warn (fresh box
// with no install yet); broken → error; valid → pop deps.currentGitSHA
// and emit one OK finding.
func checkSymlink(deps *doctorDeps) ([]doctorFinding, error) {
	gitSHA, err := releaseinstall.CurrentGitSHA(deps.releasesRoot)
	if err != nil {
		//nolint:nilerr // err is converted to a finding; runner does not need to see it.
		return []doctorFinding{{
			Check:    doctorCheckSymlink,
			Severity: doctorSeverityError,
			Message:  "cannot read /opt/faas/current",
			Detail:   err.Error(),
		}}, nil
	}
	if gitSHA == "" {
		// Fresh box; not a drift but worth flagging.
		return []doctorFinding{{
			Check:    doctorCheckSymlink,
			Severity: doctorSeverityWarn,
			Message:  "no active release; /opt/faas/current is missing",
		}}, nil
	}
	deps.currentGitSHA = gitSHA
	return []doctorFinding{{
		Check:    doctorCheckSymlink,
		Severity: doctorSeverityOK,
		Target:   gitSHA,
		Message:  "active release symlink points at " + gitSHA,
	}}, nil
}

// checkBundle reads + verifies the release manifest on disk. The
// currentGitSHA from checkSymlink is the load-bearing anchor —
// without an active release there is nothing to verify.
func checkBundle(deps *doctorDeps) ([]doctorFinding, error) {
	if deps.currentGitSHA == "" {
		return []doctorFinding{{
			Check:    doctorCheckBundle,
			Severity: doctorSeverityWarn,
			Message:  "skipped: no active release symlink",
		}}, nil
	}
	m, err := releaseinstall.Read(deps.releasesRoot, deps.currentGitSHA)
	if err != nil {
		//nolint:nilerr // err is converted to a finding; runner does not need to see it.
		return []doctorFinding{{
			Check:    doctorCheckBundle,
			Severity: doctorSeverityError,
			Target:   deps.currentGitSHA,
			Message:  "manifest read failed",
			Detail:   err.Error(),
		}}, nil
	}
	if err := releaseinstall.Verify(deps.releasesRoot, m); err != nil {
		//nolint:nilerr // err is converted to a finding; runner does not need to see it.
		return []doctorFinding{{
			Check:    doctorCheckBundle,
			Severity: doctorSeverityError,
			Target:   deps.currentGitSHA,
			Message:  "manifest verify failed",
			Detail:   err.Error(),
		}}, nil
	}
	return []doctorFinding{{
		Check:    doctorCheckBundle,
		Severity: doctorSeverityOK,
		Target:   deps.currentGitSHA,
		Message:  "manifest read + verify OK",
	}}, nil
}

// checkLockstep confirms manifest.daemon_hashes has one entry per
// catalog daemon. Mirrors ValidateManifest's invariant; surfacing
// it separately so a bundle built with a stale renderer PR is
// flagged distinctly from a bin/ mismatch.
func checkLockstep(deps *doctorDeps) ([]doctorFinding, error) {
	if deps.currentGitSHA == "" {
		return []doctorFinding{{
			Check:    doctorCheckLockstep,
			Severity: doctorSeverityWarn,
			Message:  "skipped: no active release symlink",
		}}, nil
	}
	m, err := releaseinstall.Read(deps.releasesRoot, deps.currentGitSHA)
	if err != nil {
		//nolint:nilerr // err is converted to a finding; runner does not need to see it.
		return []doctorFinding{{
			Check:    doctorCheckLockstep,
			Severity: doctorSeverityError,
			Target:   deps.currentGitSHA,
			Message:  "manifest read failed",
			Detail:   err.Error(),
		}}, nil
	}
	catalog := len(catalogHostKeys())
	if got := len(m.DaemonHashes); got != catalog {
		return []doctorFinding{{
			Check:    doctorCheckLockstep,
			Severity: doctorSeverityError,
			Target:   deps.currentGitSHA,
			Message:  fmt.Sprintf("manifest.daemon_hashes has %d entries, want %d", got, catalog),
		}}, nil
	}
	return []doctorFinding{{
		Check:    doctorCheckLockstep,
		Severity: doctorSeverityOK,
		Target:   deps.currentGitSHA,
		Message:  fmt.Sprintf("manifest covers all %d catalog daemons", catalog),
	}}, nil
}

// checkNodes walks ListComputeNodes and flags per-node drift:
// release_id missing / malformed / pointing at a release_bundles
// row that doesn't exist; manifest_hash drift on the active release.
// Honors --node and --release filters.
func checkNodes(ctx context.Context, deps *doctorDeps) ([]doctorFinding, error) {
	if deps.store == nil {
		// DB down — surface one warn and skip. The deep/--fail-on
		// escalation is handled by cmdDoctorDispatch.
		return []doctorFinding{{
			Check:    doctorCheckDB,
			Severity: doctorSeverityWarn,
			Message:  "database DSN not set; skipping nodes + bundle-orphans",
		}}, nil
	}

	// Use the shared pre-load from cmdDoctorDispatch. A nil map
	// means the pre-load failed (deps.store.ListAllBundles err'd);
	// in that case we surface the failure as a hard error so
	// the operator sees the DB read failure rather than a
	// silent skip.
	if deps.bundlesBySHA == nil {
		return nil, fmt.Errorf("release_bundles pre-load missing")
	}

	nodes, err := deps.store.ListComputeNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list compute nodes: %w", err)
	}

	var findings []doctorFinding
	for _, n := range nodes {
		if deps.nodeFilter != "" && n.Name != deps.nodeFilter {
			continue
		}
		// Validity runs BEFORE the --release filter: empty /
		// malformed / orphan release_ids are exactly the drift
		// the check exists to surface, and an operator
		// triaging a specific SHA must see those defects.
		// release_id sanity.
		if n.ReleaseID == "" {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckNodes,
				Severity: doctorSeverityError,
				Target:   n.Name,
				Message:  "compute_nodes row has empty release_id",
			})
			continue
		}
		if !releaseinstall.ValidGitSHA(n.ReleaseID) {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckNodes,
				Severity: doctorSeverityError,
				Target:   n.Name,
				Message:  "compute_nodes.release_id is not 40-char lowercase hex",
				Detail:   n.ReleaseID,
			})
			continue
		}
		// Orphan release_id: points at a row that doesn't exist.
		if _, ok := deps.bundlesBySHA[n.ReleaseID]; !ok {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckNodes,
				Severity: doctorSeverityError,
				Target:   n.Name,
				Message:  "orphan release_id: no release_bundles row for " + n.ReleaseID,
			})
			continue
		}
		// --release filter is applied AFTER the validity
		// checks above. The remaining comparison (manifest_hash
		// vs. active release) only matters for nodes on the
		// filtered release.
		if deps.releaseFilter != "" && n.ReleaseID != deps.releaseFilter {
			continue
		}
		// manifest_hash drift: the row's manifest_hash must match
		// the active release's manifest_hash (when active).
		if deps.currentGitSHA != "" && n.ReleaseID == deps.currentGitSHA && n.ManifestHash != "" {
			bundle, ok := deps.bundlesBySHA[n.ReleaseID]
			if ok && bundle.ManifestHash != n.ManifestHash {
				findings = append(findings, doctorFinding{
					Check:    doctorCheckNodes,
					Severity: doctorSeverityError,
					Target:   n.Name,
					Message:  "compute_nodes.manifest_hash drift",
					Detail:   fmt.Sprintf("got %s, want %s", n.ManifestHash, bundle.ManifestHash),
				})
				continue
			}
		}
	}

	if len(findings) == 0 {
		// Synthesize one OK finding so the JSON report shows the
		// check ran successfully.
		return []doctorFinding{{
			Check:    doctorCheckNodes,
			Severity: doctorSeverityOK,
			Message:  fmt.Sprintf("scanned %d compute_nodes rows", len(nodes)),
		}}, nil
	}
	return findings, nil
}

// checkBundleOrphans walks ListAllBundles and flags any
// applied_at IS NULL bundle whose on-disk tree is gone. A
// recoverable warning — the install path is incomplete but the
// operator can re-run `gregalectl release install` against the SHA.
func checkBundleOrphans(ctx context.Context, deps *doctorDeps) ([]doctorFinding, error) {
	if deps.store == nil {
		return []doctorFinding{{
			Check:    doctorCheckDB,
			Severity: doctorSeverityWarn,
			Message:  "database DSN not set; skipping bundle-orphans",
		}}, nil
	}
	if deps.bundlesBySHA == nil {
		return nil, fmt.Errorf("release_bundles pre-load missing")
	}
	var findings []doctorFinding
	for _, b := range deps.bundlesBySHA {
		if deps.releaseFilter != "" && b.GitSHA != deps.releaseFilter {
			continue
		}
		// Only orphan unapplied rows that have been swept.
		if b.AppliedAt != nil {
			continue
		}
		ok, err := releaseinstall.IsBundleOnDisk(deps.releasesRoot, b.GitSHA)
		if err != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckBundleOrphans,
				Severity: doctorSeverityWarn,
				Target:   b.GitSHA,
				Message:  "could not stat bundle on disk",
				Detail:   err.Error(),
			})
			continue
		}
		if !ok {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckBundleOrphans,
				Severity: doctorSeverityWarn,
				Target:   b.GitSHA,
				Message:  "unapplied release_bundles row; on-disk tree missing",
			})
		}
	}
	if len(findings) == 0 {
		return []doctorFinding{{
			Check:    doctorCheckBundleOrphans,
			Severity: doctorSeverityOK,
			Message:  fmt.Sprintf("scanned %d release_bundles rows", len(deps.bundlesBySHA)),
		}}, nil
	}
	return findings, nil
}

// checkNodeHashes is the --deep check. Loads each node's
// release_id + manifest_hash, then re-Verify()s the on-disk
// bundle against the current release_bundles row hashes. A
// mismatch means the on-disk binary was tampered with after the
// install — a real drift signal.
func checkNodeHashes(ctx context.Context, deps *doctorDeps) ([]doctorFinding, error) {
	if deps.store == nil {
		// cmdDoctorDispatch blocks --deep + no-DB at the entry
		// gate; defensive guard here keeps the check symmetric.
		return []doctorFinding{{
			Check:    doctorCheckNodeHashes,
			Severity: doctorSeverityError,
			Message:  "database DSN not set; --deep requires DB",
		}}, nil
	}
	nodes, err := deps.store.ListComputeNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list compute nodes: %w", err)
	}
	// Bundle cache: one per release_id so the heavy re-hash runs
	// at most once per release.
	type bundleVer struct {
		manifest  releaseinstall.Manifest
		verifyErr error
	}
	cache := make(map[string]bundleVer, len(nodes))
	var findings []doctorFinding
	for _, n := range nodes {
		if deps.nodeFilter != "" && n.Name != deps.nodeFilter {
			continue
		}
		if deps.releaseFilter != "" && n.ReleaseID != deps.releaseFilter {
			continue
		}
		if _, ok := cache[n.ReleaseID]; !ok {
			m, err := releaseinstall.Read(deps.releasesRoot, n.ReleaseID)
			if err != nil {
				cache[n.ReleaseID] = bundleVer{verifyErr: err}
			} else {
				verr := releaseinstall.Verify(deps.releasesRoot, m)
				cache[n.ReleaseID] = bundleVer{manifest: m, verifyErr: verr}
			}
		}
		bd := cache[n.ReleaseID]
		if bd.verifyErr != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckNodeHashes,
				Severity: doctorSeverityError,
				Target:   n.Name,
				Message:  "bundle verify failed",
				Detail:   bd.verifyErr.Error(),
			})
			continue
		}
		// Cross-check against the DB row's manifest_hash too.
		// Uses the shared pre-load from cmdDoctorDispatch (one
		// SELECT for the whole fleet) — no per-node GetByGitSHA.
		bundle, ok := deps.bundlesBySHA[n.ReleaseID]
		if !ok {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckNodeHashes,
				Severity: doctorSeverityError,
				Target:   n.Name,
				Message:  "compute_nodes.release_id missing from release_bundles pre-load",
				Detail:   n.ReleaseID,
			})
			continue
		}
		if bundle.ManifestHash != n.ManifestHash {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckNodeHashes,
				Severity: doctorSeverityError,
				Target:   n.Name,
				Message:  "manifest_hash drift",
				Detail:   fmt.Sprintf("compute_nodes=%s release_bundles=%s", n.ManifestHash, bundle.ManifestHash),
			})
		}
	}
	if len(findings) == 0 {
		return []doctorFinding{{
			Check:    doctorCheckNodeHashes,
			Severity: doctorSeverityOK,
			Message:  fmt.Sprintf("deep hash verified for %d nodes", len(nodes)),
		}}, nil
	}
	return findings, nil
}

// emitDoctorReport writes the report to w. JSON mode → indented
// JSON; text mode → human-readable table.
func emitDoctorReport(w io.Writer, r doctorReport) {
	if jsonEnabled() {
		jsonEmit(w, r)
		return
	}
	// Plain text: per-check summary then findings.
	_, _ = fmt.Fprintf(w, "gregalectl doctor: %s\n", releasesRootLabel(r))
	if r.Healthy {
		_, _ = fmt.Fprintf(w, "  → healthy: %d ok, %d warn, %d error\n",
			r.Counts.OK, r.Counts.Warn, r.Counts.Error)
	} else {
		_, _ = fmt.Fprintf(w, "  → drift: %d ok, %d warn, %d error\n",
			r.Counts.OK, r.Counts.Warn, r.Counts.Error)
	}
	for _, c := range r.Checks {
		_, _ = fmt.Fprintf(w, "  - %-18s %-7s %dms (%d findings)\n",
			c.Name, c.Status, c.DurationMS, c.Notes)
	}
	if len(r.Findings) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "  findings:")
	for _, f := range r.Findings {
		target := ""
		if f.Target != "" {
			target = " [" + f.Target + "]"
		}
		_, _ = fmt.Fprintf(w, "    %s %s%s: %s\n",
			severityGlyph(f.Severity), f.Check, target, f.Message)
		if f.Detail != "" {
			_, _ = fmt.Fprintf(w, "      %s\n", f.Detail)
		}
	}
}

func releasesRootLabel(r doctorReport) string {
	if r.NodeFilter != "" || r.ReleaseFilter != "" {
		return r.ReleasesRoot + " (filters applied)"
	}
	return r.ReleasesRoot
}

func severityGlyph(s string) string {
	switch s {
	case doctorSeverityOK:
		return "OK"
	case doctorSeverityWarn:
		return "WARN"
	case doctorSeverityError:
		return "ERROR"
	}
	return s
}

// maxSeverity returns the roll-up status for a check. ok < warn <
// error. Empty findings → ok.
func maxSeverity(findings []doctorFinding) string {
	roll := doctorStatusOK
	for _, f := range findings {
		switch f.Severity {
		case doctorSeverityError:
			return doctorStatusError
		case doctorSeverityWarn:
			roll = doctorStatusWarn
		}
	}
	return roll
}

func rollupOK(findings []doctorFinding) int {
	n := 0
	for _, f := range findings {
		if f.Severity == doctorSeverityOK {
			n++
		}
	}
	return n
}

func rollupWarn(findings []doctorFinding) int {
	n := 0
	for _, f := range findings {
		if f.Severity == doctorSeverityWarn {
			n++
		}
	}
	return n
}

func rollupError(findings []doctorFinding) int {
	n := 0
	for _, f := range findings {
		if f.Severity == doctorSeverityError {
			n++
		}
	}
	return n
}

// checkSecrets (PR-X / issue #911 / ADR-110) verifies the on-disk
// secret posture matches the row's stored cert_fingerprint. The
// check is fs-only at its core (no DB required); the DB is
// consulted only AFTER the on-disk side passes, to compare the
// recomputed host.age fingerprint against the stamped value on
// compute_nodes.cert_fingerprint.
//
// The check runs on every box (no --deep flag) — secrets drift
// is a load-bearing signal that an operator needs to see in the
// default `gregalectl doctor` invocation, not behind a flag.
//
// Findings:
//
//   - missing   host.age            error (load-bearing; no
//     cluster operation can run)
//   - missing   session.key         warn (single-box dev rolls an
//     ephemeral session manager)
//   - missing   box-age-key         warn (off-host pg backup
//     cannot run; clears after
//     `secrets init`)
//   - missing   rclone.conf         warn (pg backup envelope
//     unseal will fail until
//     `backup unseal-rclone`)
//   - missing   archive-creds.json  warn (log archive cannot run)
//   - host.age wrong mode (≠ 0400)  error (spec §11)
//   - session.key not 64 hex chars  error (gatewayd loader
//     refuses non-hex bytes)
//   - cert_fingerprint mismatch     warn+ BumpComputeNodeGeneration
//     (the DB row is stale; ops
//     should re-run `secrets init`
//     to re-stamp)
//
// checkVerifyTarballSBOM (PR-B / ADR-113 day-2): walks the
// canonical on-disk triple (release.tar.gz + release.cosign.bundle
// + release.sbom.json) per release and runs:
//
//  1. cosign verify-blob via deps.verifier (the load-bearing
//     trust bit from PR-A commit 2). On failure: emit an error
//     finding with the verifier's stderr in the Detail field.
//  2. SBoM CVE-baseline diff against the on-disk
//     sbom-baseline.json (the KGV). On regression: emit an
//     error finding listing the regressed severities.
//
// The probe is deep-only because the verifier shells out to
// `cosign` (PR-A's exec-backed impl). Legacy operators (no
// canary) get a single warn-finding per release that the triple
// is missing — distinct from a 'verify failed' error so the
// on-disk shape is enumerated, not just success/failure.
//
// Per-SHA findings are emitted so the operator can see exactly
// which release regressed. The check-level summary stays
// rolled-up (max severity + finding count via runCheck).
func checkVerifyTarballSBOM(ctx context.Context, deps *doctorDeps) ([]doctorFinding, error) {
	var findings []doctorFinding
	entries, err := os.ReadDir(deps.releasesRoot)
	if err != nil {
		// releases_root missing is a separate finding (bundle/symlink
		// checks already cover it). Don't double-report here.
		if os.IsNotExist(err) {
			return []doctorFinding{{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityWarn,
				Message:  "releases root not present",
				Detail:   deps.releasesRoot,
			}}, nil
		}
		return nil, fmt.Errorf("read releases root: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		gitSHA := e.Name()
		if !releaseinstall.ValidGitSHA(gitSHA) {
			continue
		}
		if deps.releaseFilter != "" && gitSHA != deps.releaseFilter {
			continue
		}
		// Resolve the canonical triple. Missing files → warn
		// (legacy operators continue); present-and-malformed
		// → error (the canary partially failed).
		dir := releaseinstall.BundleRoot(deps.releasesRoot, gitSHA)
		tarballPath := filepath.Join(dir, "release.tar.gz")
		sigPath := filepath.Join(dir, "release.cosign.bundle")
		sbomPath := filepath.Join(dir, "release.sbom.json")
		if _, err := os.Stat(tarballPath); err != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityWarn,
				Target:   gitSHA,
				Message:  "canonical triple missing (legacy bundle path; SBoM gate not enforced)",
			})
			continue
		}
		if _, err := os.Stat(sigPath); err != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityError,
				Target:   gitSHA,
				Message:  "cosign bundle missing",
				Detail:   sigPath,
			})
			continue
		}
		if _, err := os.Stat(sbomPath); err != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityError,
				Target:   gitSHA,
				Message:  "SBoM missing",
				Detail:   sbomPath,
			})
			continue
		}
		// Build the Tarball + run Verify. Packed/Sig/SBOM are
		// the bytes the verifier inspects. The fixture
		// verifier (whitebox) skips the cosign shell-out.
		tbBody, err := os.ReadFile(tarballPath)
		if err != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityError,
				Target:   gitSHA,
				Message:  "read tarball",
				Detail:   err.Error(),
			})
			continue
		}
		sigBody, err := os.ReadFile(sigPath)
		if err != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityError,
				Target:   gitSHA,
				Message:  "read cosign bundle",
				Detail:   err.Error(),
			})
			continue
		}
		sbomBody, err := os.ReadFile(sbomPath)
		if err != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityError,
				Target:   gitSHA,
				Message:  "read SBoM",
				Detail:   err.Error(),
			})
			continue
		}
		// Read the on-disk manifest. Tarball.Verify requires a
		// valid manifest; the same file is what the install
		// path consumes on the way to AtomicFlip, so reading
		// here matches the production call site.
		m, err := releaseinstall.Read(deps.releasesRoot, gitSHA)
		if err != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityError,
				Target:   gitSHA,
				Message:  "read manifest",
				Detail:   err.Error(),
			})
			continue
		}
		tb := &releaseinstall.Tarball{
			GitSHA:   gitSHA,
			Manifest: m,
			Packed:   tbBody,
			Sig:      sigBody,
			SBOM:     sbomBody,
		}
		// BuildTarball computes BinSHA256 from manifest.DaemonHashes
		// (the canonical hash-of-each-binary map). When the probe
		// reconstructs a Tarball from on-disk bytes, the BinSHA256
		// map isn't populated by anything — derive it here so
		// Tarball.hashWalk passes. Without this, every release
		// emits "manifest missing daemon <name>" and the OK path
		// never fires.
		tb.BinSHA256 = make(map[string]string, len(m.DaemonHashes))
		for name, h := range m.DaemonHashes {
			hex := strings.TrimPrefix(h, "sha256:")
			tb.BinSHA256[name] = hex
		}
		tb.ToolSHA256 = make(map[string]string, len(m.ToolHashes))
		for name, h := range m.ToolHashes {
			hex := strings.TrimPrefix(h, "sha256:")
			tb.ToolSHA256[name] = hex
		}
		identity, err := tb.Verify(ctx, deps.verifier)
		if err != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityError,
				Target:   gitSHA,
				Message:  "tarball verify failed",
				Detail:   err.Error(),
			})
			continue
		}
		// SBoM CVE-baseline diff. ReadBaseline returns the
		// ErrNilBaseline sentinel for missing basline; that
		// is operator-actionable (rotate) but not a doctor
		// error — the install-time gate already enforces it.
		// We surface it as a warn so operators see the
		// pending rotation in the doctor output.
		counts, parseErr := releaseinstall.ParseSPDXv2_3(sbomBody)
		if parseErr != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityError,
				Target:   gitSHA,
				Message:  "parse SBoM",
				Detail:   parseErr.Error(),
			})
			continue
		}
		baseline, baseErr := releaseinstall.ReadBaseline(deps.releasesRoot, gitSHA)
		var baselineCounts *releaseinstall.SBOMCounts
		switch {
		case baseErr == nil:
			if _, diffErr := baseline.Diff(counts); diffErr != nil {
				findings = append(findings, doctorFinding{
					Check:    doctorCheckVerifyTarballSBOM,
					Severity: doctorSeverityError,
					Target:   gitSHA,
					Message:  "SBoM CVE regression",
					Detail:   diffErr.Error(),
				})
				continue
			}
			// Snapshot the baseline counts so the OK row below can
			// print both baseline and live side-by-side. The rotate
			// contract is "baseline counts", not the live SBoM;
			// operators reading the doctor JSON must see the same
			// numbers they rotated to, not the new SBoM's tally.
			c := baseline.Counts
			baselineCounts = &c
		case errors.Is(baseErr, releaseinstall.ErrNilBaseline):
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityWarn,
				Target:   gitSHA,
				Message:  "SBoM baseline missing; run `gregalectl release kgv rotate --git-sha " + gitSHA + "`",
				Detail:   releaseinstall.SBOMBaselinePath(releaseinstall.BundleRoot(deps.releasesRoot, gitSHA)),
			})
		default:
			findings = append(findings, doctorFinding{
				Check:    doctorCheckVerifyTarballSBOM,
				Severity: doctorSeverityError,
				Target:   gitSHA,
				Message:  "read SBoM baseline",
				Detail:   baseErr.Error(),
			})
			continue
		}
		// OK finding: tarball verified + counts baseline-clean.
		// Print the baseline counts the operator rotated to AND
		// the live SBoM counts side-by-side, so the JSON line
		// matches the rotate contract (baseline) without losing
		// the current SBoM tally. When baseline is missing, only
		// the live counts are available (KGVZero fallback).
		var msg string
		if baselineCounts != nil {
			msg = fmt.Sprintf("signature=%s baseline=critical:%d high:%d medium:%d low:%d live=critical:%d high:%d medium:%d low:%d",
				identity,
				baselineCounts.CriticalN, baselineCounts.HighN, baselineCounts.MediumN, baselineCounts.LowN,
				counts.CriticalN, counts.HighN, counts.MediumN, counts.LowN)
		} else {
			msg = fmt.Sprintf("signature=%s counts=critical:%d high:%d medium:%d low:%d",
				identity, counts.CriticalN, counts.HighN, counts.MediumN, counts.LowN)
		}
		findings = append(findings, doctorFinding{
			Check:    doctorCheckVerifyTarballSBOM,
			Severity: doctorSeverityOK,
			Target:   gitSHA,
			Message:  msg,
		})
	}
	if len(findings) == 0 {
		return []doctorFinding{{
			Check:    doctorCheckVerifyTarballSBOM,
			Severity: doctorSeverityOK,
			Message:  "no releases on disk",
		}}, nil
	}
	return findings, nil
}

func checkSecrets(ctx context.Context, deps *doctorDeps) ([]doctorFinding, error) {
	var findings []doctorFinding
	// The canonical secret paths match the v1 bootstrap.sh
	// step 11d convention + the v2 gregalectl secrets init
	// writer. The doctor reads the same paths so the check
	// is consistent with the writer.
	storageDir := "/etc/faas/secrets/storage-box"
	secrets := []struct {
		label   string
		path    string
		want    string
		missing string
	}{
		{"host.age", "/etc/faas/secrets/host.age", "0400", doctorSeverityError},
		{"session.key", "/etc/faas/secrets/session.key", "0400", doctorSeverityWarn},
		{"box-age-key", storageDir + "/box-age-key", "0440", doctorSeverityWarn},
		{"rclone.conf", storageDir + "/rclone.conf", "0440", doctorSeverityWarn},
		{"archive-creds.json", storageDir + "/archive-creds.json", "0400", doctorSeverityWarn},
	}
	for _, s := range secrets {
		info, err := os.Stat(s.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				findings = append(findings, doctorFinding{
					Check:    doctorCheckSecrets,
					Severity: s.missing,
					Message:  fmt.Sprintf("missing %s", s.label),
					Detail:   s.path,
				})
				continue
			}
			findings = append(findings, doctorFinding{
				Check:    doctorCheckSecrets,
				Severity: doctorSeverityError,
				Message:  fmt.Sprintf("stat %s failed", s.label),
				Detail:   err.Error(),
			})
			continue
		}
		// Mode check. The load-bearing contract is "no
		// group/world bits + user bits ≤ canonical" — spec §11
		// says secrets are not world- or group-readable. We
		// accept a stricter mode (e.g. 0o400 instead of 0o440)
		// but never a looser one, and never anything with group
		// or world bits set. The exact-mode comparison was
		// over-strict: a 0o000 mode (rare but valid for an
		// un-read file) wouldn't match 0o400.
		got := info.Mode().Perm()
		want, permErr := parsePerm(s.want)
		if permErr != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckSecrets,
				Severity: doctorSeverityWarn,
				Message:  fmt.Sprintf("%s wanted-mode unparseable, skipping mode check", s.label),
				Detail:   permErr.Error(),
			})
			continue
		}
		// The wanted mode is an upper bound, not an exact mode:
		// 0400 is valid where 0440 is the canonical mode, but
		// group/world bits beyond the declared contract are not.
		if got&^want != 0 {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckSecrets,
				Severity: doctorSeverityError,
				Message:  fmt.Sprintf("%s wrong mode (got %04o, want %s)", s.label, got, s.want),
				Detail:   s.path,
			})
		}
	}
	// `secrets init` creates shape-valid placeholders for the two external
	// backup integrations so provisioning can complete in stages. They are
	// not usable credentials and must remain visible until the operator
	// unseals real values from the secret store.
	rclonePath := storageDir + "/rclone.conf"
	if data, err := os.ReadFile(rclonePath); err == nil && strings.Contains(string(data), "secrets init stub") {
		findings = append(findings, doctorFinding{
			Check:    doctorCheckSecrets,
			Severity: doctorSeverityWarn,
			Message:  "rclone.conf is still the secrets-init placeholder",
			Detail:   "run 'gregalectl backup unseal-rclone' with a real envelope",
		})
	}
	archiveCredsPath := storageDir + "/archive-creds.json"
	if data, err := os.ReadFile(archiveCredsPath); err == nil && strings.TrimSpace(string(data)) == "{}" {
		findings = append(findings, doctorFinding{
			Check:    doctorCheckSecrets,
			Severity: doctorSeverityWarn,
			Message:  "archive-creds.json is still the secrets-init placeholder",
			Detail:   "run 'gregalectl backup unseal-archive-creds' with a real envelope",
		})
	}
	// session.key must be exactly 64 hex chars (32 bytes
	// hex-encoded). The gatewayd loader rejects non-hex or
	// wrong-length values per
	// cmd/gatewayd-internal/session_key.go:43-58.
	sessionKeyPath := "/etc/faas/secrets/session.key"
	if data, err := os.ReadFile(sessionKeyPath); err == nil {
		trimmed := strings.TrimSpace(string(data))
		if len(trimmed) != 64 {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckSecrets,
				Severity: doctorSeverityError,
				Message:  fmt.Sprintf("session.key wrong length (got %d, want 64 hex chars)", len(trimmed)),
				Detail:   sessionKeyPath,
			})
		} else if _, err := hex.DecodeString(trimmed); err != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckSecrets,
				Severity: doctorSeverityError,
				Message:  "session.key is not valid hex",
				Detail:   err.Error(),
			})
		}
	}
	// cert_fingerprint match (DB-side). Only runs when the
	// on-disk side passes AND a DB store is available. A
	// missing DB makes the on-disk check the only signal —
	// the operator can run `secrets init` to re-stamp. The
	// check walks EVERY compute_nodes row (no --node filter
	// gate) so a per-node drift on a multi-host cluster
	// surfaces even when the operator runs `gregalectl
	// doctor` without flags.
	if deps.store == nil {
		return findings, nil
	}
	nodes, err := deps.store.ListComputeNodes(ctx)
	if err != nil {
		findings = append(findings, doctorFinding{
			Check:    doctorCheckSecrets,
			Severity: doctorSeverityError,
			Message:  "cert_fingerprint check: list compute_nodes failed",
			Detail:   err.Error(),
		})
		return findings, fmt.Errorf("list compute_nodes: %w", err)
	}
	hostAgeID, loadErr := secretbox.LoadHostKey("/etc/faas/secrets/host.age")
	if loadErr != nil {
		// Already surfaced above as a "missing host.age"
		// finding; skip the per-row fingerprint walk because
		// every row would mismatch. Propagate loadErr so
		// runCheck's existing error→finding synthesis picks
		// it up (the missing-file finding is also added
		// above as a side-channel for the operator).
		return findings, fmt.Errorf("load host.age: %w", loadErr)
	}
	// The DB stores the public host certificate (the age recipient string),
	// not the private identity file bytes. Hash the same public value that
	// `secrets init` stamps; hashing host.age's private text made every valid
	// initialization look drifted.
	sum := sha256.Sum256([]byte(secretbox.RecipientString(hostAgeID)))
	got := hex.EncodeToString(sum[:])
	for _, n := range nodes {
		if n.CertFingerprint == nil {
			continue
		}
		rowFP := *n.CertFingerprint
		if rowFP == got {
			continue
		}
		rowFPShort := rowFP
		if len(rowFPShort) > 12 {
			rowFPShort = rowFPShort[:12]
		}
		gotShort := got
		if len(gotShort) > 12 {
			gotShort = gotShort[:12]
		}
		findings = append(findings, doctorFinding{
			Check:    doctorCheckSecrets,
			Severity: doctorSeverityWarn,
			Message:  fmt.Sprintf("%s cert_fingerprint mismatch (on-disk %s, row %s)", n.Name, gotShort, rowFPShort),
			Detail:   "re-run 'gregalectl secrets init' to re-stamp compute_nodes.cert_fingerprint",
		})
		// Bump generation as a soft audit-trail signal —
		// the next doctor run surfaces this as a generation
		// bump rather than a stale-fingerprint finding. A bump
		// failure is non-fatal for checkSecrets (the warning
		// is the primary signal).
		if _, bumpErr := deps.store.BumpComputeNodeGeneration(ctx, n.Name); bumpErr != nil {
			findings = append(findings, doctorFinding{
				Check:    doctorCheckSecrets,
				Severity: doctorSeverityWarn,
				Message:  fmt.Sprintf("%s generation bump failed after fingerprint drift", n.Name),
				Detail:   bumpErr.Error(),
			})
		}
	}
	return findings, nil
}

// builder-base ext4 hooks (issue #938 / PR-B / ADR-114). Package-level
// vars so tests can stub the debugfs probe + stat path without going
// through real disk. Production wiring points these at the canonical
// /srv/fc/base/runner-builder-<arch>.ext4 and `debugfs` binary on PATH.
var (
	locateBuilderBasePathHook = func() string {
		if v := os.Getenv("FAAS_BUILDER_BASE_PATH"); v != "" {
			return v
		}
		storageRoot := os.Getenv("FAAS_STORAGE_ROOT")
		if storageRoot == "" {
			storageRoot = "/srv/fc"
		}
		return filepath.Join(storageRoot, "base", "runner-builder-"+runtime.GOARCH+".ext4")
	}
	// A split control-plane does not run imaged/builderd, so the
	// builder-base probe is not applicable there. Prefer the explicit
	// role when supplied; otherwise use systemd's read-only unit state
	// to distinguish a compute-capable box from a control-only box.
	builderBaseRequiredHook = func(ctx context.Context) bool {
		if role := os.Getenv("FAAS_BOX_ROLE"); role != "" {
			return role == "compute-only" || role == "single-box"
		}
		for _, unit := range []string{"faas-builderd.service", "faas-imaged.service"} {
			cmd := exec.CommandContext(ctx, "systemctl", "is-enabled", unit)
			if out, err := cmd.Output(); err == nil && strings.TrimSpace(string(out)) == "enabled" {
				return true
			}
		}
		return false
	}
	statHook       = os.Stat
	lookPathHook   = exec.LookPath
	runDebugfsHook = func(ctx context.Context, debugfs, ext4, target string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, debugfs, "-R", "stat "+target, ext4)
		return cmd.CombinedOutput()
	}
)

// checkBuilderBaseExt4 verifies the staged builder base ext4 contains
// /usr/local/bin/faas-guest-init (issue #938 / PR-B / ADR-114).
//
// Three states:
//  1. ext4 missing → SeverityWarn (imaged stages on first cold boot;
//     this finding self-resolves after imaged has run).
//  2. ext4 present but debugfs absent → SeverityWarn (macOS / minimal
//     containers; install e2fsprogs for full coverage — the ansible
//     control-plane role ensures this on production boxes).
//  3. ext4 present, debugfs runs, file absent → SeverityError
//     (the build pipeline silently produced a wrong rootfs — every
//     `gregale deploy` will time out at vmmd's waitReady=30s).
//
// All three states produce exactly one finding so the wire shape
// stays consistent.
//
// ctx is plumbed through so the debugfs subprocess respects the
// doctor's overall deadline (the runCheck above is non-cancellable
// today; the ctx anchor lets future --json=streaming or timeout-
// bounded wrappers reuse this check without churn).
func checkBuilderBaseExt4(ctx context.Context, deps *doctorDeps) ([]doctorFinding, error) {
	_ = deps
	basePath := locateBuilderBasePathHook()
	storageRoot := os.Getenv("FAAS_STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "/srv/fc"
	}
	canonicalPath := filepath.Join(storageRoot, "base", "runner-builder-"+runtime.GOARCH+".ext4")
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" && basePath == canonicalPath && !builderBaseRequiredHook(ctx) {
		return []doctorFinding{{
			Check:    doctorCheckBuilderBaseExt4,
			Severity: doctorSeverityOK,
			Message:  "builder base check not applicable on this box",
		}}, nil
	}
	if _, err := statHook(basePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []doctorFinding{{
				Check:    doctorCheckBuilderBaseExt4,
				Severity: doctorSeverityWarn,
				Message:  "builder base image not staged",
				Detail:   fmt.Sprintf("%s: imaged stages on first cold boot; this finding resolves after a successful cold boot", basePath),
			}}, nil
		}
		return nil, fmt.Errorf("stat %s: %w", basePath, err)
	}
	debugfs, err := lookPathHook("debugfs")
	if err != nil {
		//nolint:nilerr // err is converted to a finding; runner does not need to see it.
		return []doctorFinding{{
			Check:    doctorCheckBuilderBaseExt4,
			Severity: doctorSeverityWarn,
			Message:  "debugfs unavailable; cannot verify builder base contents",
			Detail:   "install e2fsprogs on the box for full coverage (apt install e2fsprogs / brew install e2fsprogs)",
		}}, nil
	}
	out, err := runDebugfsHook(ctx, debugfs, basePath, "/usr/local/bin/faas-guest-init")
	if err != nil {
		//nolint:nilerr // err is converted to a finding; runner does not need to see it.
		return []doctorFinding{{
			Check:    doctorCheckBuilderBaseExt4,
			Severity: doctorSeverityError,
			Message:  "faas-guest-init missing from builder base image",
			Detail:   fmt.Sprintf("debugfs stat against %s returned: %s", basePath, strings.TrimSpace(string(out))),
		}}, nil
	}
	// A successful debugfs stat prints an inode and mode record. The
	// wording differs across e2fsprogs releases (for example, older
	// versions use "File mode:" while 1.47 uses "Mode:"). Both
	// fields are always present on a hit; a missing file
	// returns exit status 1 + "file not found" before this block
	// runs, so the substring check is belt-and-braces against a
	// future debugfs version that emits a different header shape.
	// Requiring both fields (not just "Inode") avoids a false OK
	// finding if debugfs ever prints "Inode" as part of an error
	// banner (review finding #7 on PR #940).
	outStr := string(out)
	hasMode := strings.Contains(outStr, "File mode:") || strings.Contains(outStr, "Mode:")
	if !strings.Contains(outStr, "Inode:") || !hasMode {
		return []doctorFinding{{
			Check:    doctorCheckBuilderBaseExt4,
			Severity: doctorSeverityError,
			Message:  "faas-guest-init missing from builder base image",
			Detail:   fmt.Sprintf("debugfs stat output did not contain inode + mode records: %s", strings.TrimSpace(outStr)),
		}}, nil
	}
	return []doctorFinding{{
		Check:    doctorCheckBuilderBaseExt4,
		Severity: doctorSeverityOK,
		Message:  "faas-guest-init present in builder base image",
		Detail:   fmt.Sprintf("debugfs confirmed %s exists in %s", "/usr/local/bin/faas-guest-init", basePath),
	}}, nil
}

// parsePerm returns the perm bits for a 4-digit octal string
// like "0400" or "0440". Returns an error on bad input — the
// caller (commands_doctor.go:checkSecrets) emits a WARN finding
// and continues with a defensive mode rather than panicking, so
// a malformed TOML config or a typo'd --want flag does not kill
// the doctor's "never panic, always emit a finding" discipline
// (mirrors the DB-down warn paths at lines 585, 692).
func parsePerm(s string) (os.FileMode, error) {
	n := 0
	if len(s) != 4 {
		return 0, fmt.Errorf("want 4-digit octal, got %q", s)
	}
	for _, ch := range s {
		if ch < '0' || ch > '7' {
			return 0, fmt.Errorf("bad octal %q", s)
		}
		n = n*8 + int(ch-'0')
	}
	return os.FileMode(n), nil
}

// dispatchDoctor is the const name referenced by main.go +
// commands_completion_test.go. Kept here (not in commands2.go or
// constants.go) so the doctor surface is a single drop-in file.
const dispatchDoctor = "doctor"
