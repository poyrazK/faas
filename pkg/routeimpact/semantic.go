package routeimpact

import (
	"errors"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func symbolLocation(symbol functionSymbol) SymbolLocation {
	return SymbolLocation{Name: symbol.Name, File: symbol.File, Line: symbol.Line}
}

func symbolChanges(before, after sourceIndex) []SymbolChange {
	names := map[string]bool{}
	for name := range before.Symbols {
		names[name] = true
	}
	for name := range after.Symbols {
		names[name] = true
	}
	changes := []SymbolChange{}
	for name := range names {
		old, oldExists := before.Symbols[name]
		current, newExists := after.Symbols[name]
		change := SymbolChange{Name: name}
		if oldExists {
			location := symbolLocation(old)
			change.Before = &location
		}
		if newExists {
			location := symbolLocation(current)
			change.After = &location
		}
		switch {
		case !oldExists:
			change.Change = "added"
		case !newExists:
			change.Change = "removed"
		case old.Hash != current.Hash || old.File != current.File:
			change.Change = "modified"
		default:
			continue
		}
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	return changes
}

func semanticFileChanges(changes []FileChange, before, after sourceIndex, initialization bool) []FileChange {
	out := []FileChange{}
	for _, change := range changes {
		old, oldExists := before.Modules[change.File]
		current, newExists := after.Modules[change.File]
		if oldExists && newExists {
			if initialization && old.InitializationHash == current.InitializationHash {
				continue
			}
			if !initialization && old.Hash == current.Hash {
				continue
			}
		}
		out = append(out, change)
	}
	return out
}

func handlerChanged(before, after Route, changes []FileChange, oldIndex, newIndex sourceIndex) bool {
	if before.Source.File != after.Source.File || before.Handler != after.Handler || before.HandlerSymbol != after.HandlerSymbol {
		return true
	}
	old, oldExists := oldIndex.Symbols[before.HandlerSymbol]
	current, newExists := newIndex.Symbols[after.HandlerSymbol]
	if oldExists && newExists {
		return old.Hash != current.Hash
	}
	return handlerFileChanged(before, after, changes)
}

// Function references include direct calls and callback references. They are
// static potential paths, never runtime traces. Both revisions are traversed.
func preciseEvidence(route Route, index sourceIndex, changes, initialization []FileChange, symbols []SymbolChange, revision string) ([]Evidence, []Issue, error) {
	if index.Symbols == nil {
		evidence, err := linkedEvidence(route, index, changes, revision)
		return evidence, nil, err
	}
	starts := append([]string{route.HandlerSymbol}, route.DependencySymbols...)
	sort.Strings(starts)
	chains := map[string][]SymbolLocation{}
	kinds := map[string]string{}
	seen := map[string]bool{}
	queue := [][]SymbolLocation{}
	issues := []Issue{}
	for _, name := range starts {
		if seen[name] {
			continue
		}
		seen[name] = true
		if symbol, exists := index.Symbols[name]; exists {
			queue = append(queue, []SymbolLocation{symbolLocation(symbol)})
			kinds[name] = "function_reference"
		} else {
			issues = append(issues, Issue{Code: "unresolved_symbol_root", Symbol: name, File: route.Source.File,
				Line: route.Source.Line, Revision: revision, Message: "A handler or dependency does not resolve to an indexed function; using module fallback."})
		}
	}
	// Function bodies can also execute at module initialization. Keep these
	// roots separate from route decorators and dependency injection, which
	// register function objects without invoking their bodies.
	fallbackRoute := route
	fallbackRoute.DependencyFiles = append([]string{}, route.DependencyFiles...)
	modulesSeen := map[string]bool{}
	for depth := 0; ; depth++ {
		modules, err := linkedChains(fallbackRoute, index)
		if err != nil {
			return nil, nil, err
		}
		paths := make([]string, 0, len(modules))
		for path := range modules {
			if !modulesSeen[path] {
				paths = append(paths, path)
			}
		}
		if len(paths) == 0 {
			break
		}
		if depth >= api.RouteImpactMaxGraphDepth {
			return nil, nil, errors.New("module initialization graph exceeds the route impact depth limit")
		}
		sort.Strings(paths)
		for _, path := range paths {
			modulesSeen[path] = true
			module := index.Modules[path]
			for _, issue := range module.Issues {
				issue.Revision = revision
				issues = append(issues, issue)
			}
			for _, name := range module.InitializationSymbols {
				symbol := index.Symbols[name]
				fallbackRoute.DependencyFiles = append(fallbackRoute.DependencyFiles, symbol.File)
				if !seen[name] {
					seen[name] = true
					kinds[name] = "module_initialization"
					queue = append(queue, []SymbolLocation{{Name: "<module>", File: path, Line: 1}, symbolLocation(symbol)})
				}
			}
		}
	}
	for len(queue) > 0 {
		chain := queue[0]
		queue = queue[1:]
		name := chain[len(chain)-1].Name
		chains[name] = chain
		symbol := index.Symbols[name]
		for _, issue := range symbol.Issues {
			issue.Revision = revision
			issues = append(issues, issue)
		}
		for _, reference := range symbol.References {
			if seen[reference] {
				continue
			}
			if len(chain) >= api.RouteImpactMaxGraphDepth {
				return nil, nil, errors.New("local function graph exceeds the route impact depth limit")
			}
			seen[reference] = true
			kinds[reference] = kinds[name]
			child := index.Symbols[reference]
			queue = append(queue, append(append([]SymbolLocation{}, chain...), symbolLocation(child)))
		}
	}
	evidence := []Evidence{}
	for _, change := range symbols {
		if chain, linked := chains[change.Name]; linked {
			location := chain[len(chain)-1]
			evidence = append(evidence, Evidence{Kind: kinds[change.Name], Symbol: change.Name, File: location.File,
				Line: location.Line, Change: change.Change, Revision: revision, Via: []string{}, ViaSymbols: chain})
		}
	}
	moduleChanges := initialization
	if len(issues) > 0 {
		moduleChanges = changes
		for _, issue := range issues {
			fallbackRoute.DependencyFiles = append(fallbackRoute.DependencyFiles, issue.File)
		}
	}
	moduleEvidence, err := linkedEvidence(fallbackRoute, index, moduleChanges, revision)
	if err != nil {
		return nil, nil, err
	}
	for i := range moduleEvidence {
		moduleEvidence[i].Kind = "module_initialization"
		if len(issues) > 0 {
			moduleEvidence[i].Kind = "module_fallback"
		}
	}
	// Class middleware, models, and other registration references have no
	// function root in this first adapter. Retain their local import coverage.
	if len(route.FallbackFiles) > 0 && len(issues) == 0 {
		fallback, err := linkedEvidence(Route{DependencyFiles: route.FallbackFiles}, index, changes, revision)
		if err != nil {
			return nil, nil, err
		}
		covered := map[string]bool{}
		for _, row := range moduleEvidence {
			covered[row.File] = true
		}
		for _, row := range fallback {
			if !covered[row.File] {
				row.Kind = "module_fallback"
				moduleEvidence = append(moduleEvidence, row)
			}
		}
	}
	return append(evidence, moduleEvidence...), issues, nil
}
