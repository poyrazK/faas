package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type manifestAccessClient interface {
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
}

// checkManifestAccessFlags refuses a production deploy whose
// --require-authn/--no-require-authn flag would be undone by the source's
// access key (ADR-20261124090000 amendment 1). apid applies gregale.yaml's
// `access` on the deploy, after the flag, so a contradiction would silently
// lose the flag. It runs before the app is created or changed. Removing the
// key from an app that declared it returns the app to the plan default,
// which --no-require-authn also contradicts.
func checkManifestAccessFlags(ctx context.Context, client manifestAccessClient, slug, cwd string, requireAuthn, noRequireAuthn bool) error {
	if !requireAuthn && !noRequireAuthn {
		return nil
	}
	declared, err := manifestAccess(cwd)
	if err != nil {
		return err
	}
	switch {
	case declared == api.ServiceAccessPublic && requireAuthn, declared == api.ServiceAccessKey && noRequireAuthn:
		return fmt.Errorf("gregale.yaml declares access: %s, which the --require-authn/--no-require-authn flag contradicts; every deploy applies the file, so change one of them", declared)
	case declared != "" || !noRequireAuthn:
		return nil
	}
	current, err := client.GetApp(ctx, slug)
	var apiErr *api.APIError
	switch {
	case errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound:
		return nil // a new app: the flag holds
	case err != nil:
		return fmt.Errorf("check %s's declared access: %w", slug, err)
	case current.DeclaredAccess == "":
		return nil // never declared access: the flag holds
	}
	return fmt.Errorf("%s used to declare access: %s in gregale.yaml, and a deploy without it returns the app to the plan default, which --no-require-authn contradicts; add `access: public` to gregale.yaml instead", slug, current.DeclaredAccess)
}

func manifestAccess(cwd string) (string, error) {
	if cwd == "" {
		return "", nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil || !ok || m == nil {
		return "", err
	}
	if err := m.Validate(); err != nil {
		return "", err
	}
	return m.Access, nil
}
