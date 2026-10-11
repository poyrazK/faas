package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type ProfileInvestigationsView struct {
	RequestMix       *ProfileRequestMixView
	Saved            *api.ProfileInvestigationResponse
	Items            []api.ProfileInvestigationResponse
	CSRF             string
	Error            string
	SelectedPath     *api.ProfileCallPath
	SelectedPathJSON string
	TitleMaxBytes    int
	TextMaxBytes     int
	CanSave          bool
}

func (v *ProfileInvestigationsView) RegressionOptions() api.ProfileRegressionOptions {
	if v.Saved != nil && v.Saved.Saved.Assessment != nil {
		return api.NormalizeProfileRegressionOptions(v.Saved.Saved.Assessment.Options)
	}
	return api.DefaultProfileRegressionOptions()
}

func (*ProfileInvestigationsView) RegressionLimits() map[string]any {
	return map[string]any{"relative": api.ProfileRegressionMaxRelativePercent, "absolute": api.ProfileRegressionMaxAbsoluteCPU, "absolute_per_request": api.ProfileRegressionMaxAbsoluteCPUPerRequest, "profiles": api.ProfileMaxCoverageEntries, "coverage": api.ProfileRegressionMinimumCoverageRatio, "routes": api.ProfileRouteRegressionMaxRoutes, "requests": api.ProfileRegressionMaxMinimumRequests, "evidence": api.ProfileRegressionMaxEvidence}
}
