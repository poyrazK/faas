package state

import "context"

type ProjectEnvironmentPromotionWorkloadSpecInput struct {
	PromotionID        string
	SourceDeploymentID string
	SourceHash         string
	PreviousTargetHash string
}

// Preparation creates a target pin without advancing its desired head. The
// captured prior head and fallback settings are restored atomically on rollback.
type ProjectEnvironmentPromotionWorkloadSpec struct {
	PromotionID                string
	AppID                      string
	DeploymentID               string
	PreparedSpecID             string
	PreviousSpecID             string
	SourceHash                 string
	PreviousHash               string
	PreviousSettings           ProjectEnvironmentWorkloadSettings
	legacyPreviousDeploymentID string
	legacyPreviousSpec         ProjectEnvironmentWorkloadSpec
}

type ProjectEnvironmentPromotionWorkloadSpecStore interface {
	CreateDeploymentForEnvironmentPromotion(context.Context, Deployment, ProjectEnvironmentPromotionWorkloadSpecInput) (Deployment, error)
	ProjectEnvironmentPromotionWorkloadSpec(context.Context, string, string, string) (ProjectEnvironmentPromotionWorkloadSpec, error)
}

// Physical placement belongs to the target environment. Promotion copies
// application intent while preserving the target's assigned infrastructure.
func WorkloadSettingsForPromotion(source, target ProjectEnvironmentWorkloadSettings) (ProjectEnvironmentWorkloadSettings, error) {
	settings, err := cloneWorkloadSettings(source)
	if err != nil {
		return ProjectEnvironmentWorkloadSettings{}, err
	}
	settings.StaticEgressIP = target.StaticEgressIP
	settings.OverflowNode = target.OverflowNode
	return cloneWorkloadSettings(settings)
}

func clonePromotionWorkloadSpec(capture ProjectEnvironmentPromotionWorkloadSpec) (ProjectEnvironmentPromotionWorkloadSpec, error) {
	settings, err := cloneWorkloadSettings(capture.PreviousSettings)
	capture.PreviousSettings = settings
	return capture, err
}
