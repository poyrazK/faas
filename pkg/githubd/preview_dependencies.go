package githubd

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewDependencyParents selects the bound workload's transitive
// depends_on closure. Under the project service policy the bound preview may
// call any service of its project without declaring it, so every callable
// sibling at the PR head joins the preview too (ADR-20261025093331); workers,
// jobs and scheduled workloads join only as declared dependencies. Otherwise
// an unrelated service never gets a PR preview merely because the scanner
// discovered it.
func (s *Service) previewDependencyParents(ctx context.Context, parent state.App, scan reposcan.Result, prNumber int) ([]state.App, error) {
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
	production, err := s.Reconcile.Store.AppsForProject(ctx, parent.AccountID, parent.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("githubd: list production dependency apps: %w", err)
	}
	byWorkload := make(map[string]state.App, len(production))
	for _, app := range production {
		byWorkload[strings.ToLower(app.WorkloadName)] = app
	}
	if previewCallsWholeProject(parent, workloads[root]) {
		for key, workload := range workloads {
			if callablePreviewSibling(workload, byWorkload[key]) {
				if err := visit(key); err != nil {
					return nil, err
				}
			}
		}
	}
	closure := make([]reposcan.Workload, 0, len(selected))
	for _, workload := range selected {
		closure = append(closure, workload)
	}
	order, err := reposcan.DependencyOrder(closure, scan.Managed)
	if err != nil {
		return nil, fmt.Errorf("githubd: PR dependency graph: %w", err)
	}
	parents := make([]state.App, 0, len(order)-1)
	for _, name := range order {
		key := strings.ToLower(name)
		if key == root {
			continue
		}
		app, found := byWorkload[key]
		if !found {
			// A PR may introduce a new dependency before it exists in
			// production. Start with a clean preview template, without
			// copying the root app's env or credentials.
			if !api.ValidAppSlug(name) {
				return nil, fmt.Errorf("githubd: PR dependency %q is not a valid app slug", name)
			}
			app = state.App{
				AccountID: parent.AccountID, ProjectID: parent.ProjectID,
				Slug: name, WorkloadName: name, Type: state.AppTypeApp,
				RAMMB: 128, MaxConcurrency: 1, CPUMillicores: api.DefaultAppCPUMillicores,
				Status: state.AppActive,
			}
		}
		if app.Status != state.AppActive {
			return nil, fmt.Errorf("githubd: PR dependency %q has no active production app", name)
		}
		// A production app can appear after the first PR preview, possibly
		// with a different slug. Reuse the already-reserved preview identity
		// so synchronize does not consume a second slot or strand the old row.
		existing, err := s.Reconcile.Store.PreviewAppByProjectWorkload(ctx,
			parent.AccountID, parent.ProjectID, prNumber, name)
		if err == nil {
			expected, slugErr := previewSlug(existing.PreviewOfSlug, prNumber)
			if slugErr != nil || existing.Slug != expected {
				return nil, fmt.Errorf("githubd: PR dependency %q has inconsistent preview identity: %w", name, state.ErrConflict)
			}
			app.Slug = existing.PreviewOfSlug
		} else if !errors.Is(err, state.ErrNotFound) {
			return nil, fmt.Errorf("githubd: resolve existing PR dependency %q: %w", name, err)
		}
		parents = append(parents, app)
	}
	return parents, nil
}

// previewCallsWholeProject reports whether the bound preview runs under the
// project service policy: the PR head's declaration, else the policy the
// production app already has, as reconcile applies it to the preview.
func previewCallsWholeProject(parent state.App, root reposcan.Workload) bool {
	policy := parent.Manifest.EffectiveServiceBindingPolicy()
	if root.ServiceBindingPolicy != "" {
		policy = api.ServiceBindingPolicy(root.ServiceBindingPolicy).Effective()
	}
	return policy == api.ServiceBindingPolicyProject
}

// callablePreviewSibling reports whether a project sibling serves calls.
// A worker, job or scheduled workload consumes production queues or runs on
// a timer; it joins a preview only when a service declares it in depends_on.
func callablePreviewSibling(workload reposcan.Workload, production state.App) bool {
	if workload.Schedule != "" || len(workload.Schedules) > 0 ||
		workload.Class == reposcan.ClassJob || workload.Class == reposcan.ClassWorker {
		return false
	}
	return production.WorkloadClass != state.WorkloadClassJob && production.WorkloadClass != state.WorkloadClassWorker
}

