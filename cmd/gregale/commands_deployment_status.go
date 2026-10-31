package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDeploymentStatus prints a deployment's conditions (ADR-20261030151810).
func cmdDeploymentStatus(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := newFlagSet("deployment status", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug; needed for vN or latest outside a linked project")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 || !validDeploymentReadRef(pos[0]) {
		PrintUsage(os.Stderr, "usage: gregale release status <id|vN|latest> [--app SLUG]", "deployment")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := resolveDeploymentReadArg(ctx, client, *appFlag, pos[0])
	if err != nil {
		return printErr("Could not resolve deployment", err)
	}
	status, err := client.GetDeploymentStatus(ctx, id)
	if err != nil {
		return printErr("Could not read conditions", deploymentStatusError(id, err))
	}
	return renderResourceStatus(status)
}

// waitDeploymentCondition is `release wait --for`: the deployment's
// conditions through the same wait and exit codes as `app wait`.
func waitDeploymentCondition(app, ref, forFlag string, timeout time.Duration) int {
	want, err := parseConditionFor(api.ResourceKindDeployment, forFlag)
	if err != nil {
		return printErr("Invalid wait options", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	id, err := resolveDeploymentReadArg(ctx, client, app, ref)
	cancel()
	if err != nil {
		return printErr("Could not resolve deployment", err)
	}
	read := func(ctx context.Context) (api.ResourceStatus, error) {
		status, err := client.GetDeploymentStatus(ctx, id)
		return status, deploymentStatusError(id, err)
	}
	return waitForCondition(read, want, timeout, 2*time.Second)
}

// deploymentStatusError explains a 404 as appStatusError does.
func deploymentStatusError(id string, err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
		return &exitErr{code: exitCodeForStatus(http.StatusNotFound), msg: fmt.Sprintf("no conditions for deployment %s: it does not exist, or conditions are not available to this account yet; `gregale release wait %s` waits for it instead", id, id)}
	}
	return err
}
