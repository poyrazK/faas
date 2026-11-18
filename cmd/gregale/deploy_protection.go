package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type manifestProtectionClient interface {
	GetAppProtection(ctx context.Context, slug string) (api.AppProtectionResponse, error)
	SetAppProtection(ctx context.Context, slug, state string) (api.AppProtectionResponse, error)
}

// deployManifestProtection turns the app's production protection on or off
// as the manifest's protection key says (ADR-20261116120000 §5), as apid does
// for a source deploy. It changes nothing when the key is absent or the app
// is already in that state. Protection is a preview: where the route is not
// available (a 404) the deploy goes on and says so.
func deployManifestProtection(ctx context.Context, client manifestProtectionClient, slug, cwd string) error {
	if cwd == "" {
		return nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil || !ok || m == nil || m.Protection == "" {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	current, err := client.GetAppProtection(ctx, slug)
	if err == nil && current.State == m.Protection {
		return nil
	}
	if err == nil {
		_, err = client.SetAppProtection(ctx, slug, m.Protection)
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
		_, _ = fmt.Fprintf(osStderr, "Note: the manifest's protection key was not applied; production protection (a preview) is not available to this account yet.\n")
		return nil
	}
	return err
}
