package main

// `gregalectl release verify` (ADR-20261110183000, binary integrity): proves
// the platform binaries on this host are the signed release's. A timer runs it
// hourly; its metrics and journal lines leave the host, so a change an
// intruder makes to /opt/faas shows up off host.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

const subReleaseVerify = "verify"

// releaseVerifyReport is what one run found.
type releaseVerifyReport struct {
	Release        string   `json:"release"`
	ManifestDigest string   `json:"manifest_digest,omitempty"`
	FilesOK        bool     `json:"files_ok"`
	Processes      int      `json:"processes"`
	Failures       []string `json:"failures,omitempty"`
}

// OK reports a run with a verified current release and no failure.
func (r releaseVerifyReport) OK() bool {
	return r.Release != "" && r.FilesOK && len(r.Failures) == 0
}

func cmdReleaseVerify(args []string) int {
	fs := flag.NewFlagSet("release verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	releasesRoot := fs.String("releases-root", "/opt/faas/releases", "releases root directory")
	procRoot := fs.String("proc-root", "/proc", "process table to scan")
	metricsFile := fs.String("metrics-file", "", "optional node_exporter textfile output")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: gregalectl release verify [--releases-root PATH] [--metrics-file PATH] [--json]")
		return 2
	}
	report := releaseVerify(*releasesRoot, *procRoot)
	if err := writePromTextfile(*metricsFile, releaseVerifyMetrics(report, time.Now())); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release verify: write metrics: %v\n", err)
		return 3
	}
	for _, failure := range report.Failures {
		_, _ = fmt.Fprintf(os.Stderr, "release integrity FAIL: %s\n", failure)
	}
	if *jsonOut || jsonOutput {
		_ = json.NewEncoder(osStdout).Encode(report)
	} else if report.OK() {
		_, _ = fmt.Fprintf(osStdout, "release integrity OK: release %s, signed manifest %s, %d platform processes\n", report.Release, report.ManifestDigest, report.Processes)
	}
	if !report.OK() {
		return 1
	}
	return 0
}

// releaseVerify checks the current release's files against its signed
// manifest, then every process running a binary under the platform tree:
// it must run from an installed release that verifies, from a file that has
// not been replaced since it started.
func releaseVerify(releasesRoot, procRoot string) releaseVerifyReport {
	var report releaseVerifyReport
	current, err := releaseinstall.CurrentGitSHA(releasesRoot)
	if err != nil || current == "" {
		report.Failures = append(report.Failures, fmt.Sprintf("no current release under %s: %v", releasesRoot, err))
		return report
	}
	report.Release = current
	verified := map[string]error{}
	check := func(sha string) error {
		if result, done := verified[sha]; done {
			return result
		}
		m, err := releaseinstall.Read(releasesRoot, sha)
		if err == nil {
			err = releaseinstall.Verify(releasesRoot, m)
		}
		if err == nil && sha == current {
			report.ManifestDigest, err = releaseinstall.SignedManifestDigest(m)
		}
		verified[sha] = err
		return err
	}
	if err := check(current); err != nil {
		report.Failures = append(report.Failures, fmt.Sprintf("release %s: %v", current, err))
	} else {
		report.FilesOK = true
	}
	platformRoot := filepath.Dir(releasesRoot) + string(filepath.Separator)
	var skipped []string
	for _, tree := range releaseVerifySkippedTrees {
		skipped = append(skipped, filepath.Join(filepath.Dir(releasesRoot), tree)+string(filepath.Separator))
	}
	releasesPrefix := filepath.Clean(releasesRoot) + string(filepath.Separator)
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		report.Failures = append(report.Failures, fmt.Sprintf("read %s: %v", procRoot, err))
		return report
	}
	for _, entry := range entries {
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid <= 0 {
			continue
		}
		exe, linkErr := os.Readlink(filepath.Join(procRoot, entry.Name(), "exe"))
		if linkErr != nil || !strings.HasPrefix(exe, platformRoot) {
			continue // kernel threads, exited processes, everything outside /opt/faas
		}
		if releaseVerifySkips(exe, skipped) {
			continue
		}
		report.Processes++
		if path, deleted := strings.CutSuffix(exe, " (deleted)"); deleted {
			report.Failures = append(report.Failures, fmt.Sprintf("pid %d runs %s, which was replaced or removed after it started", pid, path))
			continue
		}
		rest, inReleases := strings.CutPrefix(exe, releasesPrefix)
		sha, _, _ := strings.Cut(rest, string(filepath.Separator))
		if !inReleases || sha == "" {
			report.Failures = append(report.Failures, fmt.Sprintf("pid %d runs %s, outside any installed release", pid, exe))
			continue
		}
		if err := check(sha); err != nil {
			report.Failures = append(report.Failures, fmt.Sprintf("pid %d runs %s from release %s, which does not verify: %v", pid, exe, sha, err))
		}
	}
	sort.Strings(report.Failures)
	return report
}

// releaseVerifySkippedTrees are the trees under /opt/faas whose programs are
// not part of a release: node join's base pre-stage one-shot runs the next
// release's imaged from prestage/ for a few minutes; the compute edge runs a
// pinned cloudflared from cloudflared/; CI hosts run canary builds from
// canaries/. An audit hotfix under hotfixes/ is not skipped: it is an
// unsigned build and is reported.
var releaseVerifySkippedTrees = []string{"prestage", "cloudflared", "canaries"}

func releaseVerifySkips(exe string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(exe, prefix) {
			return true
		}
	}
	return false
}

func releaseVerifyMetrics(report releaseVerifyReport, now time.Time) string {
	var b strings.Builder
	ok := 0
	if report.OK() {
		ok = 1
	}
	b.WriteString("# HELP faas_release_integrity_ok 1 when the current release's files match its signed manifest and every platform process runs a verified release binary.\n")
	b.WriteString("# TYPE faas_release_integrity_ok gauge\n")
	fmt.Fprintf(&b, "faas_release_integrity_ok %d\n", ok)
	b.WriteString("# HELP faas_release_integrity_failures Failures the last release integrity check found.\n")
	b.WriteString("# TYPE faas_release_integrity_failures gauge\n")
	fmt.Fprintf(&b, "faas_release_integrity_failures %d\n", len(report.Failures))
	b.WriteString("# HELP faas_release_integrity_checked_timestamp_seconds Unix time of the last release integrity check.\n")
	b.WriteString("# TYPE faas_release_integrity_checked_timestamp_seconds gauge\n")
	fmt.Fprintf(&b, "faas_release_integrity_checked_timestamp_seconds %d\n", now.Unix())
	if report.ManifestDigest != "" {
		b.WriteString("# HELP faas_release_manifest_info Digest of the signed manifest of the release this host runs; hosts on one release must agree.\n")
		b.WriteString("# TYPE faas_release_manifest_info gauge\n")
		fmt.Fprintf(&b, "faas_release_manifest_info{release=%q,digest=%q} 1\n", report.Release, report.ManifestDigest)
	}
	return b.String()
}
