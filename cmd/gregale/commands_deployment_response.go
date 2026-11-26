package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Check the exact accepted release without the synchronous invocation plan gate.
// The response is ephemeral and says nothing about anonymous public access.
func cmdDeploymentCheckResponse(args []string) int {
	fs := newFlagSet("release verify", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	path := fs.String("path", "/", "app-relative GET path")
	project := fs.String("project-id", "", "project UUID for a named environment (looked up when omitted)")
	environment := fs.String("environment-id", "", "environment UUID for a named environment (looked up when omitted)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	// In the app's checkout --app defaults to the linked app (prod hunt
	// r53: the check every deploy prints printed usage there without it).
	fillLinkedApp(app)
	if fs.NArg() != 1 || !api.ValidDeploymentResponseCheckID(fs.Arg(0)) || !api.ValidAppSlug(*app) {
		PrintUsage(osStderr, "usage: gregale release verify <deployment-uuid> [--app SLUG] [--path /] [--project-id UUID --environment-id UUID]", "deployment")
		return 1
	}
	if !api.ValidDeploymentResponseCheckPath(*path) {
		return printErr("Invalid response check", errors.New("path must be a bounded app-relative GET path without a fragment or controls"))
	}
	if (*project == "") != (*environment == "") || (*project != "" && (!api.ValidDeploymentResponseCheckID(*project) || !api.ValidDeploymentResponseCheckID(*environment))) {
		return printErr("Invalid response check", errors.New("pass both --project-id and --environment-id as platform UUIDs, or neither"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	interruptCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	request := api.DeploymentResponseCheckRequest{Path: *path, ProjectID: *project, EnvironmentID: *environment}
	result, err := deploymentResponseCheck(interruptCtx, client, *app, fs.Arg(0), request)
	if err != nil {
		if interruptCtx.Err() != nil {
			_, _ = fmt.Fprintln(osStderr, "Interrupted; the deployment is unchanged.")
			return 130
		}
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

// Setup reads (deployment, app, capability, environment lookup) get their own
// budget so a slow control plane cannot eat the time a cold boot needs.
const (
	deploymentResponseSetupTimeout = 15 * time.Second
	deploymentResponseCheckTimeout = 30 * time.Second
)

func deploymentResponseCheck(ctx context.Context, client *Client, slug, deploymentID string, request api.DeploymentResponseCheckRequest) (api.DeploymentResponseCheckResult, error) {
	var empty api.DeploymentResponseCheckResult
	setupCtx, cancelSetup := context.WithTimeout(ctx, deploymentResponseSetupTimeout)
	defer cancelSetup()
	deployment, err := client.GetDeployment(setupCtx, deploymentID)
	if err != nil {
		return empty, fmt.Errorf("read deployment: %w", err)
	}
	if !sameUUID(deployment.ID, deploymentID) {
		return empty, errors.New("the API returned a different deployment than the one requested; the deployment is unchanged")
	}
	if deployment.Status != statusLive {
		return empty, errors.New("the selected deployment is not live; inspect it with gregale release get before checking its response")
	}
	// Use the API's canonical ID form so later identity checks compare like with like.
	deploymentID = deployment.ID
	request.AppID, request.Scope = deployment.AppID, scopeOrDefault(deployment.Scope)

	// The app read and the capability read are independent; run them together.
	var (
		app                api.AppResponse
		appErr, supportErr error
		supported          bool
		failure            verificationFailure
		wg                 sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		supported, failure, supportErr = checkVerificationSupport(setupCtx, client)
	}()
	if request.Scope == api.DefaultEnvScope {
		app, appErr = client.GetApp(setupCtx, slug)
	} else {
		app, appErr = client.GetAppInEnvironment(setupCtx, slug, request.Scope)
	}
	wg.Wait()
	if appErr != nil {
		return empty, fmt.Errorf("read app access: %w", appErr)
	}
	if app.ID != request.AppID {
		return empty, errors.New("the deployment does not belong to this app")
	}
	if supportErr != nil {
		return empty, supportErr
	}
	if !supported {
		return empty, releaseVerifyError(failure)
	}
	if request.Scope != api.DefaultEnvScope && request.EnvironmentID == "" {
		if request.ProjectID, request.EnvironmentID, err = resolveDeploymentEnvironment(setupCtx, client, app, request.Scope); err != nil {
			return empty, err
		}
	}
	if err := request.Validate(); err != nil {
		return empty, err
	}
	cancelSetup()

	checkCtx, cancelCheck := context.WithTimeout(ctx, deploymentResponseCheckTimeout)
	defer cancelCheck()
	result, err := client.CheckDeploymentResponse(checkCtx, slug, deploymentID, request)
	if err != nil {
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		var problem *APIError
		if errors.As(err, &problem) {
			if failure := classifyVerificationServiceError(err); failure != verificationUnavailable || problem.Problem.Status >= 500 {
				return empty, releaseVerifyError(failure)
			}
		}
		return empty, err
	}
	if !result.Matches(request, deploymentID) {
		return empty, errors.New("response did not identify the selected app, deployment, environment, and running instance")
	}
	return result, nil
}

// resolveDeploymentEnvironment finds the project and environment UUIDs for a
// named-environment deployment, so users need not copy them from the catalog.
func resolveDeploymentEnvironment(ctx context.Context, client *Client, app api.AppResponse, scope string) (string, string, error) {
	if app.ProjectID == "" {
		return "", "", fmt.Errorf("app is not part of a project, so environment %q cannot be resolved; pass --project-id and --environment-id", scope)
	}
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return "", "", fmt.Errorf("look up the app's project: %w", err)
	}
	projectSlug := ""
	for _, project := range projects {
		if sameUUID(project.ID, app.ProjectID) {
			projectSlug = project.Slug
			break
		}
	}
	if projectSlug == "" {
		return "", "", errors.New("the app's project is not visible to this account; pass --project-id and --environment-id")
	}
	environment, err := client.GetProjectEnvironment(ctx, projectSlug, scope)
	if err != nil {
		return "", "", fmt.Errorf("look up environment %q: %w", scope, err)
	}
	if environment.ID == "" || environment.Slug != scope || !sameUUID(environment.ProjectID, app.ProjectID) {
		return "", "", fmt.Errorf("environment %q does not belong to the app's project", scope)
	}
	return app.ProjectID, environment.ID, nil
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
	if result.HTTPStatus >= 300 && result.HTTPStatus < 400 {
		PrintWarn(osStdout, "The app answered with a redirect (HTTP %d). Only 2xx counts as verified; rerun with --path set to the route the redirect leads to.", result.HTTPStatus)
	} else if !verified {
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

// sameUUID compares platform UUIDs ignoring case and dashes, so the
// 36-character form a user pastes matches the 32-character form the API returns.
func sameUUID(a, b string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "-", "")) }
	return a != "" && norm(a) == norm(b)
}
