package deploydiff

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func diffComputeRegion(out *Diff, baseline *api.AppResponse, requested *string) {
	if baseline != nil {
		out.ComputeRegion = baseline.ComputeRegion
	}
	if requested == nil {
		return
	}
	if !api.ValidComputeRegion(*requested) {
		out.Breaks = append(out.Breaks, Break{Code: api.CodeValidation, Severity: SeverityError,
			Field: "compute_region", Reason: "compute_region must be a canonical lowercase identifier"})
		return
	}
	if baseline != nil {
		if *requested != baseline.ComputeRegion {
			out.Breaks = append(out.Breaks, Break{Code: api.CodeConflict, Severity: SeverityError,
				Field: "compute_region", Reason: fmt.Sprintf("app has immutable compute region %q; create a new app to use %q", baseline.ComputeRegion, *requested),
				Observed: AsAny(baseline.ComputeRegion)})
		}
		return
	}
	out.ComputeRegion = *requested
	if *requested != "" {
		out.Changes = append(out.Changes, Change{Field: "compute_region", Kind: ChangeAdd, After: AsAny(*requested)})
	}
}
