package state

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Environment branches (ADR-20261031193635). A branch is an ordinary project
// environment with a parent and a branch point. Creating one copies the
// parent's configuration in one transaction; each parent workload then gets a
// deployment that reuses the parent's built artifact and activates without a
// boot, so its first request cold boots.

const (
	EnvironmentBranchEgressLive      = "live"
	EnvironmentBranchEgressContained = "contained"
	EnvironmentBranchEgressSimulated = "simulated"

	// EnvironmentBranchDeploymentReason marks deployments a branch created
	// from its parent's artifacts.
	EnvironmentBranchDeploymentReason = "environment-branch"
	// EnvironmentBranchActivationReason is the deployment_ready
	// no_snapshot_reason schedd sends for such a deployment: it goes live
	// without a boot and its first request cold boots.
	EnvironmentBranchActivationReason = "environment_branch"
)

var (
	ErrProjectEnvironmentBranchDepth    = fmt.Errorf("%w: environment branch depth limit reached", ErrConflict)
	ErrProjectEnvironmentBranchLimit    = fmt.Errorf("%w: environment branch limit reached", ErrConflict)
	ErrProjectEnvironmentBranchChildren = fmt.Errorf("%w: environment has branches", ErrConflict)
	ErrProjectEnvironmentNotBranch      = fmt.Errorf("%w: environment is not a branch", ErrNotFound)
)

// ProjectEnvironmentBranch is one branch row joined with its environment and
// parent slugs.
type ProjectEnvironmentBranch struct {
	EnvironmentID       string
	AccountID           string
	ProjectID           string
	Slug                string
	ParentEnvironmentID string
	ParentSlug          string
	BranchPoint         time.Time
	ExpiresAt           time.Time
	EgressMode          string
	Depth               int
	CreatedAt           time.Time
}

// ProjectEnvironmentBranchCreate asks for a new branch of ParentSlug.
type ProjectEnvironmentBranchCreate struct {
	AccountID  string
	ProjectID  string
	ParentSlug string
	Slug       string
	// BranchPoint is the time data layers read the parent as of. Zero means
	// the creating transaction's time.
	BranchPoint time.Time
	ExpiresAt   time.Time
	EgressMode  string
	// MaxDepth caps how deep a branch of a branch may go (>= 1). MaxBranches
	// caps the account's live branches; zero means no cap.
	MaxDepth    int
	MaxBranches int
}

func (r ProjectEnvironmentBranchCreate) validate() error {
	if r.AccountID == "" || r.ProjectID == "" || r.Slug == r.ParentSlug ||
		!api.ValidProjectEnvironmentSlug(r.Slug) || !api.ValidProjectEnvironmentSlug(r.ParentSlug) ||
		r.Slug == "production" || r.Slug == DefaultEnvScope || r.ExpiresAt.IsZero() || r.MaxDepth < 1 || r.MaxBranches < 0 {
		return ErrInvalidArgument
	}
	if !r.BranchPoint.IsZero() && !r.BranchPoint.Before(r.ExpiresAt) {
		return ErrInvalidArgument
	}
	switch r.EgressMode {
	case EnvironmentBranchEgressLive, EnvironmentBranchEgressContained, EnvironmentBranchEgressSimulated:
	default:
		return ErrInvalidArgument
	}
	return nil
}

// ProjectEnvironmentBranchWorkload is one parent workload a branch serves:
// the parent's selected live deployment for the app.
type ProjectEnvironmentBranchWorkload struct {
	AppID              string
	WorkloadSlug       string
	SourceDeploymentID string
}

