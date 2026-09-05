package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// obsCapacity handles GET /v1/admin/obs/capacity. The store projection is
// already aggregate-shaped, so this handler only folds the bounded node rows
// into fleet totals for the operator UI.
func (s *server) obsCapacity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	snapshot, err := s.store.OperatorCapacity(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not aggregate fleet capacity"))
		return
	}
	response := api.ObsCapacityResponse{
		GeneratedAt: time.Now().UTC(),
		Summary: api.ObsCapacitySummary{
			TotalNodes:   len(snapshot.Nodes),
			AppsTotal:    snapshot.AppsTotal,
			TenantsTotal: snapshot.TenantsTotal,
			UnplacedApps: snapshot.UnplacedApps,
		},
		Nodes: make([]api.ObsCapacityNode, 0, len(snapshot.Nodes)),
	}
	for _, node := range snapshot.Nodes {
		row := api.ObsCapacityNode{
			ID:                   node.ID,
			Name:                 node.Name,
			Active:               node.Active,
			VPCPUs:               node.VPCPUs,
			VCPUBudget:           node.VCPUBudget,
			MemMB:                node.MemMB,
			AdmissionCeilingMB:   node.AdmissionCeilingMB,
			InstancesLive:        node.InstancesLive,
			InstancesRunning:     node.InstancesRunning,
			InstancesWaking:      node.InstancesWaking,
			InstancesColdBooting: node.InstancesColdBooting,
			RAMUsedMB:            node.RAMUsedMB,
			AdmissionMarginMB:    int64(node.AdmissionCeilingMB) - node.RAMUsedMB,
			AppsCount:            node.AppsCount,
			TenantsCount:         node.TenantsCount,
		}
		response.Nodes = append(response.Nodes, row)
		if node.Active {
			response.Summary.ActiveNodes++
		} else {
			response.Summary.InactiveNodes++
		}
		response.Summary.TotalVCPUs += int64(node.VPCPUs)
		response.Summary.TotalVCPUBudget += int64(node.VCPUBudget)
		response.Summary.TotalMemMB += int64(node.MemMB)
		response.Summary.TotalAdmissionCeilingMB += int64(node.AdmissionCeilingMB)
		response.Summary.RAMUsedMB += node.RAMUsedMB
		response.Summary.InstancesLive += node.InstancesLive
		response.Summary.InstancesRunning += node.InstancesRunning
		response.Summary.InstancesWaking += node.InstancesWaking
		response.Summary.InstancesColdBooting += node.InstancesColdBooting
	}
	response.Summary.AdmissionMarginMB =
		response.Summary.TotalAdmissionCeilingMB - response.Summary.RAMUsedMB
	writeJSON(w, http.StatusOK, response)
}

// obsTenant360 handles GET /v1/admin/obs/tenants/{id}/360. It composes the
// existing safe tenant projection with one month of aggregate usage and a
// bounded invoice/credit summary. No raw secrets, invoice URLs, or usage
// minute rows cross this boundary.
func (s *server) obsTenant360(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	targetID := r.PathValue("id")
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad account id", "expected UUID"))
		return
	}
	monthText := r.URL.Query().Get("month")
	if monthText == "" {
		monthText = time.Now().UTC().Format("2006-01")
	}
	month, err := time.Parse("2006-01", monthText)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad month", "expected YYYY-MM"))
		return
	}
	includePII, _ := strconv.ParseBool(r.URL.Query().Get("include_pii"))
	if includePII {
		emitPIIAccessed(r, s, acct, "tenants/"+targetID+"/360")
	}
	target, err := s.store.AccountByID(r.Context(), targetID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Account not found", err.Error()))
		return
	}

	detail := buildTenantDetail(r.Context(), s.store, target, includePII)
	usage, err := projectObsTenantUsage(r, s.store, target, month, monthText)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load tenant usage"))
		return
	}
	billing, err := projectObsTenantBilling(r, s.store, target.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load tenant billing"))
		return
	}
	writeJSON(w, http.StatusOK, api.ObsTenant360Response{
		Account:  detail.Account,
		Apps:     detail.Apps,
		Orgs:     detail.Orgs,
		APIKeys:  detail.APIKeys,
		Sessions: detail.Sessions,
		Usage:    usage,
		Billing:  billing,
	})
}

