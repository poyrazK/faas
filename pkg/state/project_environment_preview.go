package state

import (
	"context"
	"time"
)

const (
	ProjectEnvironmentPreviewOpen        = "open"
	ProjectEnvironmentPreviewClosed      = "closed"
	ProjectEnvironmentPreviewTearingDown = "tearing_down"

	ProjectEnvironmentPreviewDefaultTTL = 7 * 24 * time.Hour
	ProjectEnvironmentPreviewCloseGrace = 24 * time.Hour
	ProjectEnvironmentPreviewDrainGrace = 5 * time.Minute
)

// ProjectEnvironmentPreviewDeployment identifies a release that was drained
// when an expired project preview began teardown. Callers publish the
// deployment_changed notification after the store transaction commits.
type ProjectEnvironmentPreviewDeployment struct {
	DeploymentID string
	AppID        string
}

// ProjectEnvironmentPreviewLifecycleStore is the janitor/webhook seam for
// expiring PR environments. Kept separate from Store so unrelated services
// need not depend on preview lifecycle operations.
type ProjectEnvironmentPreviewLifecycleStore interface {
	UpdateProjectEnvironmentPreviewHead(context.Context, string, string, int, string, time.Time) (ProjectEnvironment, error)
	CloseProjectEnvironmentPreview(context.Context, string, string, int, time.Time) (ProjectEnvironment, error)
	ListProjectEnvironmentPreviewsForTeardown(context.Context, time.Time, int) ([]ProjectEnvironment, error)
	BeginProjectEnvironmentPreviewTeardown(context.Context, string, string, string, time.Time, time.Time) (ProjectEnvironment, []ProjectEnvironmentPreviewDeployment, error)
}

// initializeProjectEnvironmentPreviewLifecycle supplies the default bounded
// lifetime for preview rows created through either the API or a state-store
// clone. Non-preview environments intentionally keep lifecycle fields empty.
func initializeProjectEnvironmentPreviewLifecycle(environment *ProjectEnvironment, now time.Time) error {
	if environment.PreviewPRNumber == 0 {
		if environment.PreviewState != "" || environment.PreviewExpiresAt != nil {
			return ErrInvalidArgument
		}
		return nil
	}
	if environment.PreviewState == "" {
		environment.PreviewState = ProjectEnvironmentPreviewOpen
	}
	if environment.PreviewExpiresAt == nil {
		expiresAt := now.UTC().Add(ProjectEnvironmentPreviewDefaultTTL)
		environment.PreviewExpiresAt = &expiresAt
	}
	if environment.PreviewState != ProjectEnvironmentPreviewOpen &&
		environment.PreviewState != ProjectEnvironmentPreviewClosed &&
		environment.PreviewState != ProjectEnvironmentPreviewTearingDown {
		return ErrInvalidArgument
	}
	if environment.PreviewExpiresAt.IsZero() {
		return ErrInvalidArgument
	}
	return nil
}
