package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdRegistryPublished is the post-push CI handoff. It never resolves a tag:
// the publisher supplies the exact digest produced by its build.
func cmdRegistryPublished(args []string) int {
	fs := newFlagSet("registry published", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	image := fs.String("image", "", "published registry/repository@sha256:digest (required)")
	scope := fs.String("scope", "", "deployment scope (default: default)")
	environment := fs.String("environment", "", "registered project environment")
	wait := fs.Bool("wait", false, "wait for the deployment to finish")
	timeout := fs.Duration("timeout", defaultDeployWaitTimeout, "deployment wait timeout")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *slug == "" || !api.ValidDeploymentImage(*image) || *timeout <= 0 || (*scope != "" && *environment != "") {
		return printErr("Invalid flags", fmt.Errorf("--app and a digest-pinned --image are required; use either --scope or --environment and a positive --timeout"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dep, err := client.PublishAppImage(ctx, *slug, api.CreateDeploymentRequest{Image: *image, Scope: *scope, Environment: *environment})
	if err != nil {
		return printErr("Image publication failed", err)
	}
	if *wait {
		return finishPublishedImageWait(client, *slug, dep, *timeout)
	}
	if jsonOutput {
		return jsonOut(writeJSON(dep))
	}
	PrintOK(osStdout, "Image accepted for %s: deployment=%s status=%s", *slug, dep.ID, dep.Status)
	return 0
}

func finishPublishedImageWait(client *Client, slug string, dep api.DeploymentResponse, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	final, ok := waitForDeploymentReceiptUntil(ctx, client, dep, timeout)
	if !ok {
		if jsonOutput {
			if err := writeJSON(dep); err != nil {
				return 1
			}
		}
		warnDeploymentWaitTimeout(slug, dep.ID, timeout)
		return 1
	}
	if jsonOutput {
		if err := writeJSON(final); err != nil {
			return 1
		}
	} else if final.Status == statusLive {
		PrintOK(osStdout, "Image deployment %s: %s", final.ID, final.Status)
	} else {
		PrintFail(osStderr, "Image deployment %s: %s", final.ID, final.Status)
	}
	if final.Status != statusLive {
		return 1
	}
	return 0
}
