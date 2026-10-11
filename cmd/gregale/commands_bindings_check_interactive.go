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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

func cmdBindingsCheckInteractive(slug string, timeout, interval time.Duration) int {
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
	var target api.DeploymentResponse
	for page := 0; page < 100; page++ {
		readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
		history, readErr := client.ListAppDeployments(readCtx, slug, cursor, 20)
		cancel()
		if readErr != nil {
			return printErr("Could not list deployments", readErr)
		}
		live := []api.DeploymentResponse{}
		labels := []string{}
		for _, deployment := range history.Items {
			if deployment.AppID != app.ID || !deploymentIDPattern.MatchString(deployment.ID) {
				return printErr("Invalid deployment history", errors.New("an entry does not match the selected app or has an invalid ID"))
			}
			if deployment.Status != "live" {
				continue
			}
			live = append(live, deployment)
			labels = append(labels, fmt.Sprintf("%s · scope %s · %s", deploymentLabel(deployment), oneLine(scopeOrDefault(deployment.Scope)), deployment.ID))
		}
		more := history.NextBefore != ""
		if len(live) == 0 && !more {
			PrintProgress(osStdout, "No live deployments available on this history page for %s.", slug)
			return 0
		}
		if more {
			labels = append(labels, "Show older deployments")
		}
		labels = append(labels, "Cancel")
		choice, inputErr := prompt.choose(ctx, "Choose a live deployment to check for "+slug+".", labels, 0)
		if inputErr != nil {
			return startInputExit(inputErr)
		}
		if choice == len(labels)-1 {
			return 0
		}
		if more && choice == len(live) {
			if history.NextBefore == cursor || seen[history.NextBefore] {
				return printErr("Invalid pagination", errors.New("the server repeated a deployment cursor"))
			}
			seen[history.NextBefore] = true
			cursor = history.NextBefore
			continue
		}
		target = live[choice]
		break
	}
	if target.ID == "" {
		return printErr("Deployment history limit reached", errors.New("use bindings check APP --deployment ID for an explicit target"))
	}
	policy := bindingcheck.Policy{App: slug, DeploymentID: target.ID, Scope: scopeOrDefault(target.Scope), MaxVerificationAge: bindingcheck.DefaultMaxVerificationAge}
	PrintProgress(osStdout, "Checking %s: deployment %s; scope %s.", slug, deploymentLabel(target), oneLine(policy.Scope))
	for {
		value, inputErr := prompt.text(ctx, "Maximum verification age", policy.MaxVerificationAge.String())
		if inputErr != nil {
			return startInputExit(inputErr)
		}
		age, parseErr := time.ParseDuration(value)
		if parseErr == nil && age > 0 {
			policy.MaxVerificationAge = age
			break
		}
		_, _ = fmt.Fprintln(osStderr, "Enter a positive duration, such as 10m.")
	}
	policy.RequireApplicationAck, err = prompt.confirm(ctx, "Require current PostgreSQL and object-storage application acknowledgements?")
	if err != nil {
		return startInputExit(err)
	}
	policy.AllowUnsupported, err = prompt.confirm(ctx, "Waive connectivity coverage for active queue and outbound bindings?")
	if err != nil {
		return startInputExit(err)
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "bindings", "check", quoteLogCommandArg(slug), "--deployment", quoteLogCommandArg(target.ID), "--scope", quoteLogCommandArg(policy.Scope), "--max-verification-age", policy.MaxVerificationAge.String())
	if policy.RequireApplicationAck {
		command = append(command, "--require-application-ack")
	}
	if policy.AllowUnsupported {
		command = append(command, "--allow-unsupported")
	}
	_, _ = fmt.Fprintln(osStdout, "Equivalent command (POSIX shells):\n"+strings.Join(command, " "))
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	report, err := pollBindingCheck(readCtx, client, policy, false, interval)
	cancel()
	if err != nil {
		return printErr("Could not check bindings", err)
	}
	renderBindingCheck(report)
	if report.Passed {
		return 0
	}
	follow, err := prompt.confirm(ctx, "Wait for pending checks? (existing work only)")
	if err != nil {
		return startInputExit(err)
	}
	if !follow {
		return 1
	}
	command = append(command, "--wait", "--timeout", timeout.String(), "--poll-interval", interval.String())
	_, _ = fmt.Fprintln(osStdout, "Resume command (POSIX shells):\n"+strings.Join(command, " "))
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	report, err = pollBindingCheck(waitCtx, client, policy, true, interval)
	if report.App != "" {
		renderBindingCheck(report)
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if err != nil {
		return printErr("Binding check wait ended", err)
	}
	if !report.Passed {
		return 1
	}
	return 0
}
