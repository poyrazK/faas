// Package functionsrepo resolves a function app's settings from a repository
// push of its functions project (ADR-20261108160000 §4). It reads the pushed
// archive with the same plan `gregale deploy` uses, so a push and a CLI deploy
// build the same function with the same settings.
package functionsrepo

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// maxSelectedBytes bounds the manifest and handler files held in memory
// while planning one push.
const maxSelectedBytes = 64 << 20

// Function is one function app's part of a pushed functions project.
type Function struct {
	Plan gregalemanifest.FunctionPlan
	// Manifest is the function's settings as a single-app manifest
	// (gregalemanifest.Manifest.FunctionAppManifest).
	Manifest *gregalemanifest.Manifest
}

// Member reports whether app is a functions-project member: a function app
// in a project, named after its function.
func Member(app state.App) bool {
	return app.Type == state.AppTypeFunction && app.ProjectID != "" && app.WorkloadName != ""
}

// Resolve plans app's function from the pushed archive. ok is false when app
// is not a member, the source is not a functions project, or the project no
// longer declares app's function (its app is kept, ADR-20261108160000 §2).
func Resolve(archive string, app state.App) (Function, bool, error) {
	if !Member(app) {
		return Function{}, false, nil
	}
	manifestFS, err := tarball.SelectFS(archive, isRootManifest, gregalemanifest.InlineConfigMaxFileBytes, maxSelectedBytes)
	if err != nil {
		return Function{}, false, fmt.Errorf("read source: %w", err)
	}
	m, found, err := gregalemanifest.LoadFS(manifestFS, ".")
	if err != nil {
		return Function{}, false, err
	}
	if !found {
		m = nil
	}
	listed := listedHandlers(m)
	sourceFS, err := tarball.SelectFS(archive, func(name string) bool {
		return isRootManifest(name) || isConventionHandler(name) || listed[name]
	}, gregalemanifest.InlineConfigMaxFileBytes, maxSelectedBytes)
	if err != nil {
		return Function{}, false, fmt.Errorf("read source: %w", err)
	}
	return resolveFS(sourceFS, m, app)
}

func resolveFS(fsys fs.FS, m *gregalemanifest.Manifest, app state.App) (Function, bool, error) {
	m, _, err := gregalemanifest.ApplyInlineConfigFS(m, fsys)
	if err != nil {
		return Function{}, false, fmt.Errorf("functions config in code: %w", err)
	}
	if m == nil || len(m.Functions) == 0 {
		return Function{}, false, nil
	}
	plans, err := m.PlanFunctionsFS(fsys, "")
	if err != nil {
		return Function{}, false, err
	}
	for _, p := range plans {
		if p.Spec.Name != app.WorkloadName {
			continue
		}
		p.Slug = app.Slug
		return Function{Plan: p, Manifest: m.FunctionAppManifest(p)}, true, nil
	}
	return Function{}, false, nil
}

func isRootManifest(name string) bool {
	switch name {
	case "gregale.yaml", "gregale.yml", "gregale.toml":
		return true
	}
	return false
}

// isConventionHandler matches the handler.* files a function root or a
// functions/<name>/ directory may hold.
func isConventionHandler(name string) bool {
	switch strings.ToLower(path.Base(name)) {
	case "handler.js", "handler.mjs", "handler.cjs", "handler.ts", "handler.py", "handler.go":
		return true
	}
	return false
}

// listedHandlers is the set of handler files gregale.yaml names.
func listedHandlers(m *gregalemanifest.Manifest) map[string]bool {
	out := map[string]bool{}
	if m == nil {
		return out
	}
	for _, fn := range m.Functions {
		if fn.Handler != "" {
			out[path.Join(fn.FunctionRoot(), path.Clean(fn.Handler))] = true
		}
	}
	return out
}
