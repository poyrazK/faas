package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjectPromotionInteractive(project string, timeoutSeconds int) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	environments, err := client.ListProjectEnvironments(readCtx, project)
	cancel()
	if err != nil {
		return printErr("Could not list environments", err)
	}
	if len(environments) < 2 {
		return printErr("Two environments required", errors.New("create source and destination environments before promoting"))
	}
	sort.Slice(environments, func(i, j int) bool { return environments[i].Slug < environments[j].Slug })
	labels := make([]string, len(environments))
	for i, environment := range environments {
		labels[i] = promotionEnvironmentLabel(environment)
	}
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	source, err := prompt.choose(ctx, "Choose the source environment for project "+project+".", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	var destinations []api.ProjectEnvironmentResponse
	labels = nil
	for i, environment := range environments {
		if i != source {
			destinations = append(destinations, environment)
			labels = append(labels, promotionEnvironmentLabel(environment))
		}
	}
	target, err := prompt.choose(ctx, "Choose the destination environment.", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	from, to := environments[source].Slug, destinations[target].Slug
	_, _ = fmt.Fprintln(prompt.writer, "Configuration sync copies non-secret configuration from source to destination. Secrets remain scoped to the destination.")
	syncConfig, err := prompt.confirm(ctx, "Include non-secret source configuration?")
	if err != nil {
		return startInputExit(err)
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	preview, err := client.GetProjectEnvironmentPromotionPreviewWithConfig(readCtx, project, to, from, syncConfig)
	cancel()
	if err != nil {
		return printErr("Promotion preview failed", err)
	}
	if preview.ProjectSlug != project || preview.FromEnvironment != from || preview.ToEnvironment != to || preview.SyncConfig != syncConfig {
		return printErr("Invalid promotion preview", errors.New("the preview does not match the selected project, environments, and configuration choice"))
	}
	renderProjectPromotionPreview(preview)
	if preview.ToEnvironmentProtected || preview.ApprovalRequired {
		PrintProgress(osStdout, "Destination %s is protected; its approval requirements apply to this promotion.", to)
	}
	if !preview.CanPromote {
		return printErr("Promotion is blocked", errors.New("resolve the preview's blockers before trying again"))
	}
	if preview.PromotionToken == "" {
		return printErr("Invalid promotion preview", errors.New("the preview has no promotion token"))
	}
	confirmed, err := prompt.confirm(ctx, "Promote "+project+": "+from+" -> "+to+"?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "No promotion submitted.")
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	// Submit the exact reviewed preview token; do not fetch a replacement
	// preview after confirmation. The server rejects stale preview state.
	return executeProjectPromotion(ctx, client, project, preview, "", true, true, timeoutSeconds)
}

func promotionEnvironmentLabel(environment api.ProjectEnvironmentResponse) string {
	if environment.Protected {
		return environment.Slug + " (protected)"
	}
	return environment.Slug
}
