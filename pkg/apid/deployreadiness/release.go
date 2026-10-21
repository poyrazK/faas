package deployreadiness

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/serviceadditions"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkMode(r *report, req api.ProjectDeploymentCheckRequest, config Config) {
	valid := (!req.Verify || req.Atomic) && (!req.Workers || req.Atomic && req.Verify) && (!req.WorkerHealth || req.Workers) && (!req.Migrate || req.Atomic && req.Verify) && (!req.Progressive || req.Atomic && req.Verify && len(req.ReleaseConfig) > 0 && (!req.Workers || req.WorkerHealth)) && (!req.Stabilize || req.Progressive)
	valid = valid && (!req.Atomic || req.Environment == "production") && (len(req.ReleaseConfig) == 0 || req.Atomic && req.Verify) && !req.SyncConfig
	valid = valid && (!req.AddServices || req.Atomic && !req.Workers && !req.Migrate && !req.Progressive && len(req.ReleaseConfig) == 0 && req.FromEnvironment == "")
	if !valid {
		r.add("release_mode_invalid", "blocked", "", "The requested release options are incompatible.", "Use atomic verification for workers/migrations; progressive rollout also requires paired configuration and worker health; stabilization requires progressive rollout.")
	} else {
		r.add("release_mode_valid", "passed", "", "Requested release options are compatible.", "")
	}
	if req.Workers && !config.WorkerReleasesEnabled {
		r.add("worker_release_unqualified", "blocked", "", "Atomic worker admission is disabled pending native qualification.", "Use the qualified server admission policy before requesting an atomic worker release.")
	}
	if req.AddServices && !config.ServiceAdditionsEnabled {
		r.add("service_additions_unqualified", "blocked", "", "Atomic service additions are disabled pending native qualification.", "Use a server qualified for atomic HTTP service additions.")
	}
	if req.Progressive && !config.ProgressiveEnabled {
		r.add("progressive_release_unqualified", "blocked", "", "Progressive release admission is disabled pending native qualification.", "Use a server qualified for progressive project releases.")
	}
	if req.Verify && !config.CriticalProbesConfigured {
		r.add("critical_probes_unavailable", "blocked", "", "The server has no configured critical gateway probe sender.", "Configure critical gateway probes before requesting verification.")
	}
	if len(req.ReleaseConfig) > 0 {
		if _, _, err := api.NormalizeProjectReleaseConfig(req.ReleaseConfig); err != nil {
			r.add("release_config_invalid", "blocked", "", "The proposed release configuration is invalid.", "Supply a valid workload configuration document using --config-file.")
		}
	}
}

