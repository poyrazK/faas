package main

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) previewManagedPostgresUsageImport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.managedPostgresUsageImport(w, r, acct, false)
}

func (s *server) applyManagedPostgresUsageImport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.managedPostgresUsageImport(w, r, acct, true)
}

func (s *server) managedPostgresUsageImport(w http.ResponseWriter, r *http.Request, acct state.Account, apply bool) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	account, err := uuid.Parse(r.PathValue("account_id"))
	if err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	var request api.ManagedPostgresUsageImportRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := decodeJSON(r, &request); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	result, err := s.managedPostgres.ImportUsage(r.Context(), account.String(), acct.ID, usageImportRequest(request), apply)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, usageImportView(result))
}

func usageImportRequest(request api.ManagedPostgresUsageImportRequest) managedpostgres.UsageImportRequest {
	result := managedpostgres.UsageImportRequest{ImportID: request.ImportID, DatabaseID: request.DatabaseID,
		EvidenceReference: request.EvidenceReference, EvidenceSHA256: request.EvidenceSHA256, Reason: request.Reason, ExpectedRevision: request.ExpectedRevision}
	for _, w := range request.Windows {
		window := managedpostgres.UsageImportWindow{From: w.From, To: w.To, ObservedAt: w.ObservedAt}
		for _, reading := range w.Readings {
			window.Readings = append(window.Readings, managedpostgres.MeterReading{Meter: managedpostgres.Meter(reading.Meter), Quantity: reading.Quantity})
		}
		result.Windows = append(result.Windows, window)
	}
	return result
}

func usageImportView(result managedpostgres.UsageImportResult) api.ManagedPostgresUsageImportResult {
	return api.ManagedPostgresUsageImportResult{ImportID: result.ImportID, DatabaseID: result.DatabaseID, Revision: result.Revision,
		Applied: result.Applied, WindowCount: result.WindowCount, PreviousCostMillicents: result.PreviousCostMillicents,
		ImportedCostMillicents: result.ImportedCostMillicents, CostDeltaMillicents: result.CostDeltaMillicents,
		CollectedFrom: result.CollectedFrom, CollectedUntil: result.CollectedUntil, ObservedAt: result.ObservedAt}
}
