// Package frameworkprofile turns a source tree into a reproducible API run
// profile. It is deliberately read-only: deployment still validates and
// authorizes every inferred value server-side.
package frameworkprofile

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/markers"
)

const (
	// Version is the profile inference contract version. A receipt can pin this
	// value so a later inference change is explainable and reproducible.
	Version            = "v1"
	maxSourceFileBytes = 1 << 20
)

// Profile is the inferred run contract for an API source tree.
type Profile struct {
	Version      string `json:"version"`
	Framework    string `json:"framework"`
	FrameworkVer string `json:"framework_version,omitempty"`
	// PackageManager records the lockfile/package-manager choice used by
	// static inference. It is advisory metadata; the builder remains the
	// authority for dependency installation.
	PackageManager string    `json:"package_manager,omitempty"`
	StartCommand   string    `json:"start_command,omitempty"`
	Port           int       `json:"port"`
	HealthPath     string    `json:"health_path"`
	Inferred       bool      `json:"inferred"`
	Warnings       []Warning `json:"warnings,omitempty"`
}

// Warning is actionable profile feedback. Source paths are relative to the
// analyzed tree and never contain file contents or environment values.
type Warning struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Sources []string `json:"sources,omitempty"`
}

// AnalyzeDir analyzes a local directory without executing customer code.
func AnalyzeDir(path string) (Profile, error) {
	return Analyze(os.DirFS(path))
}

// Analyze identifies the most specific supported API profile available from
// the source markers. Unknown sources return a valid profile with warnings;
// malformed files are not fatal because inference must never block an
// explicit Dockerfile or command override.
func Analyze(fsys fs.FS) (Profile, error) {
	framework, err := markers.DetectFromFS(fsys)
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{Version: Version, Framework: string(framework), Port: defaultPort(framework), HealthPath: "/healthz"}

	switch framework {
	case markers.FrameworkNode:
		inferNode(fsys, &profile)
	case markers.FrameworkPython:
		inferPython(fsys, &profile)
	case markers.FrameworkGo:
		inferGo(fsys, &profile)
	case markers.FrameworkDocker:
		inferDocker(fsys, &profile)
	default:
		profile.Framework = string(markers.FrameworkUnknown)
		profile.Warnings = append(profile.Warnings, Warning{Code: "framework_not_detected", Message: "No supported API framework marker was found; supply an explicit Dockerfile or command."})
	}
	if framework != markers.FrameworkUnknown {
		profile.FrameworkVer = markers.VersionFromFS(fsys, framework)
	}
	if framework != markers.FrameworkDocker {
		if health := inferHealthPath(fsys); health != "" {
			profile.HealthPath = health
		}
	}
	profile.Warnings = append(profile.Warnings, loopbackWarnings(fsys)...)
	profile.Inferred = profile.StartCommand != "" && profile.Framework != string(markers.FrameworkUnknown)
	return profile, nil
}

func inferNode(fsys fs.FS, profile *Profile) {
	body := readFile(fsys, "package.json")
	var pkg struct {
		Main           string            `json:"main"`
		PackageManager string            `json:"packageManager"`
		Scripts        map[string]string `json:"scripts"`
		Dependencies   map[string]string `json:"dependencies"`
		DevDeps        map[string]string `json:"devDependencies"`
	}
	if body != "" && json.Unmarshal([]byte(body), &pkg) != nil {
		profile.Warnings = append(profile.Warnings, Warning{Code: "package_json_invalid", Message: "package.json could not be parsed; using conservative Node defaults.", Sources: []string{"package.json"}})
	}
	deps := make(map[string]struct{}, len(pkg.Dependencies)+len(pkg.DevDeps))
	for name := range pkg.Dependencies {
		deps[name] = struct{}{}
	}
	for name := range pkg.DevDeps {
		deps[name] = struct{}{}
	}
	switch {
	case hasDependency(deps, "@nestjs/core"):
		profile.Framework = "nestjs"
	case hasDependency(deps, "hono"):
		profile.Framework = "hono"
	case hasDependency(deps, "fastify"):
		profile.Framework = "fastify"
	case hasDependency(deps, "express"):
		profile.Framework = "express"
	default:
		profile.Framework = "node"
	}
	profile.PackageManager = nodePackageManager(fsys, pkg.PackageManager)
	for _, script := range []string{"start:prod", "start"} {
		if strings.TrimSpace(pkg.Scripts[script]) != "" {
			profile.StartCommand = profile.PackageManager + " run " + script
			if looksLikeDevelopmentCommand(pkg.Scripts[script]) {
				profile.Warnings = append(profile.Warnings, Warning{
					Code:    "development_start_command",
					Message: "The inferred start script appears development-only; use a production server for predictable zero-config deploys.",
					Sources: []string{"package.json"},
				})
			}
			return
		}
	}
	for _, main := range []string{pkg.Main, "server.js", "app.js", "index.js"} {
		if main != "" && fileExists(fsys, main) {
			profile.StartCommand = "node " + main
			return
		}
	}
	profile.Warnings = append(profile.Warnings, Warning{Code: "missing_start_command", Message: "No npm start script or conventional Node entrypoint was found; add scripts.start or configure an explicit command.", Sources: []string{"package.json"}})
}