func checkRelease(ctx context.Context, r *report, store Reader, account state.Account, project state.Project, apps []state.App, baseline state.ProjectReleaseSet, req api.ProjectDeploymentCheckRequest, scan reposcan.Result, config Config) {
	var sourceErr error
	if req.AddServices {
		safe := &api.ProjectSafeRelease{AddServices: true, PreviousReleaseID: baseline.ID, TTLSeconds: baseline.TTLSeconds}
		_, sourceErr = serviceadditions.Plan(ctx, store, account, project, safe, apps, scan)
		r.identities["service_additions"] = api.ProjectReleaseAdditionsPolicyID(safe.Additions)
	} else {
		var serving []state.App
		dormant := map[string]bool{}
		if reader, ok := store.(state.ProjectReleaseAdditionsStore); ok {
			dormant, sourceErr = reader.DormantProjectReleaseApps(ctx, account.ID, project.ID)
		}
		for _, app := range apps {
			if !dormant[app.ID] {
				serving = append(serving, app)
			}
		}
		if sourceErr == nil {
			_, sourceErr = reconcile.ProjectSafeSourceApps(serving, scan, req.Migrate, req.Workers)
		}
	}
	if sourceErr != nil {
		r.add("atomic_source_incompatible", "blocked", "", "Source membership or workload settings are incompatible with an atomic source release.", "Keep existing source workload membership and settings unchanged; deploy configuration changes through the supported release configuration contract.")
	} else {
		r.add("atomic_source_compatible", "passed", "", "Source matches the requested atomic project membership and workload settings.", "")
	}
	owners := 0
	workers := 0
	for _, item := range req.Workloads {
		if len(item.ReleaseCommand) > 0 {
			owners++
		}
		if item.Workload.Class == "worker" {
			workers++
		}
	}
	if req.Migrate && owners != 1 || !req.Migrate && owners > 0 {
		r.add("migration_owner_invalid", "blocked", "", "Migration intent must select exactly one declared release command and opt into migration.", "Declare one backward-compatible release command and use --atomic --verify --migrate.")
	}
	if workers > 0 && !req.Workers {
		r.add("worker_opt_in_required", "blocked", "", "Atomic source releases containing a worker require explicit worker admission.", "Use --atomic --verify --workers for the supported push worker contract.")
	}
	if req.Workers {
		r.quota("worker_replica_overlap", 2*workers, api.MustLimitsFor(account.Plan).WorkerReplicasMax, "Minimum worker replicas required by retained and candidate pools; other running pools still consume quota.", "Reduce worker overlap or select a plan with enough worker replicas.")
	}
	if baseline.ID == "" {
		return
	}
	minimum := time.Duration(0)
	if req.Verify {
		minimum = api.ProjectReleaseVerificationMinRetention
	}
	if req.Stabilize {
		minimum = api.ProjectReleaseStabilizationMinRetention
	}
	if time.Duration(baseline.TTLSeconds)*time.Second < minimum {
		r.add("rollback_retention_insufficient", "blocked", "", "Baseline revision retention is shorter than the requested verification window.", "Increase revision retention before creating the baseline release.")
	} else {
		r.add("rollback_retention_available", "passed", "", "Baseline retention meets the requested release window.", "")
	}
	if req.Verify {
		if _, problem := VerificationPolicy(ctx, store, account.ID, baseline); problem != nil {
			status := "blocked"
			if problem.Status >= 500 {
				status = "unknown"
			}
			r.add("verification_policy_unavailable", status, "", "Critical route verification policy does not satisfy the release contract.", "Configure GET/HEAD route-health probes and sufficient retention; retry unavailable policy reads.")
		} else {
			r.add("verification_policy_available", "passed", "", "Critical route verification policy satisfies the release contract.", "")
		}
	}
	if req.Workers {
		reader, ok := store.(state.ProjectReleaseWorkerStore)
		if !ok {
			r.add("worker_consumer_unavailable", "unknown", "", "Worker consumer metadata is unavailable.", "Use a server with worker release support.")
		} else if worker, err := reader.PreviewProjectReleaseWorker(ctx, account.ID, project.ID, baseline.ID); err != nil {
			if inspectionUnknown(err) {
				r.add("worker_consumer_unavailable", "unknown", "", "Worker consumer metadata could not be read.", "Retry when worker consumer metadata is available.")
			} else {
				r.add("worker_consumer_incompatible", "blocked", "", "Atomic source deployment requires one unchanged production push worker and its owned consumer.", "Keep one enabled production push binding and owned consumer; multiple workers currently require environment promotion.")
			}
		} else {
			r.identities["worker_policy"] = api.ProjectReleaseWorkerPolicyID(worker)
			r.add("worker_consumer_compatible", "passed", worker.Slug, "Production push worker consumer intent is compatible.", "")
		}
	}
	if len(req.ReleaseConfig) > 0 || req.AddServices {
		reader, ok := store.(state.ProjectReleaseConfigurationStore)
		if !ok {
			r.add("release_config_unavailable", "unknown", "", "Paired release configuration cannot be inspected.", "Use a server with paired configuration support.")
		} else if capture, err := reader.PreviewProjectReleaseConfiguration(ctx, account.ID, project.ID, baseline.ID, req.ReleaseConfig); err != nil {
			if inspectionUnknown(err) {
				r.add("release_config_unavailable", "unknown", "", "Paired configuration metadata could not be read.", "Retry when release configuration metadata is available.")
			} else {
				r.add("release_config_incompatible", "blocked", "", "The proposed configuration conflicts with the retained release or secret references.", "Refresh workload names, staged secret references and configuration against the current baseline.")
			}
		} else {
			r.identities["release_config"] = capture
			if req.AddServices && capture != nil {
				r.add("service_addition_config_unsupported", "blocked", "", "Service additions cannot inherit pinned release configuration yet.", "Use an HTTP project without release configuration snapshots for this first version.")
			}
			if capture != nil && capture.Secrets != nil && (!config.SecretsEnabled || req.Workers || req.Migrate) {
				r.add("secret_release_unqualified", "blocked", "", "Secret snapshots require qualified HTTP-only verified release admission.", "Use the qualified HTTP-only secret release mode.")
			}
		}
	}
}

func inspectionUnknown(err error) bool {
	return !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrInvalidArgument) && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrReleaseConfigurationTransferUnsupported)
}
