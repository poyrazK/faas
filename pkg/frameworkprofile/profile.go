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
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostingconfig"
	"github.com/onebox-faas/faas/pkg/markers"
	"github.com/onebox-faas/faas/pkg/statefuldenylist"
)

const (
	// Version is the profile inference contract version. A receipt can pin this
	// value so a later inference change is explainable and reproducible.
	Version            = "v1"
	maxSourceFileBytes = 1 << 20
)

// Profile persists source-derived runtime defaults and, for project images,
// the command contract captured at deployment admission.
type Profile struct {
	Version   string `json:"version"`
	Framework string `json:"framework"`
	// Family is the runtime family the source markers identified
	// (node|python|go|docker|unknown) — the vocabulary the deployment
	// pipeline records as BuildPlan.Framework. Framework refines it
	// (express, fastapi, gin, …). Not part of the profile wire shape.
	Family       string `json:"-"`
	FrameworkVer string `json:"framework_version,omitempty"`
	// DockerfilePath records the explicit Dockerfile selected for this exact
	// deployment. It is relative to the accepted source root and remains empty
	// for conventional Dockerfile and Railpack builds.
	DockerfilePath string `json:"dockerfile_path,omitempty"`
	// DockerfileBuild is the Compose build.args and build.target the app
	// carried when this deployment was enqueued (ADR-20261019142544). builderd
	// prefers it to the app's current options so a queued build keeps its
	// plan's options.
	DockerfileBuild *api.DockerfileBuildOptions `json:"dockerfile_build,omitempty"`
	// BuildProcess is a built service's Compose user, working_dir and stop
	// contract (ADR-20261022160000), frozen at enqueue; imaged applies it to
	// the built image. Its entrypoint, if any, is already in the command.
	BuildProcess *api.ComposeProcess `json:"build_process,omitempty"`
	// PackageManager records the lockfile/package-manager choice used by
	// static inference. It is advisory metadata; the builder remains the
	// authority for dependency installation.
	PackageManager string `json:"package_manager,omitempty"`
	StartCommand   string `json:"start_command,omitempty"`
	Port           int    `json:"port"`
	HealthPath     string `json:"health_path"`
	Inferred       bool   `json:"inferred"`
	// ConfigFile identifies the optional source manifest that supplied an
	// explicit hosting override. It is metadata only; the values above remain
	// the effective contract used by the builder and runtime.
	ConfigFile string `json:"config_file,omitempty"`
	// StartFromConfig reports that StartCommand is gregale.yaml's explicit
	// hosting.start. It replaces the built image's command, which a Railpack
	// build with no detected start inherits from the runtime base (python3 on
	// the Python runner) (Hunt 12 H12-29).
	StartFromConfig bool `json:"start_from_config,omitempty"`
	// StartPinned reports that StartCommand also replaces the built image's
	// command although no file named it: the command Railpack builds for this
	// Flask or FastAPI source cannot serve the application the profile
	// located (a `main:app` without a main module, `python main.py` that only
	// defines the app, or none at all), see pythonStartPinned (ADR-1500
	// amendment 4). Profiles written before it lack the mark and keep the
	// image's command.
	StartPinned bool `json:"start_pinned,omitempty"`
	// HealthFromConfig reports that HealthPath is gregale.yaml's explicit
	// hosting.health, which keeps HTTP readiness even on a raw-TCP routed
	// port (ADR-20261027090000).
	HealthFromConfig bool `json:"health_from_config,omitempty"`
	// HealthDefaulted reports that HealthPath is the platform's /healthz:
	// neither gregale.yaml nor the source named a health route. Startup
	// readiness is then the port accepting connections, as for a direct
	// image, because a plain Express or Flask app answers /healthz with 404
	// and failed every deploy (ADR-079 amendment 2). Profiles written before
	// it lack the mark and keep HTTP readiness.
	HealthDefaulted bool      `json:"health_defaulted,omitempty"`
	Warnings        []Warning `json:"warnings,omitempty"`
	// ImageCommand is the accepted Compose CMD contract. Its presence freezes
	// image semantics even when Cmd is nil (inherit) or the app changes class.
	ImageCommand     *ImageCommand     `json:"image_command,omitempty"`
	ImageHealthcheck *ImageHealthcheck `json:"image_healthcheck,omitempty"`
	// ImageHealthcheckRequired is imaged's effective immutable-manifest receipt.
	ImageHealthcheckRequired bool `json:"image_healthcheck_required,omitempty"`
}

