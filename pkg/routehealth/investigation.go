package routehealth

import (
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func InvestigationFinding(report api.RouteHealthReport, opts api.RouteHealthInvestigationOptions) (api.RouteHealthFinding, error) {
	if err := opts.Validate(); err != nil {
		return api.RouteHealthFinding{}, err
	}
	for _, f := range report.Routes {
		if f.Method == opts.Method && f.Path == opts.Path {
			if opts.StatusCode != 0 && !slices.Contains(f.WatchStatuses, opts.StatusCode) {
				return f, errors.New("selected response code is not watched on this route")
			}
			return f, nil
		}
	}
	return api.RouteHealthFinding{}, errors.New("select an exact configured method/path label")
}

func InvestigationSignal(f api.RouteHealthFinding, code int) (string, string) {
	if code == 0 {
		return f.ErrorStatus, f.ErrorReason
	}
	if f.ClientErrors != nil {
		for _, signal := range f.ClientErrors.Statuses {
			if signal.StatusCode == code {
				return signal.Status, signal.Reason
			}
		}
	}
	return "unknown", "status_evidence_unavailable"
}

func NewInvestigation(report api.RouteHealthReport, finding api.RouteHealthFinding, selection api.RouteHealthInvestigationSelection) api.RouteHealthInvestigation {
	r := api.RouteHealthInvestigation{Version: 1, Report: report, Finding: finding, Selection: selection, Coverage: "observed_only", EvidenceStatus: "unavailable", ExamplesLimit: api.RouteHealthInvestigationExamplesLimit, Windows: []api.RouteHealthInvestigationWindow{}}
	r.Status, r.Reason = InvestigationSignal(finding, selection.StatusCode)
	for _, w := range finding.Windows {
		r.Windows = append(r.Windows, api.RouteHealthInvestigationWindow{Start: w.Start, End: w.End, Candidate: api.RouteHealthInvestigationSide{Examples: []api.RouteHealthInvestigationExample{}}, Stable: api.RouteHealthInvestigationSide{Examples: []api.RouteHealthInvestigationExample{}}})
	}
	return r
}
