package routehealth

import (
	"errors"
	"net/url"
	"reflect"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ValidateInvestigation binds diagnostic rows to already validated report and
// finding verdicts. Missing traces never imply complete request capture.
func ValidateInvestigation(r api.RouteHealthInvestigation, opts api.RouteHealthInvestigationOptions, slug string) error {
	aggregate, err := InvestigationFinding(r.Report, opts)
	if err != nil {
		return err
	}
	f := r.Finding
	status, reason := InvestigationSignal(f, opts.StatusCode)
	if r.Version != 1 || r.Report.Customers != nil || r.Selection != opts.Selection() || r.Status != status || r.Reason != reason || r.Coverage != "observed_only" || r.ExamplesLimit != api.RouteHealthInvestigationExamplesLimit || len(r.Windows) != api.RouteHealthWindows || len(f.Windows) != api.RouteHealthWindows {
		return errors.New("investigation context does not match the selection")
	}
	if f.Method != aggregate.Method || f.Path != aggregate.Path || f.CheckLatency != aggregate.CheckLatency || f.MaxP95MS != aggregate.MaxP95MS || !slices.Equal(f.WatchStatuses, aggregate.WatchStatuses) || opts.CustomerID == "" && !reflect.DeepEqual(f, aggregate) {
		return errors.New("investigation finding does not match the configured route")
	}
	observed := r.Report.StableDeploymentID != ""
	if observed && r.EvidenceStatus != "observed" || !observed && r.EvidenceStatus != "unavailable" {
		return errors.New("investigation evidence availability is inconsistent")
	}
	seen := map[string]bool{}
	for i, w := range r.Windows {
		base := f.Windows[i]
		agg := aggregate.Windows[i]
		if !w.Start.Equal(base.Start) || !w.End.Equal(base.End) || !base.Start.Equal(agg.Start) || !base.End.Equal(agg.End) || base.Candidate.Requests > agg.Candidate.Requests || base.Stable.Requests > agg.Stable.Requests || base.Candidate.ServerErrors > agg.Candidate.ServerErrors || base.Stable.ServerErrors > agg.Stable.ServerErrors {
			return errors.New("investigation window or cohort counts do not match the report")
		}
		candidate, stable := base.Candidate.ServerErrors, base.Stable.ServerErrors
		if opts.StatusCode != 0 {
			candidate, stable = -1, -1
			if f.ClientErrors != nil {
				for _, signal := range f.ClientErrors.Statuses {
					if signal.StatusCode == opts.StatusCode && len(signal.Windows) == api.RouteHealthWindows {
						candidate, stable = signal.Windows[i].Candidate.Responses, signal.Windows[i].Stable.Responses
					}
				}
			}
			if aggregate.ClientErrors == nil {
				return errors.New("missing aggregate watched-code evidence")
			}
			for _, signal := range aggregate.ClientErrors.Statuses {
				if signal.StatusCode == opts.StatusCode && len(signal.Windows) == api.RouteHealthWindows && (candidate > signal.Windows[i].Candidate.Responses || stable > signal.Windows[i].Stable.Responses) {
					return errors.New("customer response counts exceed aggregate observations")
				}
			}
		}
		for j, side := range []api.RouteHealthInvestigationSide{w.Candidate, w.Stable} {
			matching := candidate
			if j == 1 {
				matching = stable
			}
			if side.MatchingRequests != matching || side.ObservedRows < 0 || side.ObservedRows > side.MatchingRequests || len(side.Examples) != int(min(side.ObservedRows, int64(r.ExamplesLimit))) || side.ExamplesTruncated != (side.ObservedRows > int64(r.ExamplesLimit)) || !observed && (side.MatchingRequests != 0 || side.ObservedRows != 0) {
				return errors.New("investigation row inventory does not match signal counts")
			}
			var represented int64
			for _, e := range side.Examples {
				id, err := uuid.Parse(e.TelemetryID)
				path := "/v1/apps/" + url.PathEscape(slug) + "/debug/requests/" + e.TelemetryID + "/evidence"
				matches := e.Status >= 500 && e.Status <= 599
				if opts.StatusCode != 0 {
					matches = e.Status == opts.StatusCode
				}
				if err != nil || id.String() != e.TelemetryID || seen[e.TelemetryID] || e.EvidencePath != path || e.ReceivedAt.Before(w.Start) || !e.ReceivedAt.Before(w.End) || !matches || e.LatencyMS < 0 || e.RepresentedRequests < 1 || e.RepresentedRequests > side.MatchingRequests-represented {
					return errors.New("invalid or incorrectly scoped investigation example")
				}
				seen[e.TelemetryID] = true
				represented += e.RepresentedRequests
			}
			if !side.ExamplesTruncated && represented != side.MatchingRequests {
				return errors.New("complete example inventory does not reconcile")
			}
		}
	}
	return nil
}