// ProjectEnvironmentBranchStore creates, reads and retires branches.
type ProjectEnvironmentBranchStore interface {
	// CreateProjectEnvironmentBranch copies the parent's configuration into a
	// new environment and records the branch, atomically. It refuses a parent
	// with managed data bindings (*ProjectEnvironmentCloneManagedBindingsError).
	CreateProjectEnvironmentBranch(context.Context, ProjectEnvironmentBranchCreate, api.Limits) (ProjectEnvironment, ProjectEnvironmentBranch, ProjectEnvironmentCloneResult, error)
	ProjectEnvironmentBranchBySlug(ctx context.Context, accountID, projectID, slug string) (ProjectEnvironmentBranch, error)
	ListProjectEnvironmentBranches(ctx context.Context, accountID, projectID string) ([]ProjectEnvironmentBranch, error)
	// ExpiredProjectEnvironmentBranches lists branches past expiry that have
	// no branches of their own, deepest first.
	ExpiredProjectEnvironmentBranches(ctx context.Context, now time.Time, limit int) ([]ProjectEnvironmentBranch, error)
	// ProjectEnvironmentBranchWorkloads lists the parent workloads with a
	// live deployment the branch can reuse.
	ProjectEnvironmentBranchWorkloads(ctx context.Context, accountID, projectID, slug string) ([]ProjectEnvironmentBranchWorkload, error)
	// CreateDeploymentForEnvironmentBranch creates (or returns) the branch's
	// deployment of appID from the parent's live artifact, in snapshotting
	// state awaiting its no-boot activation.
	CreateDeploymentForEnvironmentBranch(ctx context.Context, accountID, projectID, slug, appID string) (Deployment, error)
	// DeleteProjectEnvironmentBranch retires the branch's deployments and
	// deletes the environment in one transaction.
	DeleteProjectEnvironmentBranch(ctx context.Context, accountID, projectID, slug string) error
}

// EnvironmentBranchDeploymentReader tells schedd and imaged whether a
// deployment was created by a branch from its parent's artifact, and so
// activates without a boot.
type EnvironmentBranchDeploymentReader interface {
	EnvironmentBranchDeployment(ctx context.Context, deploymentID string) (bool, error)
}

type projectEnvironmentBranchDeploymentInput struct {
	AccountID, ProjectID, Slug string
}

type projectEnvironmentBranchDeployment struct {
	DeploymentID       string
	EnvironmentID      string
	AppID              string
	SourceDeploymentID string
	CreatedAt          time.Time
}

// branchDeploymentFromArtifact builds the branch's stable deployment of a
// parent artifact. It is served at 100% of the branch once activated and
// keeps no warm floor of its own.
func branchDeploymentFromArtifact(a projectCloneArtifact, slug string, input *projectEnvironmentBranchDeploymentInput) Deployment {
	d := a.deployment("", slug, ProjectEnvironmentWorkloadSettings{})
	d.Status = DeploySnapshotting
	d.TrafficPercent, d.TrafficPercentExplicit = 100, false
	d.MinInstances = 0
	d.Reason = EnvironmentBranchDeploymentReason
	d.branchInput = input
	return d
}

// MemStore

func (m *MemStore) CreateProjectEnvironmentBranch(ctx context.Context, req ProjectEnvironmentBranchCreate, limits api.Limits) (ProjectEnvironment, ProjectEnvironmentBranch, ProjectEnvironmentCloneResult, error) {
	if err := req.validate(); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentBranch{}, ProjectEnvironmentCloneResult{}, err
	}
	var branch ProjectEnvironmentBranch
	clone := ProjectEnvironmentClone{AccountID: req.AccountID, ProjectID: req.ProjectID, SourceSlug: req.ParentSlug, TargetSlug: req.Slug,
		branch: &req, branchOut: &branch}
	created, result, err := m.CloneProjectEnvironment(ctx, clone, limits)
	if err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentBranch{}, ProjectEnvironmentCloneResult{}, err
	}
	return created, branch, result, nil
}