func inferPython(fsys fs.FS, profile *Profile) {
	var content strings.Builder
	for _, name := range []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py"} {
		content.WriteString(strings.ToLower(readFile(fsys, name)))
	}
	all := content.String()
	profile.PackageManager = pythonPackageManager(fsys)
	switch {
	case strings.Contains(all, "fastapi"):
		profile.Framework = "fastapi"
		module, symbol, ok := findPythonApplication(fsys, `FastAPI\s*\(`)
		if ok {
			profile.StartCommand = fmt.Sprintf("uvicorn %s:%s --host 0.0.0.0 --port $PORT", module, symbol)
		} else {
			profile.StartCommand = "uvicorn app:app --host 0.0.0.0 --port $PORT"
			profile.Warnings = append(profile.Warnings, pythonEntrypointWarning("FastAPI", "app:app"))
		}
	case strings.Contains(all, "django") || fileExists(fsys, "manage.py"):
		profile.Framework = "django"
		module, ok := findDjangoWSGI(fsys)
		if !ok {
			module = "app.wsgi"
			profile.Warnings = append(profile.Warnings, pythonEntrypointWarning("Django", "app.wsgi:application"))
		}
		profile.StartCommand = "gunicorn " + module + ":application --bind 0.0.0.0:$PORT"
	case strings.Contains(all, "flask"):
		profile.Framework = "flask"
		module, symbol, ok := findPythonApplication(fsys, `Flask\s*\(`)
		if ok {
			profile.StartCommand = fmt.Sprintf("gunicorn %s:%s --bind 0.0.0.0:$PORT", module, symbol)
		} else {
			profile.StartCommand = "gunicorn app:app --bind 0.0.0.0:$PORT"
			profile.Warnings = append(profile.Warnings, pythonEntrypointWarning("Flask", "app:app"))
		}
	default:
		profile.Framework = "python"
		profile.Warnings = append(profile.Warnings, Warning{Code: "python_framework_not_detected", Message: "Python was detected but no FastAPI, Flask, or Django dependency was found; configure an explicit command.", Sources: []string{"requirements.txt", "pyproject.toml"}})
	}
}

func inferGo(fsys fs.FS, profile *Profile) {
	profile.Framework = "go-net-http"
	profile.PackageManager = "go"
	var files []string
	_ = fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || len(files) >= 128 {
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	for _, path := range files {
		body := readFile(fsys, path)
		if strings.Contains(body, "github.com/gin-gonic/gin") {
			profile.Framework = "gin"
			break
		}
	}
	profile.StartCommand = "go run ."
}

func inferDocker(fsys fs.FS, profile *Profile) {
	profile.Framework = "oci"
	profile.PackageManager = "container"
	body := readFile(fsys, "Dockerfile")
	if port := firstPort(body); port > 0 {
		profile.Port = port
	}
	profile.Warnings = append(profile.Warnings, Warning{Code: "container_command_deferred", Message: "The container entrypoint and health contract will be read from the image; source inference is intentionally conservative.", Sources: []string{"Dockerfile"}})
}

func nodePackageManager(fsys fs.FS, declared string) string {
	if declared != "" {
		name := declared
		if at := strings.IndexByte(name, '@'); at > 0 {
			name = name[:at]
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "npm", "pnpm", "yarn", "bun":
			return strings.ToLower(strings.TrimSpace(name))
		}
	}
	for _, candidate := range []struct {
		file string
		name string
	}{
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"bun.lockb", "bun"},
		{"bun.lock", "bun"},
		{"package-lock.json", "npm"},
		{"npm-shrinkwrap.json", "npm"},
	} {
		if fileExists(fsys, candidate.file) {
			return candidate.name
		}
	}
	return "npm"
}

func pythonPackageManager(fsys fs.FS) string {
	switch {
	case fileExists(fsys, "uv.lock"):
		return "uv"
	case fileExists(fsys, "poetry.lock"):
		return "poetry"
	case fileExists(fsys, "Pipfile.lock"):
		return "pipenv"
	case fileExists(fsys, "requirements.txt"):
		return "pip"
	default:
		return "pip"
	}
}

func looksLikeDevelopmentCommand(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	for _, marker := range []string{"nodemon", "vite", "next dev", "tsx watch", "webpack serve", " --dev", " dev"} {
		if strings.Contains(command, marker) || strings.HasPrefix(command, "dev ") || command == "dev" {
			return true
		}
	}
	return false
}

var healthPathRegex = regexp.MustCompile(`(?i)(?:get|route|handle|head|live|post|put|patch|delete)\s*\(\s*["'](/(?:healthz?|readyz?|livez?|ready|live))["']`)

