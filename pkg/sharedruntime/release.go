package sharedruntime

import (
	"path"
	"strings"
)

// ReasonReleasePackageManager is the reason a build keeps its toolchain
// because its release command calls a package manager the runner base does
// not ship.
const ReasonReleasePackageManager = "release_package_manager"

var nodePackageManagers = map[string]bool{"npm": true, "npx": true, "yarn": true, "pnpm": true, "bun": true, "corepack": true, "mise": true}

// ReleaseNeedsPackageManager reports whether a deployment's release command
// calls a package manager the runtime's runner base does not ship. The runner
// bases strip npm and the Python package managers, so `release: npm run
// migrate` would exit 127 on an /app-only layer; such a build keeps the
// toolchain its image installed. A command counts when any of its words names
// one, since `cd db && npm run migrate` or `sh -c "npm run migrate"` calls it.
func ReleaseNeedsPackageManager(runtime string, command []string, shell bool) bool {
	if len(command) == 0 {
		return false
	}
	isManager := func(word string) bool { return nodePackageManagers[path.Base(word)] }
	if PythonRuntime(runtime) {
		isManager = PythonPackageManager
	}
	// Exec form is scanned word by word too: ["sh", "-c", "npm run x"] and
	// ["env", "A=1", "npx", ...] reach a package manager through a wrapper.
	// A false positive only keeps the toolchain; a miss exits 127. shell is
	// kept for callers that pass the deployment's form.
	for _, script := range command {
		for _, word := range strings.FieldsFunc(script, shellWordBreak) {
			if isManager(word) {
				return true
			}
		}
	}
	return false
}

func shellWordBreak(r rune) bool {
	switch r {
	case ' ', '\t', '\n', ';', '&', '|', '(', ')', '`', '"', '\'', '$', '{', '}', '<', '>':
		return true
	}
	return false
}