// Warning is actionable profile feedback. Source paths are relative to the
// analyzed tree and never contain file contents or environment values.
type Warning struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Sources []string `json:"sources,omitempty"`
}

// ProfileForFunctionRuntime returns the framework pipeline implied by an
// explicitly selected function runtime. Function sources intentionally do not
// need a framework manifest, so this metadata is the authoritative profile
// when the archive contains only a handler file.
//
// The returned profile does not invent an application start command or health
// endpoint. Function deployments use the platform runner and its fixed
// manifest; callers should use the Framework field to select the build
// pipeline while leaving those app-level defaults untouched.
func ProfileForFunctionRuntime(runtime string) (Profile, bool) {
	var framework markers.Framework
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "node22", "node24":
		framework = markers.FrameworkNode
	case "python312", "python313":
		framework = markers.FrameworkPython
	case "go124", "go124-alpine":
		framework = markers.FrameworkGo
	case "binary":
		// ADR-987: a binary function is built from the customer's
		// Dockerfile, whose image names one static executable.
		framework = markers.FrameworkDocker
	default:
		return Profile{}, false
	}
	return Profile{
		Version:   Version,
		Framework: string(framework),
		Inferred:  false,
	}, true
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
	profile := Profile{Version: Version, Framework: string(framework), Family: string(framework), Port: defaultPort(framework), HealthPath: "/healthz"}

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
		profile.Family = string(markers.FrameworkUnknown)
		profile.Warnings = append(profile.Warnings, Warning{Code: "framework_not_detected", Message: "No supported API framework marker was found; supply an explicit Dockerfile or command."})
	}
	if framework != markers.FrameworkUnknown {
		profile.FrameworkVer = markers.VersionFromFS(fsys, framework)
	}
	if framework != markers.FrameworkDocker && framework != markers.FrameworkUnknown {
		// Railpack starts a source build with the Procfile's web process
		// when there is one, so that is the command the guest runs. The
		// inferred framework default was reported instead: hello-python
		// showed `gunicorn handler:app --bind 0.0.0.0:$PORT` while the
		// guest ran `gunicorn --workers 2 handler:app` (Hunt 12 H12-3).
		if web := procfileWebCommand(fsys); web != "" {
			profile.StartCommand, profile.StartPinned = web, false
		}
		// railpack.json's deploy.startCommand outranks the Procfile and
		// Railpack's own detection, so it is the command the guest runs.
		if start := railpackConfigStartCommand(fsys); start != "" {
			profile.StartCommand, profile.StartPinned = start, false
		}
	}
	// An unknown tree may be a home directory, not a project: do not scan it.
	healthInferred := false
	if framework != markers.FrameworkDocker && framework != markers.FrameworkUnknown {
		if health := inferHealthPath(fsys); health != "" {
			profile.HealthPath, healthInferred = health, true
		}
	}
	if cfg, present, err := hostingconfig.Load(fsys); err != nil {
		return Profile{}, err
	} else if present {
		applyHostingConfig(&profile, cfg)
		if fileExists(fsys, "gregale.yaml") {
			profile.ConfigFile = "gregale.yaml"
		} else {
			profile.ConfigFile = "gregale.yml"
		}
		if profile.HealthFromConfig && profile.HealthPath == "/" {
			profile.Warnings = append(profile.Warnings, rootHealthPathWarning(profile.ConfigFile))
		}
	}
	profile.Warnings = append(profile.Warnings, loopbackWarnings(fsys)...)
	profile.Inferred = profile.StartCommand != "" && profile.Framework != string(markers.FrameworkUnknown)
	profile.HealthDefaulted = !healthInferred && !profile.HealthFromConfig
	return profile, nil
}

