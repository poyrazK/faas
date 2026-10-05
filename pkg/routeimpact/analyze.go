package routeimpact

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

//go:embed fastapi.py
var fastAPIAnalyzer string

//go:embed symbols.py
var symbolAnalyzer string

const fastAPIScopeDescription = "Static FastAPI HTTP registrations, module-level function references, and local module initialization within the selected source root. Function chains include calls and callback references, not runtime traces. Unresolved calls retain module fallback. No linked changes does not prove a route is unaffected; installed packages, configuration, data, and dynamic code are not verified."

const scopeDescription = fastAPIScopeDescription

const goNetHTTPScopeDescription = "Static Go net/http ServeMux, Chi, and Gin route registrations, literal Chi mounts and Gin route groups, resolvable local middleware functions, and local function references within the selected source root. Function chains are potential call paths, not runtime traces. Dynamic patterns, handlers, unresolved mounts, and unresolved middleware remain explicit uncertainty. No linked changes does not prove a route is unaffected; configuration, data, and dynamic code are not verified."
const nodeHTTPScopeDescription = "Static Express and Hono route registrations, literal local router mounts, resolvable local handlers and middleware, and local function references within the selected source root. JavaScript and TypeScript are parsed with a bundled TypeScript parser; application modules and dependencies are never loaded or executed. Dynamic paths, conditional registrations, unresolved handlers, and unsupported router factories remain explicit uncertainty. No linked changes does not prove a route is unaffected; configuration, data, and dynamic code are not verified."

// Analyze compares a resolved baseline commit with a commit or working tree.
// It does not contact Gregale, install dependencies, or execute customer code.
func Analyze(ctx context.Context, options Options) (Report, error) {
	if options.Path == "" {
		options.Path = "."
	}
	if options.Base == "" {
		return Report{}, errors.New("--base is required")
	}
	framework := strings.TrimSpace(options.Framework)
	if framework == "" {
		framework = "auto"
	}
	if framework != "auto" && framework != "fastapi" && framework != "go-nethttp" && framework != "node-http" {
		return Report{}, errors.New("--framework must be auto, fastapi, go-nethttp, or node-http")
	}
	if options.Entrypoint != "" && !validEntrypoint(options.Entrypoint) {
		return Report{}, errors.New("--entrypoint must be a Python module:variable")
	}
	ctx, cancel := context.WithTimeout(ctx, api.RouteImpactTimeout)
	defer cancel()
	repo, err := openRepository(ctx, options.Path)
	if err != nil {
		return Report{}, err
	}
	baseID, err := repo.commit(ctx, options.Base)
	if err != nil {
		return Report{}, fmt.Errorf("resolve baseline: %w", err)
	}
	base, err := repo.tree(ctx, baseID)
	if err != nil {
		return Report{}, fmt.Errorf("read baseline: %w", err)
	}
	var candidate sourceSnapshot
	if options.Head == "" {
		candidate, err = repo.worktree(ctx)
	} else {
		var headID string
		headID, err = repo.commit(ctx, options.Head)
		if err == nil {
			candidate, err = repo.tree(ctx, headID)
		}
	}
	if err != nil {
		return Report{}, fmt.Errorf("read candidate: %w", err)
	}
	if framework == "auto" {
		framework = detectFramework(base, candidate)
	}
	if framework != "fastapi" && options.Entrypoint != "" {
		return Report{}, errors.New("--entrypoint is only supported with --framework fastapi")
	}
	fingerprintForFramework(&base, framework)
	fingerprintForFramework(&candidate, framework)
	before, err := indexSource(ctx, base, framework, options.Entrypoint)
	if err != nil {
		return Report{}, fmt.Errorf("index baseline: %w", err)
	}
	after, err := indexSource(ctx, candidate, framework, options.Entrypoint)
	if err != nil {
		return Report{}, fmt.Errorf("index candidate: %w", err)
	}
	report, err := compare(base, candidate, before, after, repo.scope, options.App, framework)
	if err != nil {
		return Report{}, err
	}
	// Capture only a canonical, credential-free identity. Local Git metadata
	// is a declaration, not proof that a deployment contains these bytes.
	if origin, originErr := runGit(ctx, repo.root, nil, "config", "--get", "remote.origin.url"); originErr == nil {
		report.Repository, _ = RepositoryReference(strings.TrimSpace(string(origin)))
	}
	return report, nil
}

