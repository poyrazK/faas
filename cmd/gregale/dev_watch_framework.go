package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// devWatchHostArgs makes a development server listen on every interface.
// The gateway reaches the guest at 10.0.0.2 (ADR-009), so a server on
// localhost fails its boot with app_loopback_bound.
const devWatchHostArgs = "--host 0.0.0.0"

// devWatchFramework is a development server watch mode can start without a
// dev script (ADR-1170 phase 3). Order matters: meta-frameworks built on
// Vite come before Vite itself.
type devWatchFramework struct {
	name    string
	deps    []string // every one must be in package.json
	command string   // already carries devWatchHostArgs where needed
}

var devWatchFrameworks = []devWatchFramework{
	{name: "Nuxt", deps: []string{"nuxt"}, command: "nuxt dev " + devWatchHostArgs},
	{name: "Astro", deps: []string{"astro"}, command: "astro dev " + devWatchHostArgs},
	{name: "SvelteKit", deps: []string{"@sveltejs/kit"}, command: "vite dev " + devWatchHostArgs},
	{name: "React Router", deps: []string{"@react-router/dev"}, command: "react-router dev " + devWatchHostArgs},
	{name: "Remix", deps: []string{"@remix-run/dev", "vite"}, command: "remix vite:dev " + devWatchHostArgs},
	// next dev listens on every interface unless given --hostname.
	{name: "Next.js", deps: []string{"next"}, command: "next dev"},
	{name: "Vite", deps: []string{"vite"}, command: "vite " + devWatchHostArgs},
}

// devWatchPackage is the part of package.json watch mode reads.
type devWatchPackage struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func (p devWatchPackage) has(dep string) bool {
	_, prod := p.Dependencies[dep]
	_, dev := p.DevDependencies[dep]
	return prod || dev
}

// framework returns the first known framework the package depends on.
func (p devWatchPackage) framework() (devWatchFramework, bool) {
	for _, fw := range devWatchFrameworks {
		matched := true
		for _, dep := range fw.deps {
			matched = matched && p.has(dep)
		}
		if matched {
			return fw, true
		}
	}
	return devWatchFramework{}, false
}

// devWatchDefault picks the command for watch mode without --watch-command:
// the package's dev script, with devWatchHostArgs appended when it starts a
// server that would listen on localhost, else the detected framework's own
// development command. notes are hints to show the developer.
func devWatchDefault(sourceDir string, pkg devWatchPackage) (command string, notes []string, ok bool) {
	if script := strings.TrimSpace(pkg.Scripts["dev"]); script != "" {
		command = devWatchDefaultCommand
		switch devScriptHostBinding(script) {
		case devScriptLoopback:
			command += " -- " + devWatchHostArgs
		case devScriptComposite:
			notes = append(notes, "your dev script runs several commands; make sure the development server listens on 0.0.0.0 (for Vite-based servers: "+devWatchHostArgs+"), a server on localhost cannot be reached")
		}
	} else if fw, found := pkg.framework(); found {
		command = fw.command
	} else {
		return "", nil, false
	}
	if pkg.has("next") && !nextConfigAllowsDevOrigins(sourceDir) {
		notes = append(notes, "Next.js blocks hot reload from origins other than localhost; add allowedDevOrigins: [process.env."+api.DevWatchHostEnv+"] to next.config")
	}
	return command, notes, true
}

type devScriptBinding int

const (
	devScriptListensEverywhere devScriptBinding = iota // or unknown: leave it alone
	devScriptLoopback
	devScriptComposite
)

// devScriptHostBinding classifies a dev script by the server it starts.
// Only a single command whose development server defaults to localhost gets
// host arguments appended; a script that already names a host is left as
// written, and a composite one cannot be extended safely.
func devScriptHostBinding(script string) devScriptBinding {
	fields := strings.Fields(script)
	for i, field := range fields {
		fields[i] = strings.Trim(field, `"'`) // concurrently "vite" "tsc -w"
	}
	loopback := false
	for i := 0; i < len(fields); i++ {
		if devScriptLoopbackServer(fields, i) {
			loopback = true
			break
		}
	}
	if !loopback {
		return devScriptListensEverywhere
	}
	if strings.Contains(script, "--host") {
		return devScriptListensEverywhere
	}
	if strings.ContainsAny(script, ";|&") || strings.Contains(script, "concurrently") || strings.Contains(script, "run-p") {
		return devScriptComposite
	}
	return devScriptLoopback
}

// devScriptLoopbackServer reports whether fields[i] starts a development
// server that listens on localhost by default.
func devScriptLoopbackServer(fields []string, i int) bool {
	next := ""
	if i+1 < len(fields) {
		next = fields[i+1]
	}
	switch fields[i] {
	case "vite":
		// vite build and vite preview are not the development server.
		return next != "build" && next != "preview" && next != "optimize"
	case "astro", "nuxt", "nuxi", "react-router":
		return next == "dev"
	case "remix":
		return next == "vite:dev"
	}
	return false
}

// nextConfigAllowsDevOrigins reports whether a next.config file sets
// allowedDevOrigins. Next.js reads it only from its config file.
func nextConfigAllowsDevOrigins(sourceDir string) bool {
	for _, name := range []string{"next.config.js", "next.config.mjs", "next.config.cjs", "next.config.ts", "next.config.mts"} {
		data, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err == nil && strings.Contains(string(data), "allowedDevOrigins") {
			return true
		}
	}
	return false
}
