package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// rootAppLeftOutNote is the note shown when a directory with an app at its
// root also holds functions/<name>/ handlers that declare config: discovery
// makes a plain `gregale deploy` deploy only the functions, so the root app
// is left out (prod hunt r55: `gregale init` then `gregale add function`
// deployed the queue function and skipped the app without a word).
const rootAppLeftOutNote = "The app at the root of this directory is not part of the function set: `gregale deploy` deploys only functions/. Deploy the root app with `gregale deploy --app`"

// rootAppLeftOut reports whether dir, with no gregale.yaml, has an app at its
// root: a Dockerfile or go.mod, a package.json that starts a server, or a
// Python project with a .py file at the root.
func rootAppLeftOut(dir string) bool {
	if _, found, err := gregalemanifest.Load(dir); err != nil || found {
		return false
	}
	exists := func(name string) bool {
		info, err := os.Stat(filepath.Join(dir, name))
		return err == nil && !info.IsDir()
	}
	switch {
	case exists("Dockerfile"), exists("go.mod"):
		return true
	case exists("package.json"):
		return !packageJSONStartsNothing(dir)
	case exists("requirements.txt"), exists("pyproject.toml"):
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
				return true
			}
		}
	}
	return false
}
