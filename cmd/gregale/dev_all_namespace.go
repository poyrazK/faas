package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// devGroupWorkspaceEnv carries the `gregale dev --all` group's workspace to
// each child loop, so every app's developer environment joins one namespace
// (ADR-20261025121530). It is internal to the supervisor and its children.
const devGroupWorkspaceEnv = "GREGALE_DEV_GROUP_WORKSPACE"

// devGroupWorkspace returns the supervisor's group workspace when this
// process is a `dev --all` child, or "".
func devGroupWorkspace() string {
	workspace := os.Getenv(devGroupWorkspaceEnv)
	if len(workspace) != 32 {
		return ""
	}
	for _, ch := range workspace {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return ""
		}
	}
	return workspace
}

// devGroupClient is the API surface joining a `dev --all` group needs.
type devGroupClient interface {
	RegisterScenarioTest(context.Context, string, api.RegisterScenarioTestRequest) error
}

// joinDevGroup registers a `dev --all` child's developer environment in its
// group's namespace once the environment exists. A call from one member to
// another's name (`http://api.internal:10081`, `api.svc.gregale`) then
// reaches that member's developer environment, never production. A name the
// namespace cannot hold is reported, and that app runs outside the group.
func joinDevGroup(ctx context.Context, client devGroupClient, workspace, project, slug string, warn io.Writer) error {
	if !api.ValidAppSlug(project) {
		PrintWarn(warn, "%q runs outside the group: a name needs 3 to 40 lowercase letters, digits or hyphens to be called from the other apps, and its own calls by name reach deployed apps", project)
		return nil
	}
	err := client.RegisterScenarioTest(ctx, workspace, api.RegisterScenarioTestRequest{
		Members: []api.ScenarioTestWorkload{{Workload: project, AppSlug: slug}},
	})
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusConflict {
		return nil // registered by an earlier run of the same group
	}
	return err
}