// makePRDependencyPreview builds the desired sibling row. The caller reserves
// the root and all siblings together before refreshing leases or enqueueing.
func previewManifest(parent state.AppManifest) state.AppManifest {
	// A PR head executes code that has not been promoted. App environment
	// values can contain production credentials, so only copy non-env runtime
	// configuration. Reconciliation will inject the PR's service bindings.
	parent.Env = nil
	return parent
}

// withParentAccessGates copies the parent's access and egress gates onto a PR
// preview. A preview answers on a guessable pr-<n>-<slug> host and runs the PR
// head, so the preview of an IP-allowlisted, password-protected, signed-only
// or egress-restricted app must keep those gates; the developer preview API
// already copies them.
func withParentAccessGates(preview, parent state.App) state.App {
	preview.RequireAuthn = parent.RequireAuthn
	preview.PublicAuthMode = parent.PublicAuthMode
	preview.PublicAuthBasicSealed = append([]byte(nil), parent.PublicAuthBasicSealed...)
	preview.PublicAuthIPAllowlist = append([]netip.Prefix(nil), parent.PublicAuthIPAllowlist...)
	preview.ConsumerAuthMode = parent.ConsumerAuthMode
	preview.RequireSigned = parent.RequireSigned
	preview.EgressAllowlist = append([]netip.Prefix(nil), parent.EgressAllowlist...)
	return preview
}

func makePRDependencyPreview(parent state.App, prNumber int, expiresAt time.Time, policy state.GitHubDeployPolicy) (state.App, error) {
	slug, err := previewSlug(parent.Slug, prNumber)
	if err != nil {
		return state.App{}, err
	}
	return withParentAccessGates(applyGitHubRootPolicy(state.App{
		AccountID: parent.AccountID, OrgID: parent.OrgID, Slug: slug, Visibility: parent.Visibility, Type: parent.Type,
		Runtime: parent.Runtime, RAMMB: parent.RAMMB, MaxConcurrency: parent.MaxConcurrency,
		IdleTimeoutS: parent.IdleTimeoutS, ProjectID: parent.ProjectID,
		RootDir: parent.RootDir, WorkloadName: parent.WorkloadName,
		WorkloadClass: parent.WorkloadClass, StartCommand: parent.StartCommand,
		Manifest: previewManifest(parent.Manifest), AppProtocol: parent.AppProtocol, Status: state.AppActive,
		PreviewOfSlug: parent.Slug, PreviewPrNumber: prNumber,
		PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiresAt,
	}, policy), parent), nil
}

func (s *Service) applyPRHeadWorkload(ctx context.Context, preview state.App, workload reposcan.Workload, available map[string]struct{}, policy state.GitHubDeployPolicy) (state.App, error) {
	// Also clear environment values on an existing preview. Earlier preview
	// rows inherited parent values; a synchronize event must not retain them.
	preview.Manifest = previewManifest(preview.Manifest)
	desired := reconcile.ApplyScannedWorkloadToApp(preview, workload, available)
	desired = applyGitHubRootPolicy(desired, policy)
	params := state.UpdateAppParams{
		RootDir: &desired.RootDir, WorkloadName: &desired.WorkloadName,
		WorkloadClass: &desired.WorkloadClass, StartCommand: &desired.StartCommand,
		Manifest: &desired.Manifest,
	}
	// A preview runs with the resources its head declares (ADR-20261023120000).
	if !workload.Resources.IsZero() {
		params.RAMMB, params.CPUMillicores, params.MaxConcurrency = &desired.RAMMB, &desired.CPUMillicores, &desired.MaxConcurrency
	}
	return s.Reconcile.Store.UpdateApp(ctx, preview.ID, params)
}

// closePRDependencies advances every sibling in the same project and PR to
// the janitor's closed state with the same grace deadline. The bound preview
// may already have expired, so this is deliberately independent of its lookup path.
func (s *Service) closePRDependencies(ctx context.Context, parent state.App, prNumber int, expiresAt time.Time) error {
	if parent.ProjectID == "" {
		return nil // a legacy binding has no project-scoped sibling set
	}
	previews, err := s.Reconcile.Store.ListPreviewsForAccount(ctx, parent.AccountID)
	if err != nil {
		return err
	}
	for _, preview := range previews {
		if preview.ProjectID != parent.ProjectID || preview.PreviewPrNumber != prNumber || preview.PreviewOfSlug == parent.Slug ||
			preview.PreviewPrState == state.PreviewPrStateStale || preview.PreviewPrState == state.PreviewPrStateTearingDown ||
			preview.PreviewPrState == state.PreviewPrStateTornDown {
			continue
		}
		if _, err := s.closePRPreview(ctx, preview.ID, expiresAt); err != nil {
			return err
		}
	}
	return nil
}
