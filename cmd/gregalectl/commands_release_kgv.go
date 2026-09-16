// commands_release_kgv.go — operator-side CLI for the SBoM KGV
// (known-good version) rotate subcommand (ADR-113 PR-B).
//
// `gregalectl release kgv rotate` is the operator escape hatch from
// PR-A's fail-closed SBoM CVE-baseline gate. After a benign CVE
// bump (e.g., a backported fix that bumps the CRITICAL count vs
// what the prior baseline knew about), the install will refuse
// until the operator explicitly accepts the new SBoM by writing a
// fresh baseline. `rotate` reads the on-disk release's currently-
// installed SBoM (the PR-A canary-stamped artefact) and writes a
// new baseline at
// <releases-root>/<git-sha>/sbom-baseline.json.
//
// `rotate --from-zero` is the deliberate "I accept a baseline with
// zero known vulns" entry point. The PR-A default is KGVZero
// (zero CRITICAL/HIGH); the operator can re-assert that posture
// here without re-parsing the canary SBoM. This subsumes the
// originally-sketched `release kgv init` subcommand — one keyword
// with a flag is the same surface as two keywords.
//
// The KGV is operator-confirmed, never auto-rotated. This file
// does not depend on pkg/cosign or pkg/oci; the gate at install
// time is the consumer of the baseline, the producer is the
// operator.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

// kgv subcommands.
const (
	subReleaseKGVRotate = "rotate"
	subReleaseKGVVerify = "verify"
	// subReleaseKGVInit is a deliberate alias for `rotate --from-zero`.
	// The PR-A sketch named this leaf `init`; PR-B folded it into
	// `rotate --from-zero` (one keyword + flag is the same surface as
	// two keywords; see this file's header). Operators who muscle-
	// memory-type `release kgv init` get the alias + a deprecation
	// note on stderr instead of exit-2. Will be removed in PR-7.
	subReleaseKGVInit = "init"
)

// cmdReleaseKGV is the inner dispatcher for `gregalectl release kgv`.
// Two leaves land today (rotate + init); unknown leaves exit 2 with
// a usage pointer so the operator can `help` rather than guess.
func cmdReleaseKGV(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregalectl release kgv <subcommand> [flags]\n\nSubcommands:\n  verify    Compare an incoming release SBoM with the accepted host baseline.\n  rotate    Accept an activated release SBoM as the new host baseline.\n  init      Alias for `rotate --from-zero` (deprecated fold; will be removed in PR-7).\n", "release")
		return 1
	}
	switch args[0] {
	case subReleaseKGVRotate:
		return cmdReleaseKGVRotate(args[1:])
	case subReleaseKGVVerify:
		return cmdReleaseKGVVerify(args[1:])
	case subReleaseKGVInit:
		// Alias path: force --from-zero and emit a deprecation note
		// to stderr so operators running `kgv init` get the same
		// baseline-write semantics without the surprise of an
		// unknown-subcommand exit-2. The note goes to stderr (not
		// stdout) so --json consumers aren't polluted.
		_, _ = fmt.Fprintln(os.Stderr, "note: 'release kgv init' is an alias for 'release kgv rotate --from-zero' (will be removed in PR-7)")
		return cmdReleaseKGVRotate(append([]string{"--from-zero"}, args[1:]...))
	case flagHelpShort, flagHelpLong:
		PrintUsage(os.Stderr, "usage: gregalectl release kgv <subcommand> [flags]\n\nSubcommands:\n  verify    Compare an incoming release SBoM with the accepted host baseline.\n  rotate    Accept an activated release SBoM as the new host baseline.\n  init      Alias for `rotate --from-zero` (deprecated fold; will be removed in PR-7).\n", "release")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gregalectl release kgv: unknown subcommand %q (expected: verify, rotate, init)\n", args[0])
		return 2
	}
}

