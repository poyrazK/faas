package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAppScaleInteractive(slug, environment string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	revision := int64(-1)
	appClient := environmentAppClient{Client: client, environment: environment, revision: &revision}
	app, err := appClient.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not read current settings", err)
	}
	account, err := client.Whoami(ctx)
	if err != nil {
		return printErr("Could not read account plan", err)
	}
	limits, ok := api.LimitsFor(api.Plan(account.Plan))
	if !ok {
		return printErr("Unknown account plan", fmt.Errorf("use explicit scale flags with --plan to review settings"))
	}
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	_, _ = fmt.Fprintf(prompt.writer, "App: %s; account plan: %s\n", slug, account.Plan)
	if environment != "" {
		_, _ = fmt.Fprintf(prompt.writer, "Environment: %s\n", environment)
	}
	_, _ = fmt.Fprintf(prompt.writer, "Current: %d MB RAM; %d CPU millicores; instance cap %d; warm instances %d\n", appScalePlanCurrentRAM(app), app.CPUMillicores, app.MaxConcurrency, appScalePlanCurrentMin(app))
	choices := []string{"Keep current memory and CPU"}
	profiles := []string{""}
	for _, profile := range api.ResourceProfiles {
		if profile.MemoryMB <= limits.RAMMB {
			profiles = append(profiles, string(profile.Name))
			choices = append(choices, fmt.Sprintf("%s: %d MB RAM, %d CPU millicores", profile.Name, profile.MemoryMB, profile.CPUMillicores))
		}
	}
	selection, err := prompt.choose(ctx, "Memory limits each instance's working space; CPU controls its sustained compute allowance.", choices, 0)
	if err != nil {
		return startInputExit(err)
	}
	request := api.UpdateAppRequest{}
	if selection > 0 {
		request.ResourceProfile = &profiles[selection]
	}
	_, _ = fmt.Fprintln(prompt.writer, "The instance cap limits concurrent instances of this app; it is separate from requests handled inside one instance.")
	cap, err := scalePromptInt(ctx, prompt, "Instance cap", app.MaxConcurrency, 1, limits.MaxConcurrency)
	if err != nil {
		return startInputExit(err)
	}
	if cap != app.MaxConcurrency {
		request.MaxConcurrency = &cap
	}
	maxWarm := 0
	if limits.MinInstancesAllowed {
		maxWarm = min(limits.MaxMinInstances, cap)
	}
	_, _ = fmt.Fprintln(prompt.writer, "Warm instances reduce cold starts and consume resident usage. Zero allows scaling to zero.")
	warm, err := scalePromptInt(ctx, prompt, "Minimum warm instances", appScalePlanCurrentMin(app), 0, maxWarm)
	if err != nil {
		return startInputExit(err)
	}
	if warm != appScalePlanCurrentMin(app) {
		request.MinInstances = &warm
	}
	changes, err := buildAppScalePlanChanges(app, request)
	if err != nil {
		return printErr("Could not build settings preview", err)
	}
	if request.ResourceProfile == nil && request.MaxConcurrency == nil && request.MinInstances == nil {
		PrintProgress(osStdout, "Settings unchanged.")
		return 0
	}
	view := appScalePlanView{SchemaVersion: 1, AppSlug: slug, Environment: environment, AccountPlan: account.Plan,
		PlanLimits: &appScalePlanLimits{MaxMemoryMB: limits.RAMMB, MaxConcurrentVMs: limits.MaxConcurrency, MinInstancesAllowed: limits.MinInstancesAllowed, MaxMinInstances: limits.MaxMinInstances},
		Changes:    changes, Warnings: appScalePlanWarnings(app, request, api.Plan(account.Plan), limits),
		ResidentUsage: buildAppScaleResidentUsage(app, request, api.Plan(account.Plan), limits),
		Notes:         []string{"The API rechecks permissions and plan limits when applying settings.", "Resident usage excludes request-driven compute, egress and included plan usage; this is not a total bill estimate."}}
	plan := appScaleSavedPlan{SchemaVersion: 1, AppSlug: slug, Environment: environment, Request: request, Preview: view}
	if environment != "" {
		if revision < 0 {
			return printErr("Could not build settings plan", fmt.Errorf("environment settings did not include a workload revision"))
		}
		plan.BaseWorkloadRevision = &revision
	} else {
		plan.BaseConfigSHA256, err = appScaleCurrentConfigHash(app)
		if err != nil {
			return printErr("Could not build settings plan", err)
		}
	}
	printAppScalePlan(osStdout, view)
	action, err := prompt.choose(ctx, "What would you like to do with this plan?", []string{"Finish without applying", "Save plan for later", "Apply after confirmation"}, 0)
	if err != nil {
		return startInputExit(err)
	}
	switch action {
	case 1:
		path, err := prompt.text(ctx, "New plan file path", "")
		if err != nil {
			return startInputExit(err)
		}
		if err := writeAppScaleSavedPlan(path, plan); err != nil {
			return printErr("Could not save settings plan", err)
		}
		PrintOK(osStdout, "Saved plan to %s", path)
		_, _ = fmt.Fprintln(osStdout, "Apply later with gregale app <slug> scale --apply <plan-file> --confirm.")
	case 2:
		confirmed, err := prompt.confirm(ctx, "Apply these settings to "+slug+"?")
		if err != nil {
			return startInputExit(err)
		}
		if confirmed {
			return applyAppScaleSavedPlan(slug, plan)
		}
	}
	return 0
}

func scalePromptInt(ctx context.Context, prompt *startPrompt, label string, current, low, high int) (int, error) {
	for {
		value, err := prompt.text(ctx, fmt.Sprintf("%s (%d..%d)", label, low, high), strconv.Itoa(current))
		if err != nil {
			return 0, err
		}
		n, err := strconv.Atoi(value)
		if err == nil && n >= low && n <= high {
			return n, nil
		}
		_, _ = fmt.Fprintf(prompt.writer, "Enter a whole number from %d to %d.\n", low, high)
	}
}