// prepareProjectEnvironmentBranchLocked checks depth and the account cap
// before the clone writes anything.
func (m *MemStore) prepareProjectEnvironmentBranchLocked(req ProjectEnvironmentBranchCreate, parent ProjectEnvironment, now time.Time) (ProjectEnvironmentBranch, error) {
	depth := 1
	if parentBranch, ok := m.projectEnvironmentBranches[parent.ID]; ok {
		depth = parentBranch.Depth + 1
	}
	if depth > req.MaxDepth {
		return ProjectEnvironmentBranch{}, ErrProjectEnvironmentBranchDepth
	}
	if req.MaxBranches > 0 {
		count := 0
		for _, b := range m.projectEnvironmentBranches {
			if b.AccountID == req.AccountID {
				count++
			}
		}
		if count >= req.MaxBranches {
			return ProjectEnvironmentBranch{}, ErrProjectEnvironmentBranchLimit
		}
	}
	if !req.ExpiresAt.After(now) {
		return ProjectEnvironmentBranch{}, ErrInvalidArgument
	}
	point := req.BranchPoint
	if point.IsZero() {
		point = now
	}
	return ProjectEnvironmentBranch{AccountID: req.AccountID, ProjectID: req.ProjectID,
		ParentEnvironmentID: parent.ID, ParentSlug: parent.Slug, BranchPoint: point.UTC(),
		ExpiresAt: req.ExpiresAt.UTC(), EgressMode: req.EgressMode, Depth: depth, CreatedAt: now}, nil
}

func (m *MemStore) projectEnvironmentHasBranchesLocked(environmentID string) bool {
	for _, b := range m.projectEnvironmentBranches {
		if b.ParentEnvironmentID == environmentID {
			return true
		}
	}
	return false
}

func (m *MemStore) deleteProjectEnvironmentBranchLocked(environmentID string) {
	delete(m.projectEnvironmentBranches, environmentID)
	for id, row := range m.projectEnvironmentBranchDeployments {
		if row.EnvironmentID == environmentID {
			delete(m.projectEnvironmentBranchDeployments, id)
		}
	}
}

