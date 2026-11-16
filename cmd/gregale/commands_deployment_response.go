package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Check the exact accepted release without the synchronous invocation plan gate.
// The response is ephemeral and says nothing about anonymous public access.
func cmdDeploymentCheckResponse(args []string) int {
	fs := newFlagSet("release verify", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	path := fs.String("path", "/", "app-relative GET path")
	project := fs.String("project-id", "", "project UUID for a named environment")
	environment := fs.String("environment-id", "", "environment UUID for a named environment")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidDeploymentResponseCheckID(fs.Arg(0)) || !api.ValidAppSlug(*app) {
		PrintUsage(osStderr, "usage: gregale release verify <deployment-uuid> --app SLUG [--path /] [--project-id UUID --environment-id UUID]", "deployment")
		return 1
	}
	request := api.DeploymentResponseCheckRequest{AppID: fs.Arg(0), Scope: api.DefaultEnvScope, Path: *path, ProjectID: *project, EnvironmentID: *environment}
	if err := request.Validate(); err != nil {
		return printErr("Invalid response check", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := deploymentResponseCheck(ctx, client, *app, fs.Arg(0), request)
	if err != nil {
		return printErr("Could not verify application response", err)
	}
	return printDeploymentResponseCheck(result, *path)
}

func printDeploymentResponseCheck(result api.DeploymentResponseCheckResult, path string) int {
	verified := result.HTTPStatus >= 200 && result.HTTPStatus < 300
	if jsonOutput {
		_ = json.NewEncoder(osStdout).Encode(struct {
			api.DeploymentResponseCheckResult
			Verified             bool `json:"verified"`
			PublicAccessVerified bool `json:"public_access_verified"`
		}{DeploymentResponseCheckResult: result, Verified: verified})
	} else {
		renderDeploymentResponseCheck(result, path, verified)
	}
	if !verified {
		return 1
	}
	return 0
}

func deploymentResponseCheck(ctx context.Context, client *Client, slug, deploymentID string, request api.DeploymentResponseCheckRequest) (api.DeploymentResponseCheckResult, error) {
	var empty api.DeploymentResponseCheckResult
	deployment, err := client.GetDeployment(ctx, deploymentID)
	if err != nil {
		return empty, fmt.Errorf("read deployment: %w", err)
	}
	if deployment.ID != deploymentID || deployment.Status != statusLive {
		return empty, errors.New("the selected deployment is not live; inspect it with gregale release get before checking its response")
	}
	request.AppID, request.Scope = deployment.AppID, scopeOrDefault(deployment.Scope)
	if err := request.Validate(); err != nil {
		return empty, err
	}
	var app api.AppResponse
	if request.Scope == api.DefaultEnvScope {
		app, err = client.GetApp(ctx, slug)
	} else {
		app, err = client.GetAppInEnvironment(ctx, slug, request.Scope)
	}
	if err != nil {
		return empty, fmt.Errorf("read app access: %w", err)
	}
	if app.ID != request.AppID {
		return empty, errors.New("the deployment does not belong to this app")
	}
	capabilities, err := client.GetDeploymentResponseCheckCapabilities(ctx)
	if err != nil {
		return empty, fmt.Errorf("read verification support: %w", err)
	}
	if !capabilities.SupportsCurrentProtocol() {
		return empty, errors.New("deployment verification is unavailable on this installation; retry when the service is available; the deployment is unchanged")
	}
	result, err := client.CheckDeploymentResponse(ctx, slug, deploymentID, request)
	if err != nil {
		return empty, err
	}
	if !result.Matches(request, deploymentID) {
		return empty, errors.New("response did not identify the selected app, deployment, environment, and running instance")
	}
	return result, nil
}

func renderDeploymentResponseCheck(result api.DeploymentResponseCheckResult, path string, verified bool) {
	if verified {
		PrintOK(osStdout, "Application response verified")
	} else {
		PrintWarn(osStdout, "Application response not verified")
	}
	PrintProgress(osStdout, "Deployment: %s · %s", result.DeploymentID, result.Scope)
	PrintProgress(osStdout, "GET %s → HTTP %d · owner-authorized check", startResponseText(path), result.HTTPStatus)
	PrintProgress(osStdout, "Public access has not been verified. Account credentials were not forwarded to the app.")
	if !verified {
		PrintWarn(osStdout, "%s", startHTTPFailureHint(result.HTTPStatus))
	}
	_, _ = fmt.Fprintln(osStdout, startResponseText(result.Body))
	if result.BodyTruncated {
		PrintProgress(osStdout, "Response preview shortened to 4 KB.")
	}
}

func deploymentResponseCheckCommand(slug, deploymentID, path, projectID, environmentID string) string {
	command := fmt.Sprintf("%s release verify %s --app %s --path %s", recoveryCLI(), recoveryArgument(deploymentID), recoveryArgument(slug), recoveryArgument(path))
	if environmentID != "" {
		command += " --project-id " + recoveryArgument(projectID) + " --environment-id " + recoveryArgument(environmentID)
	}
	return command
}
