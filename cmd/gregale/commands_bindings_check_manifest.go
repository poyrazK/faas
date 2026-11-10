package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// manifestDeclaredBindings returns the PostgreSQL bindings the gregale.yaml in
// dir declares for slug (ADR-20261110213000). An entry naming the app always
// counts; an entry without `app` counts only when dir is linked to slug, so a
// check run from another project's directory never borrows its manifest. A
// directory without a manifest declares nothing.
func manifestDeclaredBindings(dir, slug string) ([]bindingcheck.DeclaredBinding, error) {
	manifest, present, err := gregalemanifest.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("read gregale.yaml: %w", err)
	}
	if !present || manifest == nil || len(manifest.Databases) == 0 {
		return nil, nil
	}
	linkedHere := false
	if linked, _, linkErr := targetProjectContext(dir); linkErr == nil {
		linkedHere = strings.EqualFold(linked.App, slug)
	} else if !errors.Is(linkErr, errProjectContextNotFound) {
		return nil, linkErr
	}
	var declared []bindingcheck.DeclaredBinding
	for _, dependency := range manifest.Databases {
		app := strings.TrimSpace(dependency.App)
		if app == "" && !linkedHere || app != "" && !strings.EqualFold(app, slug) {
			continue
		}
		declared = append(declared, bindingcheck.DeclaredBinding{Type: api.BindingTypePostgres, Name: strings.TrimSpace(dependency.Database),
			Binding: dependency.EffectiveEnvironmentKey(), Scope: strings.TrimSpace(dependency.Scope)})
	}
	return declared, nil
}

func cwdManifestDeclaredBindings(slug string) ([]bindingcheck.DeclaredBinding, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return manifestDeclaredBindings(cwd, slug)
}
