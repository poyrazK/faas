package managedpostgres

import (
	"context"
	"sort"
	"time"
)

type accountingReconciliationReceipt struct {
	RequestHash string
	Command     AccountingReconciliationCommand
	Plan        accountingReconciliationPlan
}

func (s *MemoryStore) ReconcileAccounting(ctx context.Context, command AccountingReconciliationCommand, apply bool) (AccountingReconciliationResult, error) {
	if err := validateAccountingReconciliation(command.Request, command.ActorID, command.Now, apply); err != nil {
		return AccountingReconciliationResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return AccountingReconciliationResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := command.Expected.AccountID + "\x00" + command.Request.ReconciliationID
	fingerprint := accountingReconciliationHash(command.ActorID, command.Request)
	if receipt, ok := s.accountingReconciliations[key]; ok && apply {
		if receipt.RequestHash != fingerprint {
			return AccountingReconciliationResult{}, ErrConflict
		}
		return cloneAccountingReconciliationResult(receipt.Plan.Result), nil
	}
	database, ok := s.databases[command.Expected.ID]
	if !ok {
		return AccountingReconciliationResult{}, ErrNotFound
	}
	claimed := false
	for _, other := range s.databases {
		if other.ID != database.ID && other.BackendID == command.Request.BackendID && other.BackendFingerprint == command.Request.BackendFingerprint && other.ProviderResourceID == command.Request.ProviderResourceID {
			claimed = true
		}
	}
	var coverage []reconciliationCoverage
	for k, p := range s.usageProgress {
		if k.databaseID == database.ID {
			coverage = append(coverage, reconciliationCoverage{int64(k.window / time.Second), p.CollectedFrom.UTC(), p.CollectedUntil.UTC(), p.ObservedAt.UTC(), p.UpdatedAt.UTC(), p.SourceDatabaseID})
		}
	}
	sort.Slice(coverage, func(i, j int) bool { return coverage[i].WindowSeconds < coverage[j].WindowSeconds })
	ledger := memoryReconciliationLedger(s.usage, database, command)
	plan, err := planAccountingReconciliation(command, database, coverage, ledger, claimed)
	if err != nil {
		return AccountingReconciliationResult{}, err
	}
	if !apply {
		return plan.Result, nil
	}
	if plan.Result.Revision != command.Request.ExpectedRevision {
		return AccountingReconciliationResult{}, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return AccountingReconciliationResult{}, err
	}
	s.databases[database.ID] = cloneDatabase(plan.After)
	for k := range s.usageProgress {
		if k.databaseID == database.ID {
			delete(s.usageProgress, k)
		}
	}
	plan.Result.Applied = true
	s.accountingReconciliations[key] = accountingReconciliationReceipt{fingerprint, command, plan}
	return cloneAccountingReconciliationResult(plan.Result), nil
}

func memoryReconciliationLedger(records map[usageKey]UsageRecord, database Database, command AccountingReconciliationCommand) reconciliationLedger {
	var ledger reconciliationLedger
	for _, row := range records {
		if row.DatabaseID != database.ID {
			continue
		}
		ledger.Records++
		if ledger.FirstWindow.IsZero() || row.WindowFrom.Before(ledger.FirstWindow) {
			ledger.FirstWindow = row.WindowFrom.UTC()
		}
		if row.WindowTo.After(ledger.LastWindow) {
			ledger.LastWindow = row.WindowTo.UTC()
		}
		if row.ObservedAt.After(ledger.LastObservation) {
			ledger.LastObservation = row.ObservedAt.UTC()
		}
		if row.AccountID != database.AccountID || row.BackendID != database.BackendID || row.BackendFingerprint != database.BackendFingerprint ||
			row.WindowTo.Sub(row.WindowFrom) != command.Policy.Window || !contains(command.Meters, row.Meter) {
			ledger.Invalid = true
		}
	}
	return ledger
}

func (s *MemoryStore) ReplayAccountingReconciliation(ctx context.Context, account, actor string, request AccountingReconciliationRequest) (AccountingReconciliationResult, error) {
	if err := ctx.Err(); err != nil {
		return AccountingReconciliationResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.accountingReconciliations[account+"\x00"+request.ReconciliationID]
	if !ok {
		return AccountingReconciliationResult{}, ErrNotFound
	}
	if receipt.RequestHash != accountingReconciliationHash(actor, request) {
		return AccountingReconciliationResult{}, ErrConflict
	}
	return cloneAccountingReconciliationResult(receipt.Plan.Result), nil
}

func cloneAccountingReconciliationResult(result AccountingReconciliationResult) AccountingReconciliationResult {
	if result.PreviousDeletedAt != nil {
		previous := *result.PreviousDeletedAt
		result.PreviousDeletedAt = &previous
	}
	return result
}