func detectFramework(base, candidate sourceSnapshot) string {
	// FastAPI remains the default when a repository contains both languages,
	// which preserves existing CLI behavior for Python services with Go tooling.
	if snapshotHasSource(base, ".py") || snapshotHasSource(candidate, ".py") {
		return "fastapi"
	}
	if snapshotHasSource(base, ".go") || snapshotHasSource(candidate, ".go") {
		return "go-nethttp"
	}
	for path := range base.files {
		if nodeSourcePath(path) {
			return "node-http"
		}
	}
	for path := range candidate.files {
		if nodeSourcePath(path) {
			return "node-http"
		}
	}
	return "fastapi"
}

func snapshotHasSource(snapshot sourceSnapshot, extension string) bool {
	for path := range snapshot.files {
		if strings.HasSuffix(path, extension) {
			return true
		}
	}
	return false
}

func validEntrypoint(value string) bool {
	module, variable, found := strings.Cut(value, ":")
	if !found {
		return false
	}
	for _, identifier := range append(strings.Split(module, "."), variable) {
		if identifier == "" {
			return false
		}
		for i, char := range identifier {
			if char != '_' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (i == 0 || char < '0' || char > '9') {
				return false
			}
		}
	}
	return true
}

func indexSource(ctx context.Context, snapshot sourceSnapshot, arguments ...string) (sourceIndex, error) {
	framework, entrypoint := "fastapi", ""
	if len(arguments) == 1 {
		entrypoint = arguments[0]
	} else if len(arguments) > 1 {
		framework, entrypoint = arguments[0], arguments[1]
	}
	if framework == "go-nethttp" {
		return indexGoNetHTTP(snapshot)
	}
	if framework == "node-http" {
		return indexNodeHTTP(ctx, snapshot)
	}
	type source struct {
		File string `json:"file"`
		Body []byte `json:"body"` // JSON encodes bytes as base64, preserving source encoding.
	}
	sources := make([]source, 0, snapshot.meta.PythonFiles)
	for path, file := range snapshot.files {
		if strings.HasSuffix(path, ".py") && file.body != nil {
			sources = append(sources, source{File: path, Body: file.body})
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].File < sources[j].File })
	payload, err := json.Marshal(struct {
		Sources    []source       `json:"sources"`
		Entrypoint string         `json:"entrypoint"`
		Limits     map[string]int `json:"limits"`
	}{sources, entrypoint, map[string]int{
		"routes": api.RouteImpactMaxRoutes, "issues": api.RouteImpactMaxIssues,
		"edges": api.RouteImpactMaxImportEdges, "depth": api.RouteImpactMaxGraphDepth,
		"symbols": api.RouteImpactMaxSymbols, "symbol_edges": api.RouteImpactMaxSymbolEdges,
		"symbol_issues": api.RouteImpactMaxSymbolIssues,
	}})
	if err != nil {
		return sourceIndex{}, fmt.Errorf("encode static analysis input: %w", err)
	}
	parserCtx, cancel := context.WithTimeout(ctx, api.RouteImpactParserTimeout)
	defer cancel()
	// Isolated mode excludes the working directory/PYTHONPATH; -S disables
	// site initialization. Only the embedded AST script and stdlib execute.
	cmd := exec.CommandContext(parserCtx, "python3", "-I", "-S", "-c", symbolAnalyzer+"\n"+fastAPIAnalyzer)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stderr = io.Discard
	out := &limitedBuffer{max: api.RouteImpactASTOutputMaxBytes}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return sourceIndex{}, fmt.Errorf("run isolated Python AST parser (requires python3): %w", err)
	}
	var index sourceIndex
	if err := json.Unmarshal(out.Bytes(), &index); err != nil {
		return sourceIndex{}, fmt.Errorf("decode static route index: %w", err)
	}
	index.Issues = append(index.Issues, snapshot.issues...)
	if len(index.Issues) > api.RouteImpactMaxIssues {
		return sourceIndex{}, errors.New("static route index exceeds the issue limit")
	}
	return index, nil
}

