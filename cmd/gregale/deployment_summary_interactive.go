package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func cmdDeploymentSummaryInteractive(slug string) int {
	if !validCLISlug(slug) {
		return printErr("Invalid app", errors.New("pass a valid app slug"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	app, err := client.GetApp(readCtx, slug)
	cancel()
	if err != nil {
		return printErr("Could not read app", err)
	}
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		deployments, err := client.ListAppDeployments(readCtx, slug, cursor, 20)
		cancel()
		if err != nil {
			return printErr("Could not list releases", err)
		}
		labels := make([]string, 0, len(deployments.Items)+2)
		for _, deployment := range deployments.Items {
			if deployment.AppID != app.ID || !deploymentIDPattern.MatchString(deployment.ID) {
				return printErr("Invalid release history", errors.New("a history entry does not match the selected app or has an invalid deployment ID"))
			}
			labels = append(labels, fmt.Sprintf("%s · %s · %s · scope %s", deploymentLabel(deployment), deployment.Status, deployment.CreatedAt, scopeOrDefault(deployment.Scope)))
		}
		if len(labels) == 0 && deployments.NextBefore == "" {
			message := "No releases available for %s."
			if page > 0 {
				message = "No older releases available for %s."
			}
			PrintProgress(osStdout, message, slug)
			return 0
		}
		more := deployments.NextBefore != ""
		if more {
			labels = append(labels, "Show older releases")
		}
		labels = append(labels, "Cancel")
		selection, err := prompt.choose(ctx, "Choose a release to inspect for "+slug+".", labels, 0)
		if err != nil {
			return startInputExit(err)
		}
		if selection == len(labels)-1 {
			PrintProgress(osStdout, "Release selection canceled.")
			return 0
		}
		if more && selection == len(deployments.Items) {
			if deployments.NextBefore == cursor || seen[deployments.NextBefore] {
				return printErr("Invalid release pagination", errors.New("the server repeated a history cursor"))
			}
			seen[deployments.NextBefore] = true
			cursor = deployments.NextBefore
			continue
		}
		target := deployments.Items[selection]
		readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
		summary, err := client.GetAppDeploymentSummary(readCtx, slug, target.ID)
		cancel()
		if err != nil {
			return printErr("Could not fetch selected release summary", err)
		}
		if summary.Deployment.AppID != app.ID || !sameBindingDeployment(summary.Deployment.ID, target.ID) || scopeOrDefault(summary.Deployment.Scope) != scopeOrDefault(target.Scope) {
			return printErr("Invalid release summary", errors.New("the summary does not match the selected app, release, and scope"))
		}
		command := []string{"gregale"}
		if profile := currentProfile(); profile != "default" {
			command = append(command, "--profile", quoteLogCommandArg(profile))
		}
		command = append(command, "deployment", "summary", quoteLogCommandArg(target.ID), "--app", quoteLogCommandArg(slug))
		_, _ = fmt.Fprintln(osStdout, "Equivalent command (POSIX shells):\n"+strings.Join(command, " "))
		PrintProgress(osStdout, "App: %s; scope: %s; release: %s", slug, scopeOrDefault(target.Scope), deploymentLabel(target))
		return renderDeploymentSummary(summary)
	}
	return printErr("Release history limit reached", errors.New("use gregale deployments --app APP with pagination to inspect older releases"))
}
