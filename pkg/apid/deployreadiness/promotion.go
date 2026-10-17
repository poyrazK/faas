package deployreadiness

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkPromotion(ctx context.Context, r *report, store Reader, account state.Account, req api.ProjectDeploymentCheckRequest, config Config) {
	project, err := store.ProjectBySlug(ctx, account.ID, r.ProjectSlug)
	if err != nil {
		status := "unknown"
		if errors.Is(err, state.ErrNotFound) {
			status = "blocked"
		}
		r.add("promotion_project_unavailable", status, "", "The promotion project is unavailable.", "Select an existing project owned by this account.")
		return
	}
	if !req.Verify {
		checkPromotionPreview(ctx, r, account, req, config)
		return
	}
	reader, ok := store.(state.ProjectReleaseSetReader)
	if !ok {
		r.add("release_graphs_unavailable", "unknown", "", "Environment release graph inventory is unavailable.", "Use a server with project release graph support.")
		return
	}
	sources, targets := map[string]state.Deployment{}, map[string]state.Deployment{}
	graphs := map[string]state.ProjectReleaseSet{}
	workers := 0
	for _, scope := range []string{req.FromEnvironment, req.Environment} {
		graph, err := reader.ActiveProjectReleaseSet(ctx, account.ID, project.ID, scope)
		if err != nil {
			status := "unknown"
			if errors.Is(err, state.ErrNotFound) {
				status = "blocked"
			}
			r.add("promotion_release_unavailable", status, "", "A complete active release is unavailable in "+scope+".", "Deploy complete source and destination baselines with revision retention.")
			continue
		}
		graphs[scope] = graph
		r.identities["graph:"+scope] = graph
		for _, member := range graph.Members {
			deploymentReader, ok := store.(interface {
				DeploymentByID(context.Context, string) (state.Deployment, error)
			})
			if !ok {
				r.add("deployment_inventory_unavailable", "unknown", "", "Deployment metadata is unavailable.", "Retry with deployment metadata support.")
				continue
			}
			d, err := deploymentReader.DeploymentByID(ctx, member.DeploymentID)
			if err != nil {
				r.add("deployment_inventory_unavailable", "unknown", "", "Deployment metadata could not be read.", "Retry when deployment metadata is available.")
				continue
			}
			app, err := state.AppForDeployment(ctx, struct {
				Reader
				interfaceDeploymentReader
			}{store, deploymentReader}, d)
			if err != nil || app.AccountID != account.ID || app.ProjectID != project.ID {
				r.add("workload_inventory_unavailable", "unknown", "", "Release workload metadata could not be read.", "Retry when workload metadata is available.")
				continue
			}
			if scope == req.FromEnvironment {
				sources[app.Slug] = d
				if app.WorkloadClass == state.WorkloadClassWorker {
					workers++
				}
			} else {
				targets[app.Slug] = d
			}
		}
	}
	if req.Workers {
		r.quota("worker_replica_overlap", 2*workers, api.MustLimitsFor(account.Plan).WorkerReplicasMax, "Minimum worker replicas required for retained and candidate pools; other running pools still consume quota.", "Reduce worker overlap or select a plan with enough worker replicas.")
	}
	if graph, ok := graphs[req.Environment]; ok && req.Verify {
		if time.Duration(graph.TTLSeconds)*time.Second < api.ProjectPromotionHealthMinRetention {
			r.add("rollback_retention_insufficient", "blocked", "", "Destination retention does not cover promotion verification and rollback.", "Increase revision retention before publishing the destination baseline.")
		}
		full, ok := store.(state.Store)
		if !ok {
			r.add("promotion_health_policy_unavailable", "unknown", "", "Promotion policy readers are unavailable.", "Use a server with promotion policy support.")
		} else if _, err := promotion.PrepareHealth(ctx, full, account.ID, sources, targets, graph.TTLSeconds, req.Migrate, req.Workers); err != nil {
			r.add("promotion_health_policy_incompatible", "blocked", "", "HTTP probe policies or immutable workload settings do not satisfy verified promotion.", "Configure GET/HEAD probes for every HTTP member and matching source/destination workload settings.")
		} else {
			r.add("promotion_health_policy_compatible", "passed", "", "HTTP probe policies and workload settings satisfy verified promotion.", "")
		}
	}
	checkPromotionPreview(ctx, r, account, req, config)
}

func checkPromotionPreview(ctx context.Context, r *report, account state.Account, req api.ProjectDeploymentCheckRequest, config Config) {
	if config.PromotionPreview == nil {
		r.add("promotion_preview_unavailable", "unknown", "", "Authoritative promotion preview is unavailable.", "Use a server with promotion preview support.")
		return
	}
	preview, problem := config.PromotionPreview(ctx, account, r.ProjectSlug, req)
	if problem != nil {
		status := "blocked"
		if problem.Status >= 500 {
			status = "unknown"
		}
		r.add("promotion_preview_unavailable", status, "", "The authoritative promotion preview could not be completed.", "Inspect the environment promotion preview and resolve unavailable inputs.")
		return
	}
	r.identities["promotion_hash"] = preview.PromotionHash
	for _, reason := range preview.BlockingReasons {
		r.add("promotion_blocked", "blocked", "", reason, "Resolve this blocker and request a fresh promotion preview.")
	}
	if !preview.CanPromote && len(preview.BlockingReasons) == 0 {
		r.add("promotion_blocked", "blocked", "", "The current environment promotion cannot be admitted.", "Inspect a fresh promotion preview before deploying.")
	}
	if preview.ApprovalRequired {
		r.add("promotion_approval_required", "blocked", "", "The protected destination requires approval for this exact promotion preview.", "Obtain an environment approval for the fresh promotion token before submitting.")
	}
	if preview.CanPromote {
		r.add("promotion_preview_valid", "passed", "", "The current promotion preview satisfies admission checks.", "")
	}
}

type interfaceDeploymentReader interface {
	DeploymentByID(context.Context, string) (state.Deployment, error)
}
