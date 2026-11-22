package sharedruntime

import (
	"path"
	"strings"
)

// nodePackageManagers are the Node package managers the shared runner bases
// strip (images/runner-node*.Dockerfile): the base scan gate refuses every
// cold boot on a base with a critical finding, so npm's bundled dependencies
// stay out of the shared drive0.
var nodePackageManagers = map[string]bool{
	"npm": true, "npx": true, "yarn": true, "yarnpkg": true, "pnpm": true, "pnpx": true, "bun": true, "bunx": true, "corepack": true,
}

// CommandUsesPackageManager reports whether a deployment command (a release
// command, for example) invokes a Node or Python package manager the shared
// runner bases do not ship. Only the start command is rewritten for the
// shared base (ResolveStart); any other command runs as written, so
// `release: npm run migrate` exited 127 "npm: not found" on the runner base
// (prod 2026-11-22). A shell command is scanned word by word, so a match
// anywhere in the script counts; a false positive only keeps the app's own
// base.
func CommandUsesPackageManager(command []string, shell bool) bool {
	if len(command) == 0 {
		return false
	}
	if shell {
		return scriptUsesPackageManager(strings.Join(command, " "))
	}
	if packageManagerProgram(command[0]) {
		return true
	}
	// An argv that runs a shell script: sh -c "npm run migrate".
	switch path.Base(command[0]) {
	case "sh", "bash", "dash", "ash", "zsh":
		for _, arg := range command[1:] {
			if scriptUsesPackageManager(arg) {
				return true
			}
		}
	}
	return false
}

func scriptUsesPackageManager(script string) bool {
	words := strings.FieldsFunc(script, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ';', '&', '|', '(', ')', '`', '{', '}', '<', '>', '!':
			return true
		}
		return false
	})
	for _, word := range words {
		if packageManagerProgram(strings.Trim(word, `"'$`)) {
			return true
		}
	}
	return false
}

func packageManagerProgram(program string) bool {
	if program == "" {
		return false
	}
	name := path.Base(program)
	return nodePackageManagers[name] || pythonPackageManagers[name]
}
