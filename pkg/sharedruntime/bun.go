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
// `bun start`, `bun index.ts` and the like.
func BunStart(command string) bool {
	fields := strings.Fields(command)
	return len(fields) > 0 && fields[0] == "bun"
}

// BunStartNeedsNodePackageManager reports whether a Bun start command, or
// the package.json script it runs with its pre/post hooks, calls npm, npx,
// yarn, pnpm, corepack or mise. Those came with mise's Node, which a Bun
// app layer drops, so such an app keeps its toolchain. A false positive
// only keeps the toolchain.
func BunStartNeedsNodePackageManager(command string, packageJSON []byte) bool {
	texts := []string{command}
	fields := strings.Fields(command)
	script := ""
	switch {
	case len(fields) == 2 && fields[1] == "start":
		script = "start"
	case len(fields) >= 3 && fields[1] == "run":
		script = fields[2]
	}
	if script != "" {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(packageJSON, &pkg) != nil {
			return true
		}
		texts = append(texts, pkg.Scripts[script], pkg.Scripts["pre"+script], pkg.Scripts["post"+script])
	}
	for _, text := range texts {
		for _, word := range strings.FieldsFunc(text, shellWordBreak) {
			if name := path.Base(word); name != "bun" && name != "bunx" && nodePackageManagers[name] {
				return true
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
