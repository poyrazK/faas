package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// previewSummary is the stable, intentionally small read model for the
// preview CLI. The full app/deployment DTOs remain available from the API;
// this shape keeps `--json` useful for scripts without exposing every app
// setting in a discovery command.
type previewSummary struct {
	ID               string                   `json:"id"`
	Slug             string                   `json:"slug"`
	ParentSlug       string                   `json:"parent_slug"`
	PRNumber         int                      `json:"pr_number"`
	Kind             string                   `json:"kind"`
	PRState          string                   `json:"pr_state,omitempty"`
	AppStatus        string                   `json:"app_status"`
	URL              string                   `json:"url"`
	ExpiresAt        *time.Time               `json:"expires_at,omitempty"`
	LatestDeployment *previewDeploymentStatus `json:"latest_deployment,omitempty"`
}

type previewDeploymentStatus struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func previewSummaryFromApp(app api.AppResponse, deployment *api.DeploymentResponse) previewSummary {
	item := previewSummary{
		ID:         app.ID,
		Slug:       app.Slug,
		ParentSlug: app.PreviewOfSlug,
		PRNumber:   app.PreviewPRNumber,
		Kind:       "developer",
		PRState:    app.PreviewPRState,
		AppStatus:  app.Status,
		URL:        app.URL,
		ExpiresAt:  app.PreviewExpiresAt,
	}
	if item.PRNumber > 0 {
		item.Kind = "pull_request"
	}
	if deployment != nil {
		item.LatestDeployment = &previewDeploymentStatus{
			ID: deployment.ID, Status: deployment.Status, CreatedAt: deployment.CreatedAt,
		}
	}
	return item
}

func cmdPreviewList(args []string) int {
	fs := newFlagSet("preview list", flag.ContinueOnError)
	appFlag := fs.String("app", "", "parent app slug (defaults to the linked app)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(osStderr, "usage: gregale preview list [--app <slug>]", "preview")
		return 1
	}
	parent, err := previewParentFilter(*appFlag)
	if err != nil {
		return printErr("Could not resolve preview scope", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	apps, err := client.ListApps(ctx)
	if err != nil {
		return printErr("Could not list previews", err)
	}
	previewApps := make([]api.AppResponse, 0, len(apps))
	for _, app := range apps {
		if app.PreviewOfSlug != "" && (parent == "" || app.PreviewOfSlug == parent) {
			previewApps = append(previewApps, app)
		}
	}
	items, err := previewSummaries(ctx, client, previewApps)
	if err != nil {
		return printErr("Could not resolve preview deployments", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(items))
	}
	renderPreviewList(items, parent)
	return 0
}

func previewSummaries(ctx context.Context, client *api.Client, apps []api.AppResponse) ([]previewSummary, error) {
	items := make([]previewSummary, 0, len(apps))
	if len(apps) == 0 {
		return items, nil
	}
	latest, err := client.ListLatestDeploymentsByApp(ctx)
	if err != nil {
		return nil, err
	}
	byApp := make(map[string]api.DeploymentResponse, len(latest.Items))
	for _, deployment := range latest.Items {
		byApp[deployment.AppID] = deployment
	}
	for _, app := range apps {
		deployment, ok := byApp[app.ID]
		if !ok {
			items = append(items, previewSummaryFromApp(app, nil))
			continue
		}
		items = append(items, previewSummaryFromApp(app, &deployment))
	}
	return items, nil
}

func previewParentFilter(explicit string) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		if !api.ValidAppSlug(explicit) {
			return "", fmt.Errorf("app %q is not a valid app slug", explicit)
		}
		return explicit, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("read current directory: %w", err)
	}
	linked, _, err := linkedProjectContext(cwd)
	if errors.Is(err, errProjectContextNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return linked.App, nil
}

func cmdPreviewShow(args []string) int {
	if len(args) != 1 {
		PrintUsage(osStderr, "usage: gregale preview show <preview-slug>", "preview")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	preview, err := client.GetPreviewStatus(ctx, args[0])
	if err != nil {
		return printErr("Could not load preview", err)
	}
	item := previewSummaryFromApp(preview.App, preview.LatestDeployment)
	if jsonOutput {
		return jsonOut(writeJSON(item))
	}
	renderPreviewDetails(item)
	return 0
}

func renderPreviewList(items []previewSummary, parent string) {
	if len(items) == 0 {
		if parent == "" {
			_, _ = fmt.Fprintln(osStdout, "No previews found.")
		} else {
			_, _ = fmt.Fprintf(osStdout, "No previews found for %s.\n", parent)
		}
		return
	}
	_, _ = fmt.Fprintf(osStdout, "%-30s %-20s %-6s %-10s %-10s %-24s %-20s %s\n", "SLUG", "PARENT", "PR", "PR STATE", "APP STATE", "URL", "EXPIRES", "DEPLOYMENT")
	for _, item := range items {
		pr := "dev"
		if item.PRNumber > 0 {
			pr = fmt.Sprintf("#%d", item.PRNumber)
		}
		state := item.PRState
		if state == "" {
			state = GlyphEmDash
		}
		_, _ = fmt.Fprintf(osStdout, "%-30s %-20s %-6s %-10s %-10s %-24s %-20s %s\n",
			item.Slug, item.ParentSlug, pr, state, item.AppStatus, item.URL,
			previewExpiry(item.ExpiresAt), previewDeployment(item.LatestDeployment))
	}
}

func renderPreviewDetails(item previewSummary) {
	_, _ = fmt.Fprintf(osStdout, "Preview:       %s\n", item.Slug)
	_, _ = fmt.Fprintf(osStdout, "Parent app:    %s\n", item.ParentSlug)
	if item.PRNumber > 0 {
		_, _ = fmt.Fprintf(osStdout, "Pull request:  #%d\n", item.PRNumber)
	} else {
		_, _ = fmt.Fprintln(osStdout, "Type:          developer")
	}
	_, _ = fmt.Fprintf(osStdout, "State:         %s\n", valueOrDash(item.PRState))
	_, _ = fmt.Fprintf(osStdout, "App status:    %s\n", valueOrDash(item.AppStatus))
	_, _ = fmt.Fprintf(osStdout, "URL:           %s\n", valueOrDash(item.URL))
	_, _ = fmt.Fprintf(osStdout, "Expires:       %s\n", previewExpiry(item.ExpiresAt))
	_, _ = fmt.Fprintf(osStdout, "Latest deploy: %s\n", previewDeployment(item.LatestDeployment))
}

func previewExpiry(expiresAt *time.Time) string {
	if expiresAt == nil {
		return GlyphEmDash
	}
	return expiresAt.Local().Format("2006-01-02 15:04 MST")
}

func previewDeployment(deployment *previewDeploymentStatus) string {
	if deployment == nil {
		return GlyphEmDash
	}
	if deployment.ID == "" {
		return valueOrDash(deployment.Status)
	}
	return fmt.Sprintf("%s (%s)", valueOrDash(deployment.Status), shortPreviewID(deployment.ID))
}

func shortPreviewID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func valueOrDash(value string) string {
	if value == "" {
		return GlyphEmDash
	}
	return value
}
