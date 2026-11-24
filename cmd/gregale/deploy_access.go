package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type manifestAccessClient interface {
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
	UpdateApp(ctx context.Context, slug string, req api.UpdateAppRequest) (api.AppResponse, error)
}

// deployManifestAccess applies the manifest's access key on a production
// deploy (ADR-20261124090000 amendment 1): apid opens the URL for "public",
// needs a Gregale API key for "key", and returns an app whose source no
// longer declares access to the plan default. It sends nothing when the
// source declares no access and the app never declared any, so a server
// without the field, and apps that never use it, see no extra request.
func deployManifestAccess(ctx context.Context, client manifestAccessClient, slug, cwd string, requireAuthn, noRequireAuthn bool) error {
	declared, err := manifestAccess(cwd)
	if err != nil {
		return err
	}
	if (declared == api.ServiceAccessPublic && requireAuthn) || (declared == api.ServiceAccessKey && noRequireAuthn) {
		return fmt.Errorf("gregale.yaml declares access: %s, which the --require-authn/--no-require-authn flag contradicts; change one of them", declared)
	}
	current, err := client.GetApp(ctx, slug)
	if err != nil {
		if declared == "" {
			// Nothing declared now; without the app we cannot tell whether
			// it used to declare access, and the deploy itself goes on.
			return nil
		}
		return err
	}
	if declared == "" && current.DeclaredAccess == "" {
		return nil
	}
	updated, err := client.UpdateApp(ctx, slug, api.UpdateAppRequest{DeclaredAccess: &declared})
	var apiErr *api.APIError
	if declared != "" && errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusBadRequest && strings.Contains(apiErr.Problem.Detail, "declared_access") {
		_, _ = fmt.Fprintf(osStderr, "Note: gregale.yaml's access key was not applied; this Gregale server does not support it yet.\n")
		return nil
	}
	if err != nil {
		return err
	}
	if note := accessChangeNote(slug, declared, current, updated); note != "" {
		PrintProgress(osStderr, "%s", note)
	}
	return nil
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

func accessChangeNote(slug, declared string, before, after api.AppResponse) string {
	switch {
	case before.RequireAuthn == after.RequireAuthn && before.PublicAuth.Mode == after.PublicAuth.Mode:
		return ""
	case declared == api.ServiceAccessPublic:
		return fmt.Sprintf("Access: %s is public (gregale.yaml access: public); anyone can call its URL without a Gregale API key.", slug)
	case declared == api.ServiceAccessKey:
		return fmt.Sprintf("Access: %s needs a Gregale API key (gregale.yaml access: key).", slug)
	default:
		return fmt.Sprintf("Access: gregale.yaml no longer declares access, so %s is back to the plan default and needs a Gregale API key again.", slug)
	}
}