func projectObsTenantUsage(r *http.Request, st state.Store, acct state.Account, month time.Time, monthText string) (api.ObsTenantUsage, error) {
	rows, err := st.UsageByMonth(r.Context(), acct.ID, month)
	if err != nil {
		return api.ObsTenantUsage{}, err
	}
	apps, err := st.ListApps(r.Context(), acct.ID)
	if err != nil {
		return api.ObsTenantUsage{}, err
	}
	slugs := make(map[string]string, len(apps))
	for _, app := range apps {
		slugs[app.ID] = app.Slug
	}
	usage := api.ObsTenantUsage{
		Month: monthText,
		Apps:  make([]api.ObsTenantUsageApp, 0, len(rows)),
	}
	var mbSeconds, cpuUsec int64
	for _, row := range rows {
		mbSeconds += row.MBSeconds
		cpuUsec += row.CPUUsec
		usage.Requests += row.Requests
		usage.UsedEgressGB += float64(row.TXBytes+row.NetTxBytes) / (1024 * 1024 * 1024)
		usage.UsedIngressGB += float64(row.NetRxBytes) / (1024 * 1024 * 1024)
		usage.ColdBootTotal += row.ColdBootCount
		usage.Apps = append(usage.Apps, api.ObsTenantUsageApp{
			AppID:      row.AppID,
			AppSlug:    slugs[row.AppID],
			MBSeconds:  row.MBSeconds,
			CPUUsec:    row.CPUUsec,
			Requests:   row.Requests,
			TXBytes:    row.TXBytes,
			NetTxBytes: row.NetTxBytes,
			NetRxBytes: row.NetRxBytes,
			ColdBoots:  row.ColdBootCount,
		})
	}
	usage.UsedGBHours = meter.GBHours(mbSeconds)
	usage.UsedCPUHours = float64(cpuUsec) / 3_600_000_000.0
	limits := api.MustLimitsFor(acct.Plan)
	usage.IncludedGBHours = int64(limits.IncludedGBHours)
	usage.OverageGBHours = usage.UsedGBHours - float64(usage.IncludedGBHours)
	if usage.OverageGBHours < 0 {
		usage.OverageGBHours = 0
	}
	usage.OverageCents = int64(usage.OverageGBHours)
	return usage, nil
}

func projectObsTenantBilling(r *http.Request, st state.Store, accountID string) (api.ObsTenantBilling, error) {
	billing := api.ObsTenantBilling{Invoices: make([]api.ObsInvoiceSummary, 0)}
	overage, err := st.CurrentMonthOverageCents(r.Context(), accountID)
	if err != nil {
		return api.ObsTenantBilling{}, err
	}
	billing.CurrentMonthOverageCents = overage
	capCents, hasCap, err := st.GetAccountOverageCapCents(r.Context(), accountID)
	if err != nil {
		return api.ObsTenantBilling{}, err
	}
	if hasCap {
		billing.OverageCapCents = &capCents
	}
	credits, err := st.ListAccountCredits(r.Context(), accountID, true)
	if err != nil {
		return api.ObsTenantBilling{}, err
	}
	for _, credit := range credits {
		billing.ActiveCreditsCents += credit.CentsRemaining
	}
	invoices, err := st.ListInvoicesForAccount(r.Context(), accountID, nil, time.Time{}, 12)
	if err != nil {
		return api.ObsTenantBilling{}, err
	}
	for _, invoice := range invoices {
		billing.Invoices = append(billing.Invoices, api.ObsInvoiceSummary{
			ID:              invoice.ID,
			Provider:        invoice.Provider,
			Number:          invoice.Number,
			Status:          invoice.Status,
			Currency:        invoice.Currency,
			PeriodStart:     invoice.PeriodStart,
			PeriodEnd:       invoice.PeriodEnd,
			TotalCents:      invoice.TotalCents,
			AmountPaidCents: invoice.AmountPaidCents,
		})
	}
	return billing, nil
}