func compare(base, candidate sourceSnapshot, before, after sourceIndex, root, app string, frameworks ...string) (Report, error) {
	framework := "fastapi"
	if len(frameworks) > 0 && frameworks[0] != "" {
		framework = frameworks[0]
	}
	base.meta.Entrypoint, candidate.meta.Entrypoint = before.Entrypoint, after.Entrypoint
	scopeDescription := fastAPIScopeDescription
	sourceExtension := ".py"
	sourcePath := func(path string) bool { return strings.HasSuffix(path, sourceExtension) }
	if framework == "go-nethttp" {
		scopeDescription = goNetHTTPScopeDescription
		sourceExtension = ".go"
		sourcePath = func(path string) bool { return strings.HasSuffix(path, ".go") }
	}
	if framework == "node-http" {
		scopeDescription = nodeHTTPScopeDescription
		sourcePath = nodeSourcePath
	}
	version := 2
	if framework == "go-nethttp" {
		version = 3
	} else if framework == "node-http" {
		version = 4
	}
	report := Report{Version: version, Framework: framework, App: app, SourceRoot: root, Status: "complete",
		Scope: scopeDescription, Base: base.meta, Candidate: candidate.meta, ChangedFiles: fileChanges(base, candidate),
		ChangedSymbols: symbolChanges(before, after), Routes: []Result{}, Issues: []Issue{}}
	if report.SourceRoot == "" {
		report.SourceRoot = "."
	}
	for _, index := range []struct {
		value    sourceIndex
		revision string
	}{{before, "base"}, {after, "candidate"}} {
		for _, issue := range index.value.Issues {
			issue.Revision = index.revision
			report.Issues = append(report.Issues, issue)
		}
	}
	for _, change := range report.ChangedFiles {
		if !sourcePath(change.File) {
			code, language := "non_python_change", "Python"
			if framework == "go-nethttp" {
				code, language = "non_go_change", "Go"
			} else if framework == "node-http" {
				code, language = "non_node_change", "JavaScript or TypeScript"
			}
			report.Issues = append(report.Issues, Issue{Code: code, File: change.File,
				Message: "Changes outside " + language + " source files are not mapped to routes."})
		}
	}
	if len(report.Issues) > 0 {
		report.Status = "incomplete"
	}
	assemblyIncomplete := report.Status == "incomplete"
	semanticChanges := semanticFileChanges(report.ChangedFiles, before, after, false)
	initializationChanges := semanticFileChanges(report.ChangedFiles, before, after, true)
	issueSet := map[Issue]bool{}
	for _, issue := range report.Issues {
		issueSet[issue] = true
	}
	oldRoutes, newRoutes := routeMap(before.Routes), routeMap(after.Routes)
	keys := map[string]bool{}
	for key := range oldRoutes {
		keys[key] = true
	}
	for key := range newRoutes {
		keys[key] = true
	}
	var sortedKeys []string
	for key := range keys {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)
	evidenceCount, evidenceBytes, uncertaintyCount := 0, 0, 0
	for _, key := range sortedKeys {
		oldRoute, oldExists := oldRoutes[key]
		newRoute, newExists := newRoutes[key]
		result := Result{Evidence: []Evidence{}, Uncertainties: []Issue{}, Precision: "function"}
		if oldExists {
			result.Before = &oldRoute
			result.Method, result.Path = oldRoute.Method, oldRoute.Path
			evidence, issues, err := preciseEvidence(oldRoute, before, semanticChanges, initializationChanges, report.ChangedSymbols, "base")
			if err != nil {
				return Report{}, err
			}
			result.Evidence = append(result.Evidence, evidence...)
			result.Uncertainties = append(result.Uncertainties, issues...)
		}
		if newExists {
			result.After = &newRoute
			result.Method, result.Path = newRoute.Method, newRoute.Path
			evidence, issues, err := preciseEvidence(newRoute, after, semanticChanges, initializationChanges, report.ChangedSymbols, "candidate")
			if err != nil {
				return Report{}, err
			}
			result.Evidence = append(result.Evidence, evidence...)
			result.Uncertainties = append(result.Uncertainties, issues...)
		}
		if (oldExists && len(oldRoute.FallbackFiles) > 0) || (newExists && len(newRoute.FallbackFiles) > 0) {
			result.Precision = "mixed"
		}
		if len(result.Uncertainties) > 0 || before.Symbols == nil || after.Symbols == nil {
			result.Precision = "module_fallback"
		}
		for _, issue := range result.Uncertainties {
			uncertaintyCount++
			evidenceBytes += len(issue.File) + len(issue.Symbol) + len(issue.Message)
			if !issueSet[issue] {
				issueSet[issue] = true
				report.Issues = append(report.Issues, issue)
			}
		}
		for _, evidence := range result.Evidence {
			evidenceCount++
			evidenceBytes += len(evidence.File) + len(evidence.Symbol)
			for _, location := range evidence.ViaSymbols {
				evidenceBytes += len(location.File) + len(location.Name)
			}
			if evidence.Kind == "module_initialization" && result.Precision == "function" {
				result.Precision = "mixed"
			}
			for _, file := range evidence.Via {
				evidenceBytes += len(file)
			}
		}
		if evidenceCount > api.RouteImpactMaxEvidence || uncertaintyCount > api.RouteImpactMaxEvidence || evidenceBytes > api.RouteImpactEvidenceMaxBytes {
			return Report{}, errors.New("route impact evidence exceeds its report bound; narrow --path")
		}
		switch {
		case !oldExists && len(before.Issues) == 0 && len(after.Issues) == 0:
			result.Change = "added"
			report.Summary.Added++
		case !newExists && len(before.Issues) == 0 && len(after.Issues) == 0:
			result.Change = "removed"
			report.Summary.Removed++
		case !oldExists || !newExists:
			result.Change = "unknown"
			report.Summary.Unknown++
		case handlerChanged(oldRoute, newRoute, report.ChangedFiles, before, after):
			result.Change = "source_changed"
			report.Summary.SourceChanged++
		case len(result.Evidence) > 0:
			result.Change = "potentially_affected"
			report.Summary.PotentiallyAffected++
		case assemblyIncomplete || len(result.Uncertainties) > 0:
			result.Change = "unknown"
			report.Summary.Unknown++
		default:
			result.Change = "no_linked_changes"
			report.Summary.NoLinkedChanges++
		}
		report.Routes = append(report.Routes, result)
	}
	if len(report.Issues) > 0 {
		report.Status = "incomplete"
	}
	sort.Slice(report.Issues, func(i, j int) bool {
		a, b := report.Issues[i], report.Issues[j]
		return fmt.Sprintf("%s:%s:%09d:%s:%s", a.Revision, a.File, a.Line, a.Symbol, a.Code) < fmt.Sprintf("%s:%s:%09d:%s:%s", b.Revision, b.File, b.Line, b.Symbol, b.Code)
	})
	return report, nil
}