// cmdReleaseKGVRotate refreshes the SBoM CVE-baseline for the
// given git-sha. Two flows:
//
//	(default) Read the on-disk release's SBoM (the PR-A canary
//	          artefact), parse it via ParseSPDXv2_3, and write a
//	          baseline with the parsed counts. The operator's
//	          intent is "accept this SBoM as the new KGV".
//	--from-zero Write KGVZero(git-sha) — zero CRITICAL/HIGH —
//	          without touching the on-disk SBoM. The operator's
//	          intent is "re-assert the fail-closed default".
//
// The release dir MUST exist (the operator pre-staged the bundle
// via `release install` or `make build-sha256`). Failure modes
// disambiguate by exit code:
//
//	1  usage error (missing/wrong flag, bad git-sha shape)
//	2  unknown kgv subcommand
//	3  platform/infra (release dir missing, manifest unreadable,
//	   SBoM unparseable, baseline write failed)
func cmdReleaseKGVRotate(args []string) int {
	if len(args) > 0 && (args[0] == flagHelpLong || args[0] == flagHelpShort) {
		PrintUsage(os.Stderr, "usage: gregalectl release kgv rotate --git-sha SHA [--releases-root PATH] [--from-zero] [--json]", "release")
		return 0
	}
	fs := flag.NewFlagSet("release kgv rotate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	gitSHA := fs.String("git-sha", "", "40-char lowercase hex git SHA (required)")
	releasesRoot := fs.String("releases-root", "/opt/faas/releases", "releases root directory")
	fromZero := fs.Bool("from-zero", false, "write KGVZero (zero CRITICAL/HIGH) instead of reading the on-disk SBoM")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *gitSHA == "" {
		_, _ = fmt.Fprintln(os.Stderr, "gregalectl release kgv rotate: --git-sha is required")
		return 1
	}
	if !releaseinstall.ValidGitSHA(*gitSHA) {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv rotate: --git-sha %q is not a 40-char lowercase hex\n", *gitSHA)
		return 1
	}
	// Resolve the baseline we'll write. --from-zero short-circuits
	// the SBoM parse; the operator is asserting "no known vulns".
	var baseline releaseinstall.SBOMBaseline
	if *fromZero {
		baseline = releaseinstall.KGVZero(*gitSHA)
	} else {
		// Read the on-disk manifest. The manifest is the source of
		// truth for what git-sha is on disk; an unset git_sha in
		// the manifest is a producer-side bug (the canary should
		// have stamped it), so we surface it as a hard error.
		m, err := releaseinstall.Read(*releasesRoot, *gitSHA)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv rotate: read manifest at %s: %v\n",
				releaseinstall.BundleRoot(*releasesRoot, *gitSHA), err)
			return 3
		}
		if m.GitSHA != *gitSHA {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv rotate: manifest git_sha=%q does not match --git-sha=%q\n",
				m.GitSHA, *gitSHA)
			return 3
		}
		// The SBoM is required for the parse path. The legacy
		// `release bundle` flow (no --tarball-path) does not write
		// one. Operators on the legacy path can use --from-zero
		// to assert the baseline without an SBoM.
		sbomPath := releaseinstall.BundleRoot(*releasesRoot, *gitSHA) + "/release.sbom.json"
		sbomBody, err := os.ReadFile(sbomPath)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv rotate: read SBoM at %s: %v\n"+
				"  (operators on the legacy bundle path can use --from-zero to assert a zero baseline)\n", sbomPath, err)
			return 3
		}
		counts, parseErr := releaseinstall.ParseSPDXv2_3(sbomBody)
		if parseErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv rotate: parse SBoM at %s: %v\n", sbomPath, parseErr)
			return 3
		}
		// PR-B review fix #4: the SBoM body must belong to the
		// same git_sha as the manifest. PR-A's install path
		// already enforces this on the manifest side; the SBoM
		// is the second producer-side artifact and can outlive
		// a manual directory swap (e.g., a stale `release
		// install --tarball-path` from a prior git_sha that
		// ended up under this dir). Path-based isolation
		// (<root>/<git-sha>/) catches dir mix-up but not
		// in-dir staleness. The canary stamps the SPDX
		// documentNamespace with the git_sha (see
		// scripts/build-canonical-tarball.sh); if present,
		// assert it matches. Absence is permissive (older
		// syft + hand-crafted SBoMs may not stamp it).
		var sbomDoc struct {
			DocumentNamespace string `json:"documentNamespace"`
			Name              string `json:"name"`
		}
		if jsonErr := json.Unmarshal(sbomBody, &sbomDoc); jsonErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv rotate: decode SBoM envelope at %s: %v\n", sbomPath, jsonErr)
			return 3
		}
		stamped := sbomDoc.DocumentNamespace
		if stamped == "" {
			stamped = sbomDoc.Name
		}
		// Detect a clearly-stamped wrong git_sha. The canary
		// convention is `https://gregale.dev/spdxdocs/<git_sha>`;
		// look for any 40-char lowercase hex in the stamped
		// field and assert it equals --git-sha.
		if stamped != "" && !sbomDocMatchesGitSHA(stamped, *gitSHA) {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv rotate: SBoM documentNamespace=%q does not reference --git-sha=%q; refusing to inherit counts from a stale SBoM\n",
				stamped, *gitSHA)
			return 3
		}
		baseline = releaseinstall.SBOMBaseline{
			GitSHA:    *gitSHA,
			Counts:    counts,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
	if err := releaseinstall.WriteBaseline(*releasesRoot, baseline); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv rotate: write baseline: %v\n", err)
		return 3
	}
	if *jsonOut {
		jsonEmit(os.Stdout, struct {
			GitSHA   string                      `json:"git_sha"`
			Baseline releaseinstall.SBOMBaseline `json:"baseline"`
			FromZero bool                        `json:"from_zero"`
			Path     string                      `json:"path"`
		}{
			GitSHA:   *gitSHA,
			Baseline: baseline,
			FromZero: *fromZero,
			Path:     releaseinstall.AcceptedSBOMBaselinePath(*releasesRoot),
		})
		return 0
	}
	if *fromZero {
		_, _ = fmt.Fprintf(os.Stdout, "OK git_sha=%s baseline=KGVZero path=%s\n",
			*gitSHA, releaseinstall.AcceptedSBOMBaselinePath(*releasesRoot))
		return 0
	}
	_, _ = fmt.Fprintf(os.Stdout, "OK git_sha=%s counts=critical:%d high:%d medium:%d low:%d path=%s\n",
		*gitSHA,
		baseline.Counts.CriticalN, baseline.Counts.HighN, baseline.Counts.MediumN, baseline.Counts.LowN,
		releaseinstall.AcceptedSBOMBaselinePath(*releasesRoot))
	return 0
}

