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

// BunStart reports whether command runs through Bun: `bun run start`,
// `bun start`, `bun index.ts`, `bunx serve` and the like.
func BunStart(command string) bool {
	fields := strings.Fields(command)
	return len(fields) > 0 && (fields[0] == "bun" || fields[0] == "bunx")
}

// bunScriptDepth bounds how many package.json scripts a start command is
// followed through.
const bunScriptDepth = 32

// BunStartNeedsNodePackageManager reports whether a Bun start command, or
// any package.json script it reaches (through `bun run <name>`,
// `bun <name>` or `<anything> run <name>`, with their pre/post hooks),
// calls npm, npx, yarn, pnpm, corepack or mise. Those came with mise's
// Node, which a Bun app layer drops, so such an app keeps its toolchain.
// Unreadable package.json, or more scripts than bunScriptDepth, count as
// needing it: a false positive only keeps the toolchain.
func BunStartNeedsNodePackageManager(command string, packageJSON []byte) bool {
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
		words := strings.FieldsFunc(text, shellWordBreak)
		for i, word := range words {
			name := path.Base(word)
			if name != "bun" && name != "bunx" && nodePackageManagers[name] {
				return true
			}
			// A script name after `run`, or straight after `bun`.
			if i == 0 || (words[i-1] != "run" && words[i-1] != "run-script" && path.Base(words[i-1]) != "bun") {
				continue
			}
			for _, script := range []string{"pre" + word, word, "post" + word} {
				body, ok := pkg.Scripts[script]
				if !ok || seen[script] {
					continue
				}
				if len(seen) >= bunScriptDepth {
					return true
				}
				seen[script] = true
				queue = append(queue, body)
			}
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