func routeMap(routes []Route) map[string]Route {
	out := make(map[string]Route, len(routes))
	for _, route := range routes {
		out[route.Path+"\x00"+route.Method] = route
	}
	return out
}

func fileChanges(base, candidate sourceSnapshot) []FileChange {
	keys := map[string]bool{}
	for path := range base.files {
		keys[path] = true
	}
	for path := range candidate.files {
		keys[path] = true
	}
	changes := []FileChange{}
	for path := range keys {
		before, oldExists := base.files[path]
		after, newExists := candidate.files[path]
		switch {
		case !oldExists:
			changes = append(changes, FileChange{File: path, Change: "added"})
		case !newExists:
			changes = append(changes, FileChange{File: path, Change: "removed"})
		case before.hash != after.hash || before.mode != after.mode:
			changes = append(changes, FileChange{File: path, Change: "modified"})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].File < changes[j].File })
	return changes
}

func handlerFileChanged(before, after Route, changes []FileChange) bool {
	for _, change := range changes {
		if change.File == before.Source.File || change.File == after.Source.File {
			return true
		}
	}
	return false
}

func linkedChains(route Route, index sourceIndex) (map[string][]string, error) {
	// A separate shortest chain is preserved for each registration context.
	// Global/route dependency injection and middleware are conservatively
	// covered by the imports of the app, including routers and handler module.
	starts := append([]string{route.Source.File}, route.DependencyFiles...)
	sort.Strings(starts)
	seen := map[string]bool{}
	chains := map[string][]string{}
	queue := [][]string{}
	for _, file := range starts {
		if file != "" && !seen[file] {
			seen[file] = true
			queue = append(queue, []string{file})
		}
	}
	for len(queue) > 0 {
		chain := queue[0]
		queue = queue[1:]
		file := chain[len(chain)-1]
		chains[file] = chain
		for _, linked := range index.Dependencies[file] {
			if !seen[linked] {
				if len(chain) >= api.RouteImpactMaxGraphDepth {
					return nil, errors.New("local import graph exceeds the route impact depth limit")
				}
				seen[linked] = true
				next := append(append([]string{}, chain...), linked)
				queue = append(queue, next)
			}
		}
	}
	// Router/app assembly files are direct evidence. Traversing every import
	// of an app's registration file would link all sibling routers to each
	// other. Shared middleware and dependencies have separate explicit roots.
	for _, file := range route.ContextFiles {
		if _, exists := chains[file]; !exists {
			chains[file] = []string{file}
		}
	}
	return chains, nil
}

func linkedEvidence(route Route, index sourceIndex, changes []FileChange, revision string) ([]Evidence, error) {
	chains, err := linkedChains(route, index)
	if err != nil {
		return nil, err
	}
	evidence := []Evidence{}
	for _, change := range changes {
		if chain, linked := chains[change.File]; linked {
			evidence = append(evidence, Evidence{Kind: "module_fallback", File: change.File, Change: change.Change, Revision: revision, Via: chain})
		}
	}
	return evidence, nil
}