// rootHealthPathWarning flags hosting.health: /. Gregale answers GET and
// HEAD on an app's health path at its edge without running the app (unless
// health_path_wakes is on), so with "/" every visitor to the home page got
// {"status":"ok","source":"edge"} and never reached the app. Coding agents
// set it when an app had no health route (agent eval baseline 2026-11-17).
func rootHealthPathWarning(configFile string) Warning {
	return Warning{
		Code:    "health_path_root",
		Message: `hosting.health is "/": Gregale answers the health path at its edge, so visitors to / get {"status":"ok"} instead of the app's page. Add a small route such as /healthz and name it instead.`,
		Sources: []string{configFile},
	}
}

func applyHostingConfig(profile *Profile, cfg hostingconfig.Config) {
	if start := strings.TrimSpace(cfg.Start); start != "" {
		profile.StartCommand = start
		profile.StartFromConfig, profile.StartPinned = true, false
	}
	if cfg.Port != 0 {
		profile.Port = cfg.Port
	}
	if health := strings.TrimSpace(cfg.Health); health != "" {
		profile.HealthPath = health
		profile.HealthFromConfig = true
	}
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
		content.WriteString(stripPythonDependencyComments(readFile(fsys, name)))
		content.WriteByte('\n')
	}
	all := strings.ToLower(content.String())
	profile.PackageManager = pythonPackageManager(fsys)
	switch {
	case pythonDependencyNamed(all, "fastapi"):
		profile.Framework = "fastapi"
		module, symbol, ok := findPythonApplication(fsys, `FastAPI\s*\(`)
		if ok {
			profile.StartCommand = fmt.Sprintf("uvicorn %s:%s --host 0.0.0.0 --port $PORT", module, symbol)
			profile.StartPinned = pythonStartPinned(fsys, all, "uvicorn")
		} else {
			profile.StartCommand = "uvicorn app:app --host 0.0.0.0 --port $PORT"
			profile.Warnings = append(profile.Warnings, pythonEntrypointWarning("FastAPI", "app:app"))
		}
	case pythonDependencyNamed(all, "django") || fileExists(fsys, "manage.py"):
		profile.Framework = "django"
		module, ok := findDjangoWSGI(fsys)
		if !ok {
			module = "app.wsgi"
			profile.Warnings = append(profile.Warnings, pythonEntrypointWarning("Django", "app.wsgi:application"))
		}
		profile.StartCommand = "gunicorn " + module + ":application --bind 0.0.0.0:$PORT"
	case pythonDependencyNamed(all, "flask"):
		profile.Framework = "flask"
		module, symbol, ok := findPythonApplication(fsys, `Flask\s*\(`)
		if ok {
			profile.StartCommand = fmt.Sprintf("gunicorn %s:%s --bind 0.0.0.0:$PORT", module, symbol)
			profile.StartPinned = pythonStartPinned(fsys, all, "gunicorn")
		} else {
			profile.StartCommand = "gunicorn app:app --bind 0.0.0.0:$PORT"
			profile.Warnings = append(profile.Warnings, pythonEntrypointWarning("Flask", "app:app"))
		}
	default:
		profile.Framework = "python"
		profile.Warnings = append(profile.Warnings, Warning{Code: "python_framework_not_detected", Message: "Python was detected but no FastAPI, Flask, or Django dependency was found; configure an explicit command.", Sources: []string{"requirements.txt", "pyproject.toml"}})
	}
}

