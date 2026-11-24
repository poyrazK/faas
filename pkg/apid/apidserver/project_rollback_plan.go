package apidserver

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hash the inputs a reviewed rollback executes, excluding observations such as
// current readiness and the IDs generated when the operation starts.
func projectRollbackPlanHash(plan state.ProjectRollbackRecord, req api.ProjectRollbackRequest) (string, error) {
	type service struct {
		AppID, Scope, CurrentID, TargetID string
		CurrentServedAt                   time.Time
		ReleaseRuns                       []state.ReleaseRun
		HealthProbes                      []api.ProjectReleaseProbe
		LiveTrafficPolicy                 *state.ProjectRollbackLiveTrafficPolicy `json:",omitempty"`
	}
	review := struct {
		Version                          int
		ProjectID, DeployGroupID         string
		All, AcknowledgeReleases, Verify bool
		Services                         []service
		ToDeploy                         string `json:"ToDeploy,omitempty"`
		UndoOf                           string `json:"UndoOf,omitempty"`
		UndoOnHealthFailure              bool   `json:"UndoOnHealthFailure,omitempty"`
		UndoOnLiveTrafficFailure         bool   `json:"UndoOnLiveTrafficFailure,omitempty"`
		VerifyBeforeCutover              bool   `json:"VerifyBeforeCutover,omitempty"`
		StabilizeForSeconds              int    `json:"StabilizeForSeconds,omitempty"`
		VerifyLiveTraffic                bool   `json:"VerifyLiveTraffic,omitempty"`
	}{
		Version: 1, ProjectID: plan.ProjectID, DeployGroupID: plan.DeployGroupID,
		All: req.All, AcknowledgeReleases: plan.AcknowledgeReleases, Verify: plan.Verify, ToDeploy: req.ToDeploy,
		UndoOf:              plan.UndoOf,
		UndoOnHealthFailure: req.UndoOnHealthFailure, VerifyBeforeCutover: req.VerifyBeforeCutover,
		UndoOnLiveTrafficFailure: req.UndoOnLiveTrafficFailure,
		StabilizeForSeconds:      req.StabilizeForSeconds, VerifyLiveTraffic: req.VerifyLiveTraffic,
	}
	for _, step := range plan.Steps {
		if step.Status != "planned" {
			continue
		}
		pin := service{
			AppID: step.AppID, Scope: step.Scope, CurrentID: step.CurrentID, TargetID: step.TargetID,
			ReleaseRuns:  canonicalProjectRollbackReleaseRuns(plan.ReleaseReviews[step.AppID]),
			HealthProbes: plan.HealthProbes[step.AppID],
		}
		if policy, ok := plan.LiveTrafficPolicies[step.AppID]; ok {
			pin.LiveTrafficPolicy = &policy
		}
		if step.CurrentServedAt != nil {
			pin.CurrentServedAt = step.CurrentServedAt.UTC()
		}
		review.Services = append(review.Services, pin)
	}
	return hashProjectRollbackReview(review)
}

func canonicalProjectRollbackReleaseRuns(runs []state.ReleaseRun) []state.ReleaseRun {
	runs = slices.Clone(runs)
	for i := range runs {
		runs[i].StartedAt = runs[i].StartedAt.UTC()
	}
	slices.SortFunc(runs, func(a, b state.ReleaseRun) int {
		return cmp.Or(a.StartedAt.Compare(b.StartedAt), cmp.Compare(a.DeploymentID, b.DeploymentID),
			cmp.Compare(a.Revision, b.Revision), cmp.Compare(a.Status, b.Status))
	})
	return runs
}

func projectRollbackResumePlanHash(source state.ProjectRollbackRecord, pins []state.ProjectRollbackResumePin) (string, error) {
	return hashProjectRollbackReview(struct {
		Kind, Project, Operation string
		Version                  int64
		Serving                  []state.ProjectRollbackResumePin
	}{"project-rollback-resume-v1", source.ProjectID, source.ID, source.Version, pins})
}

func hashProjectRollbackReview(review any) (string, error) {
	data, err := json.Marshal(review)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
