package main

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Reseed coverage (ADR-20261109140000). The ADR-680 barrier reseeds only the
// processes that registered with it. It passes with zero registrations, so a
// Node or Python process the preload never reached (python -S/-E/-I, a child
// started with a cleared environment, a sidecar with its own root filesystem)
// and every Bun or Deno process (they ignore NODE_OPTIONS) wake with the
// snapshot's random state and nothing says so. After each barrier pass
// guest-init lists the running interpreter processes that did not register and
// reports them to vmmd in the resume timing frame. Restores are not refused.

// reseedRuntimeForExe maps an executable's base name to the runtime label
// vmmd counts, or "" for a process the barrier does not concern.
func reseedRuntimeForExe(exe string) string {
	base := path.Base(exe)
	// A replaced binary reads as "node (deleted)" in /proc/<pid>/exe.
	base = strings.TrimSuffix(base, " (deleted)")
	switch {
	case base == "node" || base == "nodejs":
		return "node"
	case base == "bun":
		return "bun"
	case base == "deno":
		return "deno"
	case base == "java":
		// ADR-20261110150000: a JVM older than 16, or one started without
		// the stamped JAVA_TOOL_OPTIONS, does not register.
		return "java"
	case pythonExe.MatchString(base):
		return "python"
	}
	return ""
}

var pythonExe = regexp.MustCompile(`^(python|pypy)[0-9.]*$`)

// packageManagerLauncher mirrors node-reseed.cjs isPackageManagerLauncher: a
// Node process whose entry script is npm, npx, yarn, pnpm or corepack only
// starts the app and deliberately does not register (ADR-680 amendment
// 2026-10-11).
var packageManagerLauncher = regexp.MustCompile(`[\\/]node_modules[\\/](npm[\\/]bin[\\/](npm|npx)-cli\.js|corepack[\\/]dist[\\/](yarn|yarnpkg|pnpm|pnpx|npm|npx|corepack)\.js|yarn[\\/]bin[\\/]yarn\.c?js|pnpm[\\/]bin[\\/](pnpm|pnpx)\.c?js)$`)

// reseedCandidate is one running process as /proc describes it.
type reseedCandidate struct {
	pid int
	// exe is the /proc/<pid>/exe target; entry is argv[1] after symlinks.
	exe   string
	entry string
	// optedOut is GREGALE_RESTORE_RESEED=off in the process environment.
	optedOut bool
	// ownRoot is a process chrooted into its own root filesystem (a sidecar).
	ownRoot bool
	// runtime overrides the executable-name label: "go" for a workload
	// executable carrying Go build info (ADR-20261110150000).
	runtime string
}

// unregisteredRuntimes returns the sorted, de-duplicated runtime labels of
// candidates the barrier should have reached and did not, and how many
// processes that is. A sidecar's label carries a "-sidecar" suffix.
func unregisteredRuntimes(candidates []reseedCandidate, registered map[int]bool) (int, []string) {
	seen := map[string]bool{}
	count := 0
	for _, c := range candidates {
		runtime := c.runtime
		if runtime == "" {
			runtime = reseedRuntimeForExe(c.exe)
		}
		if runtime == "" || registered[c.pid] || c.optedOut {
			continue
		}
		if runtime == "node" && packageManagerLauncher.MatchString(c.entry) {
			continue
		}
		if c.ownRoot {
			runtime += "-sidecar"
		}
		count++
		seen[runtime] = true
	}
	labels := make([]string, 0, len(seen))
	for label := range seen {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return count, labels
}
