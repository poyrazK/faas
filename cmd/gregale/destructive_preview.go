package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type destructivePreviewResource struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at,omitempty"`
}
type destructivePreview struct {
	Operation string                       `json:"operation"`
	DryRun    bool                         `json:"dry_run"`
	App       string                       `json:"app,omitempty"`
	Resources []destructivePreviewResource `json:"resources"`
	Count     int                          `json:"count"`
	Exact     bool                         `json:"exact"`
	Cutoff    string                       `json:"cutoff,omitempty"`
	Effect    string                       `json:"effect"`
	Recovery  string                       `json:"recovery"`
	Note      string                       `json:"note"`
}

func previewDeploymentInventory(ctx context.Context, client *Client, slug string) ([]api.DeploymentResponse, error) {
	result := []api.DeploymentResponse{}
	cursor := ""
	seen := map[string]bool{}
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		page, err := client.ListAppDeployments(ctx, slug, cursor, 200)
		if err != nil {
			return nil, err
		}
		result = append(result, page.Items...)
		if page.NextBefore == "" {
			return result, nil
		}
		if seen[page.NextBefore] || page.NextBefore == cursor {
			return nil, errors.New("deployment preview pagination did not advance")
		}
		seen[page.NextBefore] = true
		cursor = page.NextBefore
	}
	return nil, errors.New("deployment preview exceeds 100 pages; narrow the selection before proceeding")
}
func previewResource(dep api.DeploymentResponse) destructivePreviewResource {
	return destructivePreviewResource{Kind: "deployment", ID: dep.ID, Status: dep.Status, CreatedAt: dep.CreatedAt}
}
func previewAppDeletion(ctx context.Context, client *Client, slug string) (destructivePreview, error) {
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return destructivePreview{}, err
	}
	deps, err := previewDeploymentInventory(ctx, client, slug)
	if err != nil {
		return destructivePreview{}, err
	}
	p := destructivePreview{Operation: "delete_app", DryRun: true, App: slug, Exact: true, Resources: []destructivePreviewResource{{Kind: "app", ID: app.ID, Status: app.Status}}, Effect: "Schedule app deletion, stop serving it, and cancel pending invocations. Deployment history remains during the deletion grace window.", Recovery: "Restore the app before its server deletion grace deadline (normally seven days): " + recoveryCLI() + " apps restore " + recoveryArgument(slug), Note: "Inventory is a read-only snapshot; resources may change before deletion. The server enforces deletion policy when applying."}
	for _, dep := range deps {
		p.Resources = append(p.Resources, previewResource(dep))
	}
	p.Count = len(p.Resources)
	return p, nil
}
func previewSingleDeployment(ctx context.Context, client *Client, id string) (destructivePreview, error) {
	dep, err := client.GetDeployment(ctx, id)
	if err != nil {
		return destructivePreview{}, err
	}
	p := destructivePreview{Operation: "clear_deployment", DryRun: true, Resources: []destructivePreviewResource{previewResource(dep)}, Count: 1, Exact: true, Effect: "If eligible, soft-delete the deployment from visible history; keep its audit record.", Recovery: "The CLI has no deployment unhide command. Clearing history does not restore or roll back application traffic.", Note: "The server rejects live deployments and rechecks eligibility when applying."}
	return p, nil
}
func previewObsoleteDeployments(ctx context.Context, client *Client, slug string, age time.Duration) (destructivePreview, error) {
	deps, err := previewDeploymentInventory(ctx, client, slug)
	if err != nil {
		return destructivePreview{}, err
	}
	cutoff := time.Now().UTC().Add(-age)
	p := destructivePreview{Operation: "clear_obsolete_deployments", DryRun: true, App: slug, Resources: []destructivePreviewResource{}, Exact: false, Cutoff: cutoff.Format(time.RFC3339Nano), Effect: "Soft-delete eligible obsolete deployments from visible history; keep audit records.", Recovery: "The CLI has no deployment unhide command. Current serving deployments remain protected by server policy.", Note: "Candidates match age and terminal status only. Server retention protections can reduce the applied count; concurrent changes can alter the selection. This preview does not check plan eligibility."}
	for _, dep := range deps {
		switch dep.Status {
		case "superseded", "failed", "cancelled":
		default:
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, dep.CreatedAt)
		if err != nil {
			return destructivePreview{}, fmt.Errorf("cannot preview deployment %s: invalid creation timestamp", dep.ID)
		}
		if created.Before(cutoff) {
			p.Resources = append(p.Resources, previewResource(dep))
		}
	}
	p.Count = len(p.Resources)
	return p, nil
}
func renderDestructivePreview(w io.Writer, p destructivePreview) {
	_, _ = fmt.Fprintf(w, "Preview %s: %d resource(s)\n", p.Operation, p.Count)
	for _, r := range p.Resources {
		_, _ = fmt.Fprintf(w, "  %s %s (%s)\n", r.Kind, r.ID, r.Status)
	}
	_, _ = fmt.Fprintf(w, "Effect: %s\nRecovery: %s\nNote: %s\n", p.Effect, p.Recovery, p.Note)
}
func writeDestructivePreview(p destructivePreview) int {
	if jsonOutput {
		return jsonOut(writeJSON(p))
	}
	renderDestructivePreview(osStdout, p)
	return 0
}
