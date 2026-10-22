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
}

// devServerNodeFramework is a Node.js development server started without a
// dev script. Order matters: meta-frameworks built on Vite come first.
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
	if strings.TrimSpace(pkg.Scripts["dev"]) != "" {
		// Railpack starts the production app with the same package manager,
		// so the runtime image has it.
		return DevServer{Command: nodePackageManager(fsys, pkg.PackageManager) + " run dev", Framework: "dev script"}, true
	}
	declared := func(dep string) bool {
		_, prod := pkg.Dependencies[dep]
		_, dev := pkg.DevDependencies[dep]
		return prod || dev
	}
	for _, fw := range devServerNodeFrameworks {
		matched := true
		for _, dep := range fw.deps {
			matched = matched && declared(dep)
		}
		if matched {
			return DevServer{Command: fw.command, Framework: fw.name}, true
		}
	}
	return DevServer{}, false
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
