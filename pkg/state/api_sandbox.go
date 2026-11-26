package state

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// APISandboxDeploymentStore materializes the retained release artifact without
// importing its runtime configuration, data, or snapshot.
type APISandboxDeploymentStore interface {
	CreateAPISandboxDeployment(ctx context.Context, accountID, sandboxAppID, sourceDeploymentID string) (Deployment, error)
}

const apiSandboxParentPrefix = "api-sandbox:"

// APISandboxPreviewParent tags the lease as a developer preview while
// keeping it outside production-slug resolution, including on older gateways.
func APISandboxPreviewParent(parentSlug string) string {
	return apiSandboxParentPrefix + parentSlug
}

func IsAPISandbox(app App) bool {
	return IsDeveloperApp(app) && strings.HasPrefix(app.PreviewOfSlug, apiSandboxParentPrefix) &&
		app.Manifest.APISandboxSourceDeploymentID != ""
}

func APISandboxAcceptsRequests(app App, now time.Time) bool {
	return IsAPISandbox(app) && app.Status == AppActive &&
		app.PreviewPrState == PreviewPrStateOpen && app.PreviewExpiresAt != nil && app.PreviewExpiresAt.After(now)
}

const apiSandboxDeploymentReason = "api-sandbox"

func IsAPISandboxDeployment(deployment Deployment) bool {
	return deployment.Reason == apiSandboxDeploymentReason
}

func apiSandboxDeploymentRequest(accountID, sandboxID, sourceID string) (Deployment, error) {
	if !validUUID(accountID) || !validUUID(sandboxID) || !validUUID(sourceID) {
		return Deployment{}, ErrInvalidArgument
	}
	return Deployment{
		AppID: sandboxID, Scope: DefaultEnvScope,
		branchInput: &projectEnvironmentBranchDeploymentInput{AccountID: accountID, SandboxSourceID: sourceID},
	}, nil
}

func (m *MemStore) CreateAPISandboxDeployment(ctx context.Context, accountID, sandboxID, sourceID string) (Deployment, error) {
	d, err := apiSandboxDeploymentRequest(accountID, sandboxID, sourceID)
	if err != nil {
		return Deployment{}, err
	}
	created, _, err := m.createDeployment(ctx, d, nil, nil)
	return created, err
}

func (s *PgStore) CreateAPISandboxDeployment(ctx context.Context, accountID, sandboxID, sourceID string) (Deployment, error) {
	d, err := apiSandboxDeploymentRequest(accountID, sandboxID, sourceID)
	if err != nil {
		return Deployment{}, err
	}
	created, _, err := s.createDeployment(ctx, d, nil, nil)
	return created, err
}

// APISandboxSourceSupported limits the first release to single-process HTTP
// apps with a retained layer and an existing release command that can prepare
// a fresh database.
func APISandboxSourceSupported(source Deployment) bool {
	switch source.Status {
	case DeployLive, DeploySuperseded:
	default:
		return false
	}
	return apiSandboxArtifactAvailable(source)
}

func apiSandboxArtifactAvailable(source Deployment) bool {
	sidecars := string(source.Sidecars)
	return source.DeletedAt == nil && source.RootfsKey != "" && source.RootfsBytes > 0 &&
		len(source.ReleaseCommand) > 0 && (sidecars == "" || sidecars == "null" || sidecars == "[]")
}

func apiSandboxCloneArtifact(source Deployment) projectCloneArtifact {
	artifact := normalizeProjectCloneArtifact(projectCloneArtifactFromDeployment(source))
	artifact.OverrideEnv = nil
	artifact.OverrideEnvSecrets = nil
	artifact.Workflows = nil
	return artifact
}

func sandboxDeploymentFromSource(source Deployment, input *projectEnvironmentBranchDeploymentInput, appID string) Deployment {
	artifact := apiSandboxCloneArtifact(source)
	deployment := branchDeploymentFromArtifact(artifact, DefaultEnvScope, input)
	deployment.AppID = appID
	deployment.Reason = apiSandboxDeploymentReason
	deployment.ReleaseCommand = append([]string(nil), source.ReleaseCommand...)
	deployment.ReleaseCommandShell = source.ReleaseCommandShell
	return deployment
}

func canSeedAPISandbox(sandbox, parent App, input *projectEnvironmentBranchDeploymentInput) bool {
	return APISandboxAcceptsRequests(sandbox, time.Now()) &&
		sandbox.AccountID == input.AccountID && parent.AccountID == input.AccountID &&
		sandbox.Manifest.APISandboxSourceDeploymentID == input.SandboxSourceID &&
		APISandboxPreviewParent(parent.Slug) == sandbox.PreviewOfSlug && parent.Status == AppActive && parent.PreviewOfSlug == ""
}

func (m *MemStore) prepareAPISandboxDeploymentLocked(d Deployment) (Deployment, *Deployment, string, error) {
	input := d.branchInput
	sandbox := m.apps[d.AppID]
	source := m.deployments[input.SandboxSourceID]
	if !canSeedAPISandbox(sandbox, m.apps[source.AppID], input) {
		return Deployment{}, nil, "", ErrNotFound
	}
	for _, existing := range m.deployments {
		if existing.AppID == sandbox.ID {
			if existing.Reason != apiSandboxDeploymentReason {
				return Deployment{}, nil, "", ErrConflict
			}
			return Deployment{}, &existing, "", nil
		}
	}
	if err := m.validateSandboxCloneSourceLocked(sandbox.ID, source); err != nil {
		return Deployment{}, nil, "", err
	}
	if err := m.requireLayerArtifactsRetainedLocked(m.deploymentLayerKeysLocked(source)); err != nil {
		return Deployment{}, nil, "", err
	}
	return sandboxDeploymentFromSource(source, input, sandbox.ID), nil, source.ID, nil
}

func prepareAPISandboxDeploymentTx(ctx context.Context, tx pgx.Tx, d Deployment) (Deployment, *Deployment, string, error) {
	input := d.branchInput
	source, err := scanDeploymentWithRootfs(tx.QueryRow(ctx, "select "+deploymentSelectColumnsWithRootfs+" from deployments where id = $1", input.SandboxSourceID))
	if err != nil {
		return Deployment{}, nil, "", mapErr(err)
	}
	sandbox, err := appByIDDB(ctx, tx, d.AppID)
	if err != nil {
		return Deployment{}, nil, "", err
	}
	parent, err := appByIDDB(ctx, tx, source.AppID)
	if err != nil {
		return Deployment{}, nil, "", err
	}
	if !canSeedAPISandbox(sandbox, parent, input) {
		return Deployment{}, nil, "", ErrNotFound
	}
	existing, err := scanDeploymentWithRootfs(tx.QueryRow(ctx, "select "+deploymentSelectColumnsWithRootfs+" from deployments where app_id = $1 order by created_at desc, id desc limit 1", sandbox.ID))
	if err == nil {
		if existing.Reason != apiSandboxDeploymentReason {
			return Deployment{}, nil, "", ErrConflict
		}
		return Deployment{}, &existing, "", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, ErrNotFound) {
		return Deployment{}, nil, "", mapErr(err)
	}
	if err := validateSandboxCloneSourceTx(ctx, tx, sandbox.ID, source); err != nil {
		return Deployment{}, nil, "", err
	}
	if err := requireDeploymentLayerArtifactsTx(ctx, tx, source.ID); err != nil {
		return Deployment{}, nil, "", err
	}
	return sandboxDeploymentFromSource(source, input, sandbox.ID), nil, source.ID, nil
}
