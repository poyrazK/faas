package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
)

const (
	previewCLIDefaultTTLHours = 7 * 24
	previewCLIMaxTTLHours     = 30 * 24
)

type previewCreateReceipt struct {
	Preview    previewSummary `json:"preview"`
	Deployment *DeployReceipt `json:"deployment,omitempty"`
}

// cmdPreviewCreate provisions a stable PR preview and deploys a GitHub ref to
// it. The two API calls intentionally remain separate: callers can retry the
// deployment without provisioning another preview app.
func cmdPreviewCreate(args []string) int {
	fs := newFlagSet("preview create", flag.ContinueOnError)
	appFlag := fs.String("app", "", "parent app slug (defaults to the linked app)")
	repo := fs.String("repo", "", "GitHub repository OWNER/NAME")
	ref := fs.String("ref", "", "branch, tag, or commit SHA")
	prNumber := fs.Int("pr-number", 0, "pull-request number")
	ttlHours := fs.Int("ttl-hours", previewCLIDefaultTTLHours, "preview lease in hours (1-720)")
	waitDeploy := fs.Bool("wait", false, "wait for the deployment to become live (default)")
	noWait := fs.Bool("no-wait", false, "return after the deployment is queued")
	timeoutSeconds := fs.Int("timeout", defaultDeployWaitTimeoutSeconds, "maximum seconds to wait for deployment readiness")
	idempotencyKey := fs.String("idempotency-key", "", "stable logical retry key for this preview operation")
	openURL := fs.Bool("open", false, "open the preview URL after queueing or successful deployment")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *waitDeploy && *noWait {
		return printErr("Invalid flags", fmt.Errorf("--wait and --no-wait are mutually exclusive"))
	}
	if err := validateRepoSlug(*repo); err != nil {
		return printErr("Invalid --repo", err)
	}
	if err := validateGitHubRef(*ref); err != nil {
		return printErr("Invalid --ref", err)
	}
	if *prNumber <= 0 {
		return printErr("Invalid --pr-number", fmt.Errorf("must be greater than zero"))
	}
	if *ttlHours < 1 || *ttlHours > previewCLIMaxTTLHours {
		return printErr("Invalid --ttl-hours", fmt.Errorf("must be between 1 and %d", previewCLIMaxTTLHours))
	}
	if *timeoutSeconds <= 0 || *timeoutSeconds > int((24*time.Hour)/time.Second) {
		return printErr("Invalid --timeout", fmt.Errorf("must be between 1 and 86400 seconds"))
	}
	if err := validateDeployIdempotencyKey(*idempotencyKey); err != nil {
		return printErr("Invalid --idempotency-key", err)
	}
	parent, err := previewParentFilter(*appFlag)
	if err != nil {
		return printErr("Could not resolve preview scope", err)
	}
	if parent == "" {
		return printErr("Parent app required", fmt.Errorf("pass --app or run from a linked Gregale project"))
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	createCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if *idempotencyKey != "" {
		createCtx = api.ContextWithIdempotencyKey(createCtx, deployOperationIdempotencyKey(*idempotencyKey, "preview-create"))
	}
	previewApp, err := client.CreatePreview(createCtx, parent, api.CreatePreviewRequest{
		PRNumber: *prNumber, TTLHours: *ttlHours,
	})
	if err != nil {
		return printErr("Could not create preview", err)
	}
	previewURL := canonicalAppURL(previewApp)

	deployCtx := context.Background()
	if *idempotencyKey != "" {
		deployCtx = api.ContextWithIdempotencyKey(deployCtx, deployOperationIdempotencyKey(*idempotencyKey, "preview-deploy"))
	}
	dep, err := client.DeployFromSourceRef(deployCtx, previewApp.Slug, api.SourceRefDeployRequest{
		Repo: *repo, Ref: *ref, Format: "tarball", DeployedBy: resolveDeployedBy(""), PRNumber: *prNumber,
	})
	if err != nil {
		return printErr("Could not deploy preview", err)
	}

	if *noWait {
		if jsonOutput {
			return jsonOut(writeJSON(previewCreateReceipt{
				Preview:    previewSummaryFromApp(previewApp, &dep),
				Deployment: newDeployReceipt(dep, nil, previewURL, ""),
			}))
		}
		PrintOK(osStdout, "Preview %s queued at %s (deployment %s)", previewApp.Slug, previewURL, dep.ID)
		openPreviewURL(*openURL, previewURL)
		return 0
	}

	if jsonOutput {
		return writePreviewCreateJSON(client, previewApp, previewURL, dep, time.Duration(*timeoutSeconds)*time.Second)
	}
	PrintProgress(osStdout, "Preview %s created at %s", previewApp.Slug, previewURL)
	code := streamDeployLogsContextWithOptions(context.Background(), client, dep, previewApp.Slug,
		streamDeployOptions{waitTimeout: time.Duration(*timeoutSeconds) * time.Second})
	if code == 0 {
		openPreviewURL(*openURL, previewURL)
	}
	return code
}

func writePreviewCreateJSON(client *Client, app api.AppResponse, appURL string, dep api.DeploymentResponse, deadline time.Duration) int {
	waitCtx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	final, ok := waitForDeploymentReceiptUntil(waitCtx, client, dep, deadline)
	receiptDep := dep
	if final.ID != "" {
		receiptDep = final
	}
	receipt := previewCreateReceipt{
		Preview:    previewSummaryFromApp(app, &receiptDep),
		Deployment: newDeployReceipt(receiptDep, nil, appURL, ""),
	}
	if !ok {
		receipt.Deployment.TimedOut = true
		receipt.Deployment.ResumeCommand = deploymentWaitResumeCommand(dep.ID, deadline)
		PrintWarn(osStderr, "deployment did not reach a terminal state before the wait deadline; resume with: %s", receipt.Deployment.ResumeCommand)
		if code := jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
		return 3
	}
	if code := jsonOut(writeJSON(receipt)); code != 0 {
		return code
	}
	if receipt.Deployment.Status != statusLive {
		return 1
	}
	return 0
}

func openPreviewURL(open bool, url string) {
	if !open || strings.TrimSpace(url) == "" {
		return
	}
	if err := browser.Open(url); err != nil {
		PrintWarn(osStderr, "Could not open preview URL: %v", err)
	}
}