// Python dependency manifests commonly contain prose comments. Framework
// inference must inspect declared packages, not words that happen to occur in
// those comments (for example "functions do not pull Flask"). A leading or
// whitespace-delimited '#' starts a comment in the supported manifest forms.
func stripPythonDependencyComments(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if cut := strings.IndexByte(line, '#'); cut >= 0 &&
			(cut == 0 || line[cut-1] == ' ' || line[cut-1] == '\t') {
			line = line[:cut]
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func pythonDependencyNamed(content, name string) bool {
	pattern := `(?i)(^|[^a-z0-9_.-])` + regexp.QuoteMeta(name) + `([^a-z0-9_.-]|$)`
	return regexp.MustCompile(pattern).MatchString(content)
}

// goLocalStart is the Go start command for `gregale dev`, which runs the
// source directly.
const goLocalStart = "go run ."

// DeployedStartCommand is the command a deployed build runs. It differs
// from StartCommand only for Go: StartCommand is the local `go run .` that
// `gregale dev` uses, while the Railpack build compiles the app and starts
// its ./out binary. The deploy summary and build plan showed `go run .`, as
// if every cold start compiled the source (prod hunt r47).
func DeployedStartCommand(p Profile) string {
	if p.StartCommand == goLocalStart && !p.StartFromConfig {
		return "./out"
	}
	return p.StartCommand
}

func inferGo(fsys fs.FS, profile *Profile) {
	profile.Framework = "go-net-http"
	profile.PackageManager = "go"
	files := collectSourceFiles(fsys, 128, func(name string) bool { return strings.HasSuffix(name, ".go") })
	for _, path := range files {
		body := readFile(fsys, path)
		if strings.Contains(body, "github.com/gin-gonic/gin") {
			profile.Framework = "gin"
			break
		}
	}
	profile.StartCommand = goLocalStart
}

func inferDocker(fsys fs.FS, profile *Profile) {
	profile.Framework = "oci"
	profile.PackageManager = "container"
	body := readFile(fsys, "Dockerfile")
	if port := firstPort(body); port > 0 {
		profile.Port = port
	}
	profile.Warnings = append(profile.Warnings, Warning{Code: "container_command_deferred", Message: "The container entrypoint and health contract will be read from the image; source inference is intentionally conservative.", Sources: []string{"Dockerfile"}})
	if warning, ok := nonHTTPServiceWarning(body); ok {
		profile.Warnings = append(profile.Warnings, warning)
	}
}

// nonHTTPServers maps official images of servers that speak their own TCP
// protocol to the port they serve on (ADR-20261112223000 §1).
var nonHTTPServers = map[string]int{
	"redis": 6379, "valkey": 6379, "keydb": 6379,
	"postgres": 5432, "mysql": 3306, "mariadb": 3306, "mongo": 27017,
	"memcached": 11211, "rabbitmq": 5672, "nats": 4222,
	"eclipse-mosquitto": 1883, "cassandra": 9042, //nolint:misspell // the official image name
}

// nonHTTPPorts are those servers' ports, plus Kafka's.
var nonHTTPPorts = func() map[int]bool {
	ports := map[int]bool{9092: true}
	for _, port := range nonHTTPServers {
		ports[port] = true
	}
	return ports
}()

var fromPattern = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--\S+\s+)*(\S+)`)

// nonHTTPServiceWarning reports a Dockerfile whose serving port is a raw TCP
// server's: its own EXPOSE, else the port of a known server image it builds
// FROM. The build readies once the port accepts connections, but the app's
// public route speaks HTTP, which the server does not, so it is reached over
// an internal TCP port instead. A final stage built FROM a datastore is
// reported whatever its port: apid refuses that build (stateless_only_violation).
func nonHTTPServiceWarning(dockerfile string) (Warning, bool) {
	if base, hosted, stateful := statefuldenylist.MatchDockerfile([]byte(dockerfile)); stateful {
		return datastoreWarning(imageName(base), hosted), true
	}
	server, port := "", firstPort(dockerfile)
	if froms := fromPattern.FindAllStringSubmatch(dockerfile, -1); len(froms) > 0 {
		ref := imageName(froms[len(froms)-1][1])
		if serverPort, known := nonHTTPServers[ref]; known {
			server = ref
			if port == 0 {
				port = serverPort
			}
		}
	}
	if !nonHTTPPorts[port] {
		return Warning{}, false
	}
	what := fmt.Sprintf("port %d is a raw TCP port", port)
	if server != "" {
		what = fmt.Sprintf("this builds %s, which serves its own TCP protocol on port %d", server, port)
	}
	return Warning{Code: "non_http_service", Sources: []string{"Dockerfile"}, Message: fmt.Sprintf(
		"%s. The app's public URL speaks HTTP, which this server does not answer. "+
			"Deploy this build or the published image (`gregale deploy --image <image>@sha256:<digest> --name <app>`); "+
			"either is ready once the port accepts connections. Then keep it private and let other apps reach it over TCP with "+
			"`gregale app <app> --visibility internal --ports <name>=tcp:%d:internal`.", what, port)}, true
}

// datastoreWarning names the hosted service for a datastore build. A datastore
// is refused built or as an image (stateless_only_violation): an app parks to
// a snapshot and keeps no data.
func datastoreWarning(server, hosted string) Warning {
	managed := hosted
	if server == "postgres" {
		managed = "use managed PostgreSQL: `gregale postgres create <name>`"
	}
	return Warning{Code: "non_http_service", Sources: []string{"Dockerfile"}, Message: fmt.Sprintf(
		"this builds %s, a datastore. Gregale does not run datastores, built or as an image: an app parks to a snapshot and keeps no data. "+
			"%s, and read its URL from the environment: `gregale env set --app <app> %s_URL=<url>`.",
		server, upperFirst(managed), strings.ToUpper(server))}
}

// imageName is an image reference's last path segment without tag or digest:
// docker.io/library/postgres:16@sha256:… → postgres.
func imageName(ref string) string {
	ref = strings.ToLower(ref)
	if at := strings.IndexByte(ref, '@'); at >= 0 {
		ref = ref[:at]
	}
	if slash := strings.LastIndexByte(ref, '/'); slash >= 0 {
		ref = ref[slash+1:]
	}
	if colon := strings.IndexByte(ref, ':'); colon >= 0 {
		ref = ref[:colon]
	}
	return ref
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

// healthPathRegex finds a conventional health route in source: a framework
// registration (app.get("/healthz"), @app.route, http.HandleFunc), a plain
// router's switch case (case "/healthz":) or a path comparison
// (url.pathname === "/healthz", self.path == "/healthz", r.URL.Path ==
// "/healthz"). A plain node:http server declared its /healthz only in a
// switch, so its 503 went unprobed under TCP readiness and an unhealthy
// release deployed (journeys 2026-11-16 TestHealthGate; ADR-079 amendment 2).
var healthPathRegex = regexp.MustCompile(`(?i)(?:(?:get|route|handle|handlefunc|head|live|post|put|patch|delete)\s*\(\s*|\bcase\s+|[=!]==?\s*)["'](/(?:healthz?|readyz?|livez?|ready|live))["']`)

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
	return collectSourceFiles(fsys, 512, isSourceCodeFile)
}

func isSourceCodeFile(name string) bool {
	return strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".py") || strings.HasSuffix(name, ".go")
}

// isTestSourceFile reports test code by the Go, Node and Python conventions.
func isTestSourceFile(name string) bool {
	name = filepath.ToSlash(name)
	base := filepath.Base(name)
	for _, dir := range strings.Split(filepath.Dir(name), "/") {
		if dir == "test" || dir == "tests" || dir == "__tests__" {
			return true
		}
	}
	return strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

// Source inference reads conventional project layouts, so the walk is bounded:
// it stops at the file limit, below sourceWalkMaxDepth directory levels, and
// after sourceWalkMaxDirs directories. An unbounded walk made `gregale doctor`
// run from a home directory read every folder under it and stall on
// macOS-protected folders in ~/Library.
const (
	sourceWalkMaxDepth = 8
	sourceWalkMaxDirs  = 4096
)

// collectSourceFiles returns up to limit files matching match, sorted, skipping
// dependency, VCS and hidden directories.
func collectSourceFiles(fsys fs.FS, limit int, match func(string) bool) []string {
	files := []string{}
	dirs := 0
	_ = fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() && name != "." {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if name == "." {
				return nil
			}
			base := path.Base(name)
			if base == "node_modules" || base == "vendor" || strings.HasPrefix(base, ".") || strings.Count(name, "/") >= sourceWalkMaxDepth {
				return fs.SkipDir
			}
			if dirs++; dirs > sourceWalkMaxDirs {
				return fs.SkipAll
			}
			return nil
		}
		if match(name) {
			files = append(files, name)
			if len(files) >= limit {
				return fs.SkipAll
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
	files := collectSourceFiles(fsys, 256, func(name string) bool { return strings.HasSuffix(name, ".py") })
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

// loopbackBindLine reports whether a line names a loopback address where an
// address is bound: beside listen, bind, host or serve. rest-api-postgres's
// check that a caller's remoteAddress is 127.0.0.1, and a client URL such as
// http://localhost:5432, warned on every deploy of our own starter (prod hunt
// r42).
func loopbackBindLine(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "127.0.0.1") && !strings.Contains(lower, "localhost") {
			continue
		}
		rest := strings.ReplaceAll(lower, "localhost", "")
		for _, word := range []string{"listen", "bind", "host", "serve"} {
			if strings.Contains(rest, word) {
				return true
			}
		}
	}
	return false
}

func loopbackWarnings(fsys fs.FS) []Warning {
	var warnings []Warning
	files := collectSourceFiles(fsys, 256, isSourceCodeFile)
	for _, path := range files {
		// Tests never serve traffic, and a source that also names 0.0.0.0
		// chooses its bind address (the MCP starters bind loopback only
		// off-platform); warning on either was noise on our own starters.
		if isTestSourceFile(path) {
			continue
		}
		body := readFile(fsys, path)
		if loopbackBindLine(body) && !strings.Contains(body, "0.0.0.0") {
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

// procfileWebCommand returns the Procfile's web process command, or "".
// Lines are Heroku-format `name: command`, split at the first colon.
func procfileWebCommand(fsys fs.FS) string {
	for _, line := range strings.Split(readFile(fsys, "Procfile"), "\n") {
		name, command, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.TrimSpace(name) == "web" {
			return strings.TrimSpace(command)
		}
	}
	return ""
}

// railpackConfigStartCommand returns railpack.json's deploy.startCommand, or
// "" when the file is absent, unreadable or names none.
func railpackConfigStartCommand(fsys fs.FS) string {
	var config struct {
		Deploy struct {
			StartCommand string `json:"startCommand"`
		} `json:"deploy"`
	}
	if json.Unmarshal([]byte(readFile(fsys, "railpack.json")), &config) != nil {
		return ""
	}
	return strings.TrimSpace(config.Deploy.StartCommand)
}

// railpackPythonMainFiles are the files whose presence gives Railpack's
// Python provider a start command (getMainPythonFile), in its order.
var railpackPythonMainFiles = []string{"main.py", "app.py", "start.py", "bot.py", "hello.py", "server.py"}

// fastapiServerExtra matches a FastAPI requirement whose extras install
// uvicorn without naming it: fastapi[standard], fastapi[all] and
// fastapi[standard-no-fastapi-cloud-cli].
var fastapiServerExtra = regexp.MustCompile(`(^|[^a-z0-9_.-])fastapi\s*\[[^\]]*\b(standard|all)\b`)

// pythonStartPinned reports whether a located Flask or FastAPI application's
// inferred start must replace the built image's command because the command
// Railpack 0.38's Python provider (GetStartCommand) builds cannot serve it:
//
//   - With none of its main files, Railpack detects no start and the image
//     keeps its base's CMD (python3, Hunt 12 H12-29).
//   - A Flask app whose manifests mention gunicorn, or a FastAPI app whose
//     manifests mention uvicorn, starts as `main:app` whichever module
//     defines the application. Without a root main module that fails; a main
//     module may re-export the application, so its presence keeps it.
//   - Otherwise Railpack runs `python <main file>`, which only defines the
//     application and exits unless the file starts a server itself. FastAPI's
//     own install, fastapi[standard], brings uvicorn without naming it.
//
// Railpack tests a dependency as a case-insensitive substring of a manifest,
// comments included; only root manifests are read here. The pinned command
// runs server, so it must be installed. manifests is the comment-stripped,
// lowercased dependency text.
func pythonStartPinned(fsys fs.FS, manifests, server string) bool {
	if !pythonDependencyNamed(manifests, server) && (server != "uvicorn" || !fastapiServerExtra.MatchString(manifests)) {
		return false
	}
	script := ""
	for _, name := range railpackPythonMainFiles {
		if fileExists(fsys, name) {
			script = name
			break
		}
	}
	if script == "" {
		return true
	}
	var raw strings.Builder
	for _, name := range []string{"requirements.txt", "pyproject.toml", "Pipfile"} {
		raw.WriteString(strings.ToLower(readFile(fsys, name)))
		raw.WriteByte('\n')
	}
	uses := func(dependency string) bool { return strings.Contains(raw.String(), dependency) }
	if uses("flask") && uses("gunicorn") || uses("fastapi") && uses("uvicorn") {
		return !fileExists(fsys, "main.py") && !fileExists(fsys, "main/__init__.py")
	}
	body := readFile(fsys, script)
	return !strings.Contains(body, "__main__") && !strings.Contains(body, ".run(")
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

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
