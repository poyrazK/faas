package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/markers"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// Bounds local source review, not deployment or Task execution quotas.
const mcpTaskReviewSourceMaxBytes int64 = 512 << 20
const mcpTaskReviewSourceMaxFiles = 200_000

func reviewMCPTaskRelease(path string) (mcphosting.TaskReleaseReview, error) {
	var out mcphosting.TaskReleaseReview
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return out, errors.New("Task release plan must be a regular file of at most 64 KiB")
	}
	p, err := readMCPNativePlan(path)
	if err != nil {
		return out, err
	}
	if !api.ValidAppSlug(p.ObserverMetricApp) {
		return out, errors.New("observer_metric_app must be a valid app slug")
	}
	if err := requireMCPNativeNodeWorker(p.WorkerPath); err != nil {
		return out, err
	}
	web, err := reviewMCPTaskSource(p.WebPath)
	if err != nil {
		return out, err
	}
	worker, err := reviewMCPTaskSource(p.WorkerPath)
	if err != nil {
		return out, err
	}
	family, err := markers.DetectFromFS(os.DirFS(p.WorkerPath))
	if err != nil || family != markers.FrameworkNode {
		return out, errors.New("Task release review requires native Node worker source markers")
	}
	if len(web.Auth.ToolScopes) == 0 || len(web.Auth.ToolScopes) > api.ToolPublicationMaxTools {
		return out, errors.New("Task tool review requires an explicit, bounded tool_scopes policy")
	}
	roles, err := loadMCPReleasePolicyWithTokens(p.ReleasePolicy, false)
	if err != nil {
		return out, err
	}
	fingerprint, err := mcpNativeFingerprint(p)
	if err != nil {
		return out, err
	}
	out = mcphosting.TaskReleaseReview{Version: 1, Status: "review_only", Fingerprint: fingerprint,
		Protocol: mcphosting.ProtocolVersion, WebApp: p.WebApp, WorkerApp: p.WorkerApp, WorkerRuntime: "node",
		ObserverApp: p.ObserverApp, ObserverMetricApp: p.ObserverMetricApp, PreviousWorkerApps: p.PreviousWorkerApps,
		Endpoint: web.Endpoint, AccessMode: web.Auth.Mode, TimeoutSeconds: p.TimeoutSeconds,
		WebLimits: mcphosting.ReviewTaskLimits(*web.Tasks), WorkerLimits: mcphosting.ReviewTaskLimits(*worker.Tasks),
		ToolScopes: web.Auth.ToolScopes, ReadinessRoles: []string{}, MigrationsRequired: true,
		RequiredSetup: []string{"runtime_postgres_binding", "separate_schema_owner_migration_binding",
			"shared_namespace_and_retained_payload_keys", "read_only_observer_binding", "worker_scoped_metrics_tokens",
			"live_worker_and_observer_readiness", "candidate_catalog_and_release_policy_verification"}}
	for _, role := range roles {
		out.ReadinessRoles = append(out.ReadinessRoles, role.Name)
	}
	sort.Strings(out.ReadinessRoles)
	return out, nil
}

func reviewMCPTaskSource(dir string) (mcphosting.Config, error) {
	if filepath.Base(dir) == "node_modules" || filepath.Base(dir) == ".git" || filepath.Base(dir) == ".gregale" {
		return mcphosting.Config{}, errors.New("Task source must not be a release cache directory")
	}
	var size int64
	var files int
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" || entry.Name() == ".git" || entry.Name() == ".gregale" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files++
		size += info.Size()
		if !info.Mode().IsRegular() || files > mcpTaskReviewSourceMaxFiles || size > mcpTaskReviewSourceMaxBytes {
			return errors.New("Task review sources require bounded regular files without symbolic links")
		}
		return nil
	})
	if err != nil {
		return mcphosting.Config{}, err
	}
	cfg, err := mcphosting.Load(dir)
	if err != nil {
		return cfg, err
	}
	if cfg.Tasks == nil || cfg.Tasks.Enabled == nil || !*cfg.Tasks.Enabled {
		return cfg, errors.New("Task release review requires enabled Tasks in both web and worker sources")
	}
	return cfg, nil
}