// cmdReleaseKGVVerify compares an incoming, already signature-verified release
// SBoM with the host's previously accepted baseline. It never mutates the
// baseline, which lets CD call it before activation and rotate only after all
// readiness gates pass.
func cmdReleaseKGVVerify(args []string) int {
	if len(args) > 0 && (args[0] == flagHelpLong || args[0] == flagHelpShort) {
		PrintUsage(os.Stderr, "usage: gregalectl release kgv verify --git-sha SHA [--releases-root PATH] [--json]", "release")
		return 0
	}
	fs := flag.NewFlagSet("release kgv verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	gitSHA := fs.String("git-sha", "", "40-char lowercase hex git SHA (required)")
	releasesRoot := fs.String("releases-root", "/opt/faas/releases", "releases root directory")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if !releaseinstall.ValidGitSHA(*gitSHA) {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv verify: --git-sha %q is not a 40-char lowercase hex\n", *gitSHA)
		return 1
	}
	m, err := releaseinstall.Read(*releasesRoot, *gitSHA)
	if err != nil || m.GitSHA != *gitSHA {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv verify: release manifest mismatch: %v\n", err)
		return 3
	}
	sbomPath := releaseinstall.BundleRoot(*releasesRoot, *gitSHA) + "/release.sbom.json"
	sbomBody, err := os.ReadFile(sbomPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv verify: read SBoM at %s: %v\n", sbomPath, err)
		return 3
	}
	counts, err := releaseinstall.ParseSPDXv2_3(sbomBody)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv verify: parse SBoM at %s: %v\n", sbomPath, err)
		return 3
	}
	var sbomDoc struct {
		DocumentNamespace string `json:"documentNamespace"`
		Name              string `json:"name"`
	}
	if err := json.Unmarshal(sbomBody, &sbomDoc); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv verify: decode SBoM envelope at %s: %v\n", sbomPath, err)
		return 3
	}
	stamped := sbomDoc.DocumentNamespace
	if stamped == "" {
		stamped = sbomDoc.Name
	}
	if stamped != "" && !sbomDocMatchesGitSHA(stamped, *gitSHA) {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv verify: SBoM documentNamespace=%q does not reference --git-sha=%q; refusing a stale SBoM\n", stamped, *gitSHA)
		return 3
	}
	baseline, err := releaseinstall.ReadBaseline(*releasesRoot, *gitSHA)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv verify: read accepted baseline at %s: %v\n", releaseinstall.AcceptedSBOMBaselinePath(*releasesRoot), err)
		return 3
	}
	if _, err := baseline.Diff(counts); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release kgv verify: %v\n", err)
		return 3
	}
	if *jsonOut {
		jsonEmit(os.Stdout, struct {
			GitSHA      string                    `json:"git_sha"`
			BaselineSHA string                    `json:"baseline_git_sha"`
			Baseline    releaseinstall.SBOMCounts `json:"baseline_counts"`
			Incoming    releaseinstall.SBOMCounts `json:"incoming_counts"`
		}{GitSHA: *gitSHA, BaselineSHA: baseline.GitSHA, Baseline: baseline.Counts, Incoming: counts})
		return 0
	}
	_, _ = fmt.Fprintf(os.Stdout, "OK git_sha=%s baseline_git_sha=%s counts=critical:%d high:%d\n", *gitSHA, baseline.GitSHA, counts.CriticalN, counts.HighN)
	return 0
}

// sbomDocMatchesGitSHA returns true when `stamped` is permissive of
// the operator-supplied git_sha. The canary stamps
// `documentNamespace` (or `name`) with the git_sha; the function
// returns true if (a) no 40-char lowercase hex is present (older
// SBoMs without stamping are allowed through), or (b) the stamped
// SHA equals `gitSHA`. A stamped SHA that disagrees is the leak
// vector — see PR-B review fix #4.
//
// Defensive parsing: instead of validating the full namespace
// shape, we extract any 40-char lowercase hex substring and compare
// it directly. This covers the canary's
// `https://gregale.dev/spdxdocs/<sha>` convention without coupling
// to the path scheme.
func sbomDocMatchesGitSHA(stamped, gitSHA string) bool {
	if !releaseinstall.ValidGitSHA(gitSHA) {
		return true // gate above already rejected; defensive only
	}
	// Scan for any 40-char lowercase hex substring.
	for i := 0; i+40 <= len(stamped); i++ {
		candidate := stamped[i : i+40]
		if !releaseinstall.ValidGitSHA(candidate) {
			continue
		}
		// Found a stamped SHA. Match means OK; mismatch means leak.
		return candidate == gitSHA
	}
	// No stamped SHA present — older SBoM without stamping;
	// permissive pass-through (the dir-based git_sha check above
	// is the load-bearing guard).
	return true
}
