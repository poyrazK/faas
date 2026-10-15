package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrReleaseReviewChanged = errors.New("reviewed app configuration or release changed")

type ReleaseReviewStore interface {
	ReadReleaseReview(context.Context, string, string) (api.AppReleaseReview, error)
}

type ReviewedTrafficStore interface {
	UpdateDeploymentTrafficReviewed(context.Context, string, int, string, api.AppReleaseReview) (Deployment, error)
}

func validateReviewedTraffic(review, current api.AppReleaseReview, percent int) error {
	if percent != 100 || current.Protected || current.EnvironmentID == "" {
		return ErrReleaseReviewChanged
	}
	return checkReleaseReview(review, current)
}

func checkReleaseReview(expected, current api.AppReleaseReview) error {
	if expected.AppID != current.AppID || normalizedDeploymentScope(expected.Scope) != normalizedDeploymentScope(current.Scope) || expected.ProjectID != current.ProjectID || expected.EnvironmentID != current.EnvironmentID || expected.WorkloadRevision != current.WorkloadRevision || expected.WorkloadConfigHash != current.WorkloadConfigHash || expected.ConfigurationHash == "" || expected.ConfigurationHash != current.ConfigurationHash || expected.LatestDeploymentID != current.LatestDeploymentID || !slices.Equal(expected.ServingDeploymentIDs, current.ServingDeploymentIDs) {
		return ErrReleaseReviewChanged
	}
	return nil
}

func (m *MemStore) ReadReleaseReview(_ context.Context, appID, scope string) (api.AppReleaseReview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releaseReviewLocked(appID, normalizedDeploymentScope(scope))
}

func (m *MemStore) releaseReviewLocked(appID, scope string) (api.AppReleaseReview, error) {
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return api.AppReleaseReview{}, ErrNotFound
	}
	// These are observed placement/scaling fields, rather than app intent.
	app.LastScaleOutAt, app.LastScaleInAt = nil, nil
	app.NodeID = ""
	app.ReassignedAt = nil
	review := api.AppReleaseReview{AppID: appID, Scope: scope, ServingDeploymentIDs: []string{}}
	var intent any = app
	if scope != api.DefaultEnvScope {
		env, err := m.workloadSpecEnvironmentLocked(app.AccountID, app.ProjectID, scope, appID)
		if err != nil {
			return review, err
		}
		spec, settings, err := m.environmentWorkloadSettingsLocked(app, scope)
		if err != nil {
			return review, err
		}
		review.ProjectID, review.EnvironmentID, review.WorkloadRevision, review.WorkloadConfigHash = app.ProjectID, env.ID, spec.Revision, spec.Hash
		review.Protected = env.Protected
		intent = struct {
			App         App
			Environment ProjectEnvironment
			SpecID      string
			Settings    ProjectEnvironmentWorkloadSettings
		}{app, env, spec.ID, settings}
	}
	data, err := json.Marshal(intent)
	if err != nil {
		return api.AppReleaseReview{}, err
	}
	sum := sha256.Sum256(data)
	review.ConfigurationHash = hex.EncodeToString(sum[:])
	var latest Deployment
	for _, deployment := range m.deployments {
		if deployment.AppID != appID || normalizedDeploymentScope(deployment.Scope) != scope {
			continue
		}
		if review.LatestDeploymentID == "" || deployment.CreatedAt.After(latest.CreatedAt) || (deployment.CreatedAt.Equal(latest.CreatedAt) && deployment.ID > latest.ID) {
			latest = deployment
			review.LatestDeploymentID = deployment.ID
		}
		if deployment.Status == DeployLive && deployment.TrafficPercent > 0 {
			review.ServingDeploymentIDs = append(review.ServingDeploymentIDs, deployment.ID)
		}
	}
	slices.Sort(review.ServingDeploymentIDs)
	return review, nil
}

// Native rollouts deliberately omit traffic: services use the readiness marker;
// workers become the newest live generation and are reconciled by schedd.
func validateReviewedDeployment(d Deployment) error {
	if d.ReviewedRolloutMode == "" {
		if d.TrafficPercent != 0 || !d.TrafficPercentExplicit {
			return ErrInvalidArgument
		}
		return nil
	}
	if d.TrafficPercentExplicit || d.TrafficPercent != 0 || d.CanaryTotalSteps != 0 {
		return ErrInvalidArgument
	}
	switch d.ReviewedRolloutMode {
	case api.ExecutionModeService:
		if !IsServiceRollout(d) {
			return ErrInvalidArgument
		}
	case api.ExecutionModeWorker:
		if NormalizeRolloutState(d.RolloutState) != "pending" {
			return ErrInvalidArgument
		}
	default:
		return ErrInvalidArgument
	}
	return nil
}

type serviceRecoveryReviewKey struct{}

// WithServiceRecoveryReview carries admission metadata only; stores check it
// under their normal rollout locks before writing reverse-handoff intent.
func WithServiceRecoveryReview(ctx context.Context, review api.AppReleaseReview) context.Context {
	return context.WithValue(ctx, serviceRecoveryReviewKey{}, review)
}
func serviceRecoveryReview(ctx context.Context) (api.AppReleaseReview, bool) {
	review, ok := ctx.Value(serviceRecoveryReviewKey{}).(api.AppReleaseReview)
	return review, ok
}
func checkServiceRecoveryReview(expected, current api.AppReleaseReview) error {
	if current.Protected {
		return ErrReleaseReviewChanged
	}
	return checkReleaseReview(expected, current)
}
