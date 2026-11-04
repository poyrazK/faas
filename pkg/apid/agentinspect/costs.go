// adr: 20261103211147
package agentinspect

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func Costs(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		w.Header().Set("Cache-Control", "private, no-store")
		q, p := query(r, "month")
		if p != nil {
			api.WriteProblem(w, p)
			return
		}
		now := time.Now().UTC()
		month := q.Get("month")
		if month == "" {
			month = now.Format("2006-01")
		}
		start, err := time.Parse("2006-01", month)
		if err != nil || start.IsZero() || start.After(now) {
			api.WriteProblem(w, api.ErrValidation("month must be a current or historical YYYY-MM"))
			return
		}
		app, _, ok := d.Resolve(w, r, account)
		if !ok {
			return
		}
		if d.Released != nil && !d.Released(r, account, app, "GET /v1/billing/costs") {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "application costs are unavailable"))
			return
		}
		report, err := billing.ReadFinancialCosts(r.Context(), d.Store, account.ResourceOwnerID(), start, now)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read retained application costs"))
			return
		}
		scoped, err := billing.ScopeFinancialCostsToApp(report, app.ID, app.Slug)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not project application costs"))
			return
		}
		if p := recheck(d, r, app, "GET /v1/apps/{slug}/costs"); p != nil {
			api.WriteProblem(w, p)
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, appCosts(scoped))
	}
}

func appCosts(scoped api.FinancialAppCostsResponse) api.AppCostsResponse {
	out := api.AppCostsResponse{AppID: scoped.AppID, AppSlug: scoped.AppSlug, Currency: scoped.Currency, PeriodStart: scoped.PeriodStart, PeriodEnd: scoped.PeriodEnd, AsOf: scoped.AsOf,
		KnownUsageMillicents: scoped.KnownUsageMillicents, Scope: scoped.Scope, ScopeDescription: scoped.ScopeDescription + " Account-wide unpriced and nonbillable quantities are withheld; per-app quantity coverage is unknown.",
		Meters: []api.AppCostMeter{}, BillEstimateAvailable: false, MissingBillComponents: append([]string{}, scoped.MissingBillComponents...)}
	for _, meter := range scoped.Meters {
		out.Meters = append(out.Meters, api.AppCostMeter{Meter: meter.Meter, Unit: meter.Unit, Quantity: meter.Quantity, NetMillicents: meter.NetMillicents, Allocations: meter.Allocations,
			Coverage: api.AppCostCoverage{Complete: meter.Coverage.Complete, Fresh: meter.Coverage.Fresh, ExpectedMinutes: meter.Coverage.ExpectedMinutes, CompleteMinutes: meter.Coverage.CompleteMinutes, Reasons: append([]string{}, meter.Coverage.Reasons...)}})
	}
	return out
}
