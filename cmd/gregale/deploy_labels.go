package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type manifestLabelClient interface {
	UpdateAppLabels(ctx context.Context, slug string, change api.LabelChange) (api.LabelsResponse, error)
}

// deployManifestLabels sets the labels the manifest declares and removes none
// (ADR-20261031100025 §7), as apid does for a source deploy. Labels are a
// preview: where the label route is not available (a 404) the deploy goes on
// and says the labels were not applied.
func deployManifestLabels(ctx context.Context, client manifestLabelClient, slug, cwd string) error {
	if cwd == "" {
		return nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil || !ok || m == nil || len(m.Labels) == 0 {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	_, err = client.UpdateAppLabels(ctx, slug, api.LabelChange{Set: m.Labels})
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
		_, _ = fmt.Fprintf(osStderr, "Note: the manifest's labels were not applied; labels (a preview) are not available to this account yet.\n")
		return nil
	}
	return err
}
