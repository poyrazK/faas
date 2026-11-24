package sharedruntime

import (
	"encoding/json"
	"path"
	"strings"
)

// Bun on the shared Node runner base (ADR-20261124160000). Railpack installs
// Bun and, for any app with dependencies, Node with mise. The runner base
// already has Node, so a Bun app keeps only Bun's own install from the
// toolchain: about 90 MB instead of the whole mise tree.

// bunInstallRoot is mise's Bun install directory as a tar path.
const bunInstallRoot = "mise/installs/bun"

// Reasons a Bun app keeps its toolchain.
const (
	// ReasonBunNotInstalled: the start command runs Bun, but the artifact
	// has no Bun install under mise to keep.
	ReasonBunNotInstalled = "bun_not_installed"
	// ReasonBunInScript: the start command reaches Bun only through a
	// package manager script, and the package manager is not on the runner
	// base.
	ReasonBunInScript = "bun_in_script"
)

// UsesBun reports whether any word of command runs Bun: `bun run start`,
// `bunx serve`, `NODE_ENV=production bun index.ts`,
// `/mise/shims/bun start` and the like. A false positive only makes imaged
// look for Bun's install.
func UsesBun(command string) bool {
	for _, word := range strings.FieldsFunc(command, shellWordBreak) {
		if name := path.Base(word); name == "bun" || name == "bunx" {
			return true
		}
	}
	return false
}

// nodeScriptRunners run other package.json scripts through the package
// manager that started them, or through npm.
var nodeScriptRunners = map[string]bool{"npm-run-all": true, "npm-run-all2": true, "run-p": true, "run-s": true, "concurrently": true}

// scriptRunners are the package managers whose next word may name a script:
// `bun start`, `npm start`, `yarn serve`.
var scriptRunners = map[string]bool{"bun": true, "npm": true, "pnpm": true, "yarn": true}

// bunScriptDepth bounds how many package.json scripts a start command is
// followed through.
const bunScriptDepth = 32

// BunStartNeedsNodePackageManager reports whether a Bun start command, or
// any package.json script it reaches (through `bun run <name>`,
// `bun <name>` or `<anything> run <name>`, flags allowed before the name,
// with their pre/post hooks), calls npm, npx, yarn, pnpm, corepack, mise or
// a script runner such as npm-run-all or concurrently. Those came with
// mise's Node or call npm, which a Bun app layer drops, so such an app keeps
// its toolchain. Unreadable package.json, or more scripts than
// bunScriptDepth, count as needing it: a false positive only keeps the
// toolchain.
func BunStartNeedsNodePackageManager(command string, packageJSON []byte) bool {
	return walkStartScripts(command, packageJSON, func(name string) bool {
		return name != "bun" && name != "bunx" && (nodePackageManagers[name] || nodeScriptRunners[name])
	})
}

// StartReachesBun reports whether command, or a package.json script it
// reaches, runs Bun. Unreadable package.json counts as reaching it.
func StartReachesBun(command string, packageJSON []byte) bool {
	return walkStartScripts(command, packageJSON, func(name string) bool {
		return name == "bun" || name == "bunx"
	})
}

// walkStartScripts reports whether hit accepts the base name of any word of
// command or of a package.json script it reaches. An unreadable
// package.json, or more scripts than bunScriptDepth, count as a hit.
func walkStartScripts(command string, packageJSON []byte, hit func(name string) bool) bool {
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if len(packageJSON) > 0 && json.Unmarshal(packageJSON, &pkg) != nil {
		return true
	}
	queue := []string{command}
	seen := map[string]bool{}
	for len(queue) > 0 {
		text := queue[0]
		queue = queue[1:]
		// expect: the next word that is not a flag may name a script.
		// afterFlag: the previous word was a flag, so a word that names no
		// script may be its value, and the name may still follow.
		expect, afterFlag := false, false
		for _, word := range strings.FieldsFunc(text, shellWordBreak) {
			name := path.Base(word)
			if hit(name) {
				return true
			}
			switch {
			case word == "run" || word == "run-script" || scriptRunners[name]:
				expect, afterFlag = true, false
				continue
			case !expect:
				continue
			case strings.HasPrefix(word, "-"):
				afterFlag = true
				continue
			}
			scripts := 0
			for _, script := range []string{"pre" + word, word, "post" + word} {
				body, ok := pkg.Scripts[script]
				if !ok {
					continue
				}
				scripts++
				if seen[script] {
					continue
				}
				if len(seen) >= bunScriptDepth {
					return true
				}
				seen[script] = true
				queue = append(queue, body)
			}
			expect = scripts == 0 && afterFlag
			afterFlag = false
		}
	}
	return false
}

// BunInstallEntry reports whether name, a clean relative tar path, is part
// of mise's Bun install.
func BunInstallEntry(name string) bool {
	return name == bunInstallRoot || strings.HasPrefix(name, bunInstallRoot+"/")
}

// BunBinDir returns the absolute bin directory of a Bun install when name is
// its bun executable ("mise/installs/bun/1.3.5/bin/bun"), else "".
func BunBinDir(name string) string {
	rest, ok := strings.CutPrefix(name, bunInstallRoot+"/")
	if !ok {
		return ""
	}
	version, bin, ok := strings.Cut(rest, "/")
	if !ok || version == "" || bin != "bin/bun" {
		return ""
	}
	return "/" + path.Join(bunInstallRoot, version, "bin")
}

// BunRunnerPath is RunnerPath with a Bun bin directory after the app's own
// binaries.
func BunRunnerPath(binDir string) string {
	return "/app/node_modules/.bin:" + binDir + strings.TrimPrefix(runnerPath, "/app/node_modules/.bin")
}
