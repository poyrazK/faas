package githubd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewDependencyParents selects only the bound workload's transitive
// depends_on closure. An unrelated service in the same repository never gets
// a PR preview merely because it was discovered by the scanner.
func (s *Service) previewDependencyParents(ctx context.Context, parent state.App, scan reposcan.Result) ([]state.App, error) {
	if parent.ProjectID == "" {
		return nil, nil // legacy single-app bindings have no project graph
	}
	workloads := make(map[string]reposcan.Workload, len(scan.Workloads))
	managed := make(map[string]bool, len(scan.Managed))
	for _, service := range scan.Managed {
		managed[strings.ToLower(service.Name)] = true
	}
	for _, workload := range scan.Workloads {
		key := strings.ToLower(workload.Name)
		if _, duplicate := workloads[key]; duplicate {
			return nil, fmt.Errorf("githubd: ambiguous PR workload %q", workload.Name)
		}
		workloads[key] = workload
	}
	root := strings.ToLower(parent.WorkloadName)
	if _, found := workloads[root]; !found {
		return nil, fmt.Errorf("githubd: bound workload %q is absent from PR source", parent.WorkloadName)
	}
	selected := make(map[string]reposcan.Workload)
	var visit func(string) error
	visit = func(name string) error {
		key := strings.ToLower(strings.TrimSpace(name))
		if _, done := selected[key]; done {
			return nil
		}
		workload, found := workloads[key]
		if !found {
			if managed[key] {
				return nil // external managed services are not app previews
			}
			return fmt.Errorf("githubd: PR workload %q depends on missing service %q", parent.WorkloadName, name)
		}
		selected[key] = workload
		for _, dependency := range workload.DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return nil, err
	}
	closure := make([]reposcan.Workload, 0, len(selected))
	for _, workload := range selected {
		closure = append(closure, workload)
	}
	order, err := reposcan.DependencyOrder(closure, scan.Managed)
	if err != nil {
		return nil, fmt.Errorf("githubd: PR dependency graph: %w", err)
	}
	production, err := s.Reconcile.Store.AppsForProject(ctx, parent.AccountID, parent.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("githubd: list production dependency apps: %w", err)
	}
	byWorkload := make(map[string]state.App, len(production))
	for _, app := range production {
		byWorkload[strings.ToLower(app.WorkloadName)] = app
	}
	parents := make([]state.App, 0, len(order)-1)
	for _, name := range order {
		key := strings.ToLower(name)
		if key == root {
			continue
		}
		app, found := byWorkload[key]
		if !found || app.Status != state.AppActive {
			return nil, fmt.Errorf("githubd: PR dependency %q has no active production app", name)
		}
		parents = append(parents, app)
	}
	return parents, nil
}

// makePRDependencyPreview builds the desired sibling row. The caller reserves
// the root and all siblings together before refreshing leases or enqueueing.
func makePRDependencyPreview(parent state.App, prNumber int, expiresAt time.Time, policy state.GitHubDeployPolicy) (state.App, error) {
	slug, err := previewSlug(parent.Slug, prNumber)
	if err != nil {
		return state.App{}, err
	}
	return applyGitHubRootPolicy(state.App{
		AccountID: parent.AccountID, OrgID: parent.OrgID, Slug: slug, Type: parent.Type,
		Runtime: parent.Runtime, RAMMB: parent.RAMMB, MaxConcurrency: parent.MaxConcurrency,
		IdleTimeoutS: parent.IdleTimeoutS, ProjectID: parent.ProjectID,
		RootDir: parent.RootDir, WorkloadName: parent.WorkloadName,
		WorkloadClass: parent.WorkloadClass, StartCommand: parent.StartCommand,
		Manifest: parent.Manifest, AppProtocol: parent.AppProtocol, Status: state.AppActive,
		PreviewOfSlug: parent.Slug, PreviewPrNumber: prNumber,
		PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiresAt,
	}, policy), nil
}

func (s *Service) applyPRHeadWorkload(ctx context.Context, preview state.App, workload reposcan.Workload, available map[string]struct{}, policy state.GitHubDeployPolicy) (state.App, error) {
	desired := reconcile.ApplyScannedWorkloadToApp(preview, workload, available)
	desired = applyGitHubRootPolicy(desired, policy)
	return s.Reconcile.Store.UpdateApp(ctx, preview.ID, state.UpdateAppParams{
		RootDir: &desired.RootDir, WorkloadName: &desired.WorkloadName,
		WorkloadClass: &desired.WorkloadClass, StartCommand: &desired.StartCommand,
		Manifest: &desired.Manifest,
	})
}

// closePRDependencies advances every sibling in the same project and PR to
// the janitor's closed state. The bound preview may already have expired, so
// this is deliberately independent of its lookup path.
func (s *Service) closePRDependencies(ctx context.Context, parent state.App, prNumber int) error {
	if parent.ProjectID == "" {
		return nil // a legacy binding has no project-scoped sibling set
	}
	previews, err := s.Reconcile.Store.ListPreviewsForAccount(ctx, parent.AccountID)
	if err != nil {
		return err
	}
	for _, preview := range previews {
		if preview.ProjectID != parent.ProjectID || preview.PreviewPrNumber != prNumber || preview.PreviewOfSlug == parent.Slug {
			continue
		}
		if err := s.stampPreviewPrState(ctx, preview.ID, state.PreviewPrStateClosed); err != nil {
			return err
		}
	}
	return nil
}