// inferHealthPath only accepts conventional readiness names. Choosing an
// arbitrary application route as a smoke endpoint is worse than retaining the
// safe default, so business routes are deliberately ignored.
func inferHealthPath(fsys fs.FS) string {
	priority := map[string]int{
		"/healthz": 0,
		"/health":  1,
		"/readyz":  2,
		"/ready":   3,
		"/livez":   4,
		"/live":    5,
	}
	best := ""
	bestRank := len(priority) + 1
	for _, name := range sourceCodeFiles(fsys) {
		for _, match := range healthPathRegex.FindAllStringSubmatch(readFile(fsys, name), -1) {
			if len(match) != 2 {
				continue
			}
			candidate := strings.ToLower(match[1])
			if rank, ok := priority[candidate]; ok && rank < bestRank {
				best, bestRank = candidate, rank
			}
		}
	}
	return best
}

func sourceCodeFiles(fsys fs.FS) []string {
	files := []string{}
	_ = fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			base := filepath.Base(name)
			if name != "." && (base == ".git" || base == "node_modules" || base == "vendor" || strings.HasPrefix(base, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".ts"), strings.HasSuffix(name, ".py"), strings.HasSuffix(name, ".go"):
			if len(files) < 512 {
				files = append(files, name)
			}
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func pythonEntrypointWarning(framework, fallback string) Warning {
	return Warning{
		Code:    "entrypoint_guess",
		Message: fmt.Sprintf("Could not locate the %s application object; using %s. Set an explicit start command if this is not the real entrypoint.", framework, fallback),
		Sources: []string{"*.py"},
	}
}

func findPythonApplication(fsys fs.FS, constructor string) (string, string, bool) {
	assignment := regexp.MustCompile(`(?m)\b([A-Za-z_]\w*)\s*=\s*` + constructor)
	for _, name := range pythonSourceFiles(fsys) {
		body := readFile(fsys, name)
		match := assignment.FindStringSubmatch(body)
		if len(match) == 2 {
			return pythonModuleName(name), match[1], true
		}
	}
	return "", "", false
}

func findDjangoWSGI(fsys fs.FS) (string, bool) {
	for _, name := range pythonSourceFiles(fsys) {
		if filepath.Base(name) != "wsgi.py" {
			continue
		}
		body := readFile(fsys, name)
		if strings.Contains(body, "get_wsgi_application") || strings.Contains(body, "WSGIHandler") {
			return pythonModuleName(name), true
		}
	}
	return "", false
}

func pythonSourceFiles(fsys fs.FS) []string {
	files := []string{}
	_ = fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			base := filepath.Base(name)
			if name != "." && (base == ".git" || base == "node_modules" || base == "vendor" || strings.HasPrefix(base, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".py") && len(files) < 256 {
			files = append(files, name)
		}
		return nil
	})
	sort.SliceStable(files, func(i, j int) bool {
		rank := func(name string) int {
			switch filepath.Base(name) {
			case "main.py":
				return 0
			case "app.py":
				return 1
			case "server.py":
				return 2
			default:
				return 3
			}
		}
		ri, rj := rank(files[i]), rank(files[j])
		if ri != rj {
			return ri < rj
		}
		return files[i] < files[j]
	})
	return files
}

func pythonModuleName(name string) string {
	name = filepath.ToSlash(strings.TrimSuffix(name, ".py"))
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimSuffix(name, "/__init__")
	return strings.ReplaceAll(name, "/", ".")
}

func loopbackWarnings(fsys fs.FS) []Warning {
	var warnings []Warning
	var files []string
	_ = fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || len(files) >= 256 {
			return nil
		}
		base := filepath.Base(path)
		if base == "node_modules" || base == "vendor" || strings.HasPrefix(base, ".") {
			return nil
		}
		if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".py") || strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	for _, path := range files {
		body := readFile(fsys, path)
		if strings.Contains(body, "127.0.0.1") || strings.Contains(body, "localhost") {
			warnings = append(warnings, Warning{Code: "loopback_bind_possible", Message: "The source references localhost or 127.0.0.1; bind to 0.0.0.0 for public API traffic.", Sources: []string{path}})
		}
	}
	return warnings
}

func hasDependency(deps map[string]struct{}, name string) bool {
	_, ok := deps[name]
	return ok
}

func defaultPort(framework markers.Framework) int {
	switch framework {
	case markers.FrameworkNode:
		return 3000
	case markers.FrameworkPython:
		return 8000
	default:
		return 8080
	}
}

var portPattern = regexp.MustCompile(`(?im)^\s*EXPOSE\s+([0-9]{1,5})`)

func firstPort(body string) int {
	m := portPattern.FindStringSubmatch(body)
	if len(m) != 2 {
		return 0
	}
	var port int
	if _, err := fmt.Sscan(m[1], &port); err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

func fileExists(fsys fs.FS, path string) bool {
	_, err := fs.Stat(fsys, path)
	return err == nil
}

func readFile(fsys fs.FS, path string) string {
	f, err := fsys.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSourceFileBytes+1))
	if err != nil || len(data) > maxSourceFileBytes {
		return ""
	}
	return string(data)
}
