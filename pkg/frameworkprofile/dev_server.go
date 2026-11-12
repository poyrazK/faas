package frameworkprofile

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/onebox-faas/faas/pkg/markers"
)

// DevServer is the development command `gregale dev --watch` runs when the
// developer names none (ADR-1170). The commands carry no host or port
// flags: in a watch-mode image guest-init bridges the runtime port to
// whatever the server listens on, localhost included.
type DevServer struct {
	Command string
	// Framework names what was detected, for messages ("Next.js",
	// "dev script", "Django", ...).
	Framework string
	// HMRFramework identifies a known Node development server that can own
	// source reload locally. Empty means the command needs CLI supervision.
	// Keep Framework as "dev script" for package-manager commands.
	HMRFramework string
}

// devServerNodeFramework identifies a Node.js development command, including
// direct dev scripts. Order matters: meta-frameworks built on Vite come first.
type devServerNodeFramework struct {
	name    string
	deps    []string // every one must be declared
	command string
}

var devServerNodeFrameworks = []devServerNodeFramework{
	{name: "Nuxt", deps: []string{"nuxt"}, command: "nuxt dev"},
	{name: "Astro", deps: []string{"astro"}, command: "astro dev"},
	{name: "SvelteKit", deps: []string{"@sveltejs/kit"}, command: "vite dev"},
	{name: "React Router", deps: []string{"@react-router/dev"}, command: "react-router dev"},
	{name: "Remix", deps: []string{"@remix-run/dev", "vite"}, command: "remix vite:dev"},
	{name: "Next.js", deps: []string{"next"}, command: "next dev"},
	{name: "Vite", deps: []string{"vite"}, command: "vite"},
}

// DevServerCommand picks the development command for the source in fsys:
// for Node.js the package's dev script through its package manager, else a
// known framework's own command; for Python a reloading server for Django,
// FastAPI or Flask. ok is false when nothing fits; the developer then names
// a command.
func DevServerCommand(fsys fs.FS) (DevServer, bool) {
	framework, err := markers.DetectFromFS(fsys)
	if err != nil {
		return DevServer{}, false
	}
	switch framework {
	case markers.FrameworkNode:
		return nodeDevServer(fsys)
	case markers.FrameworkPython:
		return pythonDevServer(fsys)
	}
	return DevServer{}, false
}

func nodeDevServer(fsys fs.FS) (DevServer, bool) {
	var pkg struct {
		PackageManager  string            `json:"packageManager"`
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal([]byte(readFile(fsys, "package.json")), &pkg) != nil {
		return DevServer{}, false
	}
	declared := func(dep string) bool {
		_, prod := pkg.Dependencies[dep]
		_, dev := pkg.DevDependencies[dep]
		return prod || dev
	}
	script := strings.TrimSpace(pkg.Scripts["dev"])
	server := DevServer{}
	if script != "" {
		// Preserve the user's script and the package manager used by Railpack.
		server = DevServer{Command: nodePackageManager(fsys, pkg.PackageManager) + " run dev", Framework: "dev script"}
	}
	for _, fw := range devServerNodeFrameworks {
		matched := true
		for _, dep := range fw.deps {
			matched = matched && declared(dep)
		}
		if matched {
			if script == "" {
				return DevServer{Command: fw.command, Framework: fw.name, HMRFramework: fw.name}, true
			}
			if nodeHMRScript(script, fw.command) {
				server.HMRFramework = fw.name
				break
			}
		}
	}
	if script != "" {
		return server, true
	}
	return DevServer{}, false
}

// Only recognise simple direct invocations. Shell wrappers, assignments and
// pipelines can change what actually runs; retain supervised reload for those.
func nodeHMRScript(script, command string) bool {
	if strings.ContainsAny(script, "\r\n&|;<>$`\\\"'(){}[]*?!#~") {
		return false
	}
	args, expected := strings.Fields(script), strings.Fields(command)
	if len(args) == 0 || args[0] != expected[0] {
		return false
	}
	// Vite's dev and serve aliases also power SvelteKit. A preview or build
	// command must never become an HMR owner just because Vite is installed.
	if expected[0] == "vite" {
		return len(args) == 1 || args[1] == "dev" || args[1] == "serve" || strings.HasPrefix(args[1], "-")
	}
	return len(args) >= 2 && args[1] == expected[1]
}

func pythonDevServer(fsys fs.FS) (DevServer, bool) {
	var content strings.Builder
	for _, name := range []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py"} {
		content.WriteString(stripPythonDependencyComments(readFile(fsys, name)))
		content.WriteByte('\n')
	}
	all := strings.ToLower(content.String())
	switch {
	case fileExists(fsys, "manage.py") && (pythonDependencyNamed(all, "django") || !pythonDependencyNamed(all, "fastapi")):
		return DevServer{Command: "python manage.py runserver", Framework: "Django"}, true
	case pythonDependencyNamed(all, "fastapi"):
		if module, symbol, ok := findPythonApplication(fsys, `FastAPI\s*\(`); ok {
			return DevServer{Command: fmt.Sprintf("uvicorn %s:%s --reload", module, symbol), Framework: "FastAPI"}, true
		}
	case pythonDependencyNamed(all, "flask"):
		if module, symbol, ok := findPythonApplication(fsys, `Flask\s*\(`); ok {
			return DevServer{Command: fmt.Sprintf("flask --app %s:%s run --debug", module, symbol), Framework: "Flask"}, true
		}
	}
	return DevServer{}, false
}