func (m *MemStore) projectEnvironmentBranchBySlugLocked(accountID, projectID, slug string) (ProjectEnvironmentBranch, error) {
	environment, err := m.projectEnvironmentBySlugLocked(projectID, slug)
	if err != nil {
		return ProjectEnvironmentBranch{}, err
	}
	if environment.AccountID != accountID {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	branch, ok := m.projectEnvironmentBranches[environment.ID]
	if !ok {
		return ProjectEnvironmentBranch{}, ErrProjectEnvironmentNotBranch
	}
	return m.branchViewLocked(branch), nil
}

// branchViewLocked refreshes the slugs, which a stored row only snapshots.
func (m *MemStore) branchViewLocked(b ProjectEnvironmentBranch) ProjectEnvironmentBranch {
	if env, ok := m.projectEnvironments[b.EnvironmentID]; ok {
		b.Slug = env.Slug
	}
	if parent, ok := m.projectEnvironments[b.ParentEnvironmentID]; ok {
		b.ParentSlug = parent.Slug
	}
	return b
}

func (m *MemStore) ProjectEnvironmentBranchBySlug(_ context.Context, accountID, projectID, slug string) (ProjectEnvironmentBranch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectEnvironmentBranchBySlugLocked(accountID, projectID, slug)
}

func (m *MemStore) ListProjectEnvironmentBranches(_ context.Context, accountID, projectID string) ([]ProjectEnvironmentBranch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ProjectEnvironmentBranch
	for _, b := range m.projectEnvironmentBranches {
		if b.AccountID == accountID && b.ProjectID == projectID {
			out = append(out, m.branchViewLocked(b))
		}
	}
	sortProjectEnvironmentBranches(out)
	return out, nil
}

func sortProjectEnvironmentBranches(branches []ProjectEnvironmentBranch) {
	sort.Slice(branches, func(i, j int) bool {
		if !branches[i].CreatedAt.Equal(branches[j].CreatedAt) {
			return branches[i].CreatedAt.Before(branches[j].CreatedAt)
		}
		return branches[i].EnvironmentID < branches[j].EnvironmentID
	})
}

func (m *MemStore) ExpiredProjectEnvironmentBranches(_ context.Context, now time.Time, limit int) ([]ProjectEnvironmentBranch, error) {
	if limit <= 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ProjectEnvironmentBranch
	for _, b := range m.projectEnvironmentBranches {
		if !b.ExpiresAt.After(now) && !m.projectEnvironmentHasBranchesLocked(b.EnvironmentID) {
			out = append(out, m.branchViewLocked(b))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth > out[j].Depth
		}
		if !out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
			return out[i].ExpiresAt.Before(out[j].ExpiresAt)
		}
		return out[i].EnvironmentID < out[j].EnvironmentID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ProjectEnvironmentBranchWorkloads(_ context.Context, accountID, projectID, slug string) ([]ProjectEnvironmentBranchWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	branch, err := m.projectEnvironmentBranchBySlugLocked(accountID, projectID, slug)
	if err != nil {
		return nil, err
	}
	apps := m.projectCloneAppsLocked(projectID)
	scopes, err := m.projectCloneValueScopesLocked(apps, ProjectEnvironmentClone{ProjectID: projectID, SourceSlug: branch.ParentSlug})
	if err != nil {
		return nil, err
	}
	var out []ProjectEnvironmentBranchWorkload
	for appID, workloadSlug := range apps {
		source := m.branchSourceDeploymentLocked(projectID, branch.ParentSlug, appID, scopes[appID])
		if source.ID == "" {
			continue
		}
		out = append(out, ProjectEnvironmentBranchWorkload{AppID: appID, WorkloadSlug: workloadSlug, SourceDeploymentID: source.ID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkloadSlug < out[j].WorkloadSlug })
	return out, nil
}

// branchSourceDeploymentLocked mirrors ReadProjectEnvironmentCloneSelectedArtifact:
// the active release set's member, else the newest live serving deployment.
func (m *MemStore) branchSourceDeploymentLocked(projectID, parentSlug, appID, scope string) Deployment {
	if release := m.activeProjectReleaseSets[releaseKey(projectID, parentSlug)]; release != "" {
		d := m.deployments[releaseMemberForApp(m.projectReleaseSets[release], appID)]
		if d.AppID != appID || d.Status != DeployLive {
			return Deployment{}
		}
		return d
	}
	var source Deployment
	for _, d := range m.deployments {
		if d.AppID == appID && d.Status == DeployLive && normalizedDeploymentScope(d.Scope) == scope && d.TrafficPercent > 0 &&
			(source.ID == "" || d.CreatedAt.After(source.CreatedAt) || d.CreatedAt.Equal(source.CreatedAt) && d.ID > source.ID) {
			source = d
		}
	}
	return source
}

func (m *MemStore) CreateDeploymentForEnvironmentBranch(ctx context.Context, accountID, projectID, slug, appID string) (Deployment, error) {
	input := &projectEnvironmentBranchDeploymentInput{AccountID: accountID, ProjectID: projectID, Slug: slug}
	created, _, err := m.createDeployment(ctx, Deployment{AppID: appID, Scope: slug, branchInput: input}, nil, nil)
	return created, err
}

// prepareBranchDeploymentLocked resolves the parent artifact for d.AppID. A
// second call for the same app returns the deployment already created.
func (m *MemStore) prepareBranchDeploymentLocked(d Deployment) (Deployment, *Deployment, string, error) {
	input := d.branchInput
	branch, err := m.projectEnvironmentBranchBySlugLocked(input.AccountID, input.ProjectID, input.Slug)
	if err != nil {
		return Deployment{}, nil, "", err
	}
	app, ok := m.apps[d.AppID]
	if !ok || app.ProjectID != input.ProjectID || app.AccountID != input.AccountID || app.Status == AppDeleted || app.PreviewOfSlug != "" {
		return Deployment{}, nil, "", ErrNotFound
	}
	for _, row := range m.projectEnvironmentBranchDeployments {
		if row.EnvironmentID == branch.EnvironmentID && row.AppID == d.AppID {
			existing := m.deployments[row.DeploymentID]
			return Deployment{}, &existing, "", nil
		}
	}
	scopes, err := m.projectCloneValueScopesLocked(map[string]string{app.ID: app.Slug}, ProjectEnvironmentClone{ProjectID: input.ProjectID, SourceSlug: branch.ParentSlug})
	if err != nil {
		return Deployment{}, nil, "", err
	}
	source := m.branchSourceDeploymentLocked(input.ProjectID, branch.ParentSlug, app.ID, scopes[app.ID])
	if source.ID == "" {
		return Deployment{}, nil, "", ErrNotFound
	}
	if err := m.requireLayerArtifactsRetainedLocked(m.deploymentLayerKeysLocked(source)); err != nil {
		return Deployment{}, nil, "", err
	}
	return branchDeploymentFromArtifact(normalizeProjectCloneArtifact(projectCloneArtifactFromDeployment(source)), branch.Slug, input), nil, source.ID, nil
}

func (m *MemStore) attachBranchDeploymentLocked(input projectEnvironmentBranchDeploymentInput, sourceID string, created Deployment) {
	branch, err := m.projectEnvironmentBranchBySlugLocked(input.AccountID, input.ProjectID, input.Slug)
	if err != nil {
		return // validated under the same lock in prepareBranchDeploymentLocked
	}
	now := time.Now().UTC()
	var layers []DeploymentSidecarLayer
	for _, layer := range m.deploymentSidecarLayers {
		if layer.DeploymentID == sourceID {
			layers = append(layers, layer)
		}
	}
	for _, layer := range layers {
		layer.DeploymentID = created.ID
		layer.CreatedAt, layer.UpdatedAt = now, now
		m.deploymentSidecarLayers[created.ID+"\x00"+layer.SidecarName] = layer
	}
	prefix := sourceID + "\x00"
	signals := map[string]string{}
	for key, signal := range m.sidecarSecretReloadSignals {
		if strings.HasPrefix(key, prefix) {
			signals[strings.TrimPrefix(key, prefix)] = signal
		}
	}
	for name, signal := range signals {
		m.sidecarSecretReloadSignals[created.ID+"\x00"+name] = signal
	}
	m.projectEnvironmentBranchDeployments[created.ID] = projectEnvironmentBranchDeployment{
		DeploymentID: created.ID, EnvironmentID: branch.EnvironmentID, AppID: created.AppID,
		SourceDeploymentID: sourceID, CreatedAt: now,
	}
}

func (m *MemStore) EnvironmentBranchDeployment(_ context.Context, deploymentID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.projectEnvironmentBranchDeployments[deploymentID]
	return ok, nil
}

func (m *MemStore) DeleteProjectEnvironmentBranch(ctx context.Context, accountID, projectID, slug string) error {
	m.mu.Lock()
	branch, err := m.projectEnvironmentBranchBySlugLocked(accountID, projectID, slug)
	if err == nil && m.projectEnvironmentHasBranchesLocked(branch.EnvironmentID) {
		err = ErrProjectEnvironmentBranchChildren
	}
	if err != nil {
		m.mu.Unlock()
		return err
	}
	now := time.Now().UTC()
	for id, d := range m.deployments {
		app, ok := m.apps[d.AppID]
		if !ok || app.ProjectID != projectID || normalizedDeploymentScope(d.Scope) != slug {
			continue
		}
		switch d.Status {
		case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting:
			d.Status = DeployCancelled
			d.CancelledAt = &now
		case DeployLive:
			d.Status, d.TrafficPercent = DeploySuperseded, 0
		default:
			continue
		}
		m.putDeploymentLocked(id, d)
	}
	m.mu.Unlock()
	return m.DeleteProjectEnvironment(ctx, accountID, projectID, slug)
}
