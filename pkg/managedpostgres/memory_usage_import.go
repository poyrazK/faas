package managedpostgres

import (
	"context"
	"sort"
)

type usageImportReceipt struct {
	RequestHash string
	Request     UsageImportRequest
	ActorID     string
	Policy      UsagePolicy
	Result      UsageImportResult
	Audit       usageImportPlan
}

func (s *MemoryStore) ImportUsage(ctx context.Context, command UsageImportCommand, apply bool) (UsageImportResult, error) {
	if err := validateUsageImportRequest(command.Request, command.ActorID, command.Now, apply); err != nil {
		return UsageImportResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return UsageImportResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := command.Expected.AccountID + "\x00" + command.Request.ImportID
	fingerprint := usageImportRequestHash(command.ActorID, command.Request)
	if receipt, ok := s.usageImports[key]; ok && apply {
		if receipt.RequestHash != fingerprint {
			return UsageImportResult{}, ErrConflict
		}
		return receipt.Result, nil
	}
	database, ok := s.databases[command.Expected.ID]
	if !ok {
		return UsageImportResult{}, ErrNotFound
	}
	if len(command.Records) == 0 || len(command.Records[0]) == 0 {
		return UsageImportResult{}, ErrInvalid
	}
	window := command.Records[0][0].WindowTo.Sub(command.Records[0][0].WindowFrom)
	progress := s.usageProgress[usageProgressKey{database.ID, window}]
	var existing []UsageRecord
	from, to := command.Request.Windows[0].From, command.Request.Windows[len(command.Request.Windows)-1].To
	for _, record := range s.usage {
		if record.DatabaseID != database.ID {
			continue
		}
		if record.WindowTo.Sub(record.WindowFrom) != window {
			return UsageImportResult{}, ErrConflict
		}
		if record.WindowFrom.Before(to) && record.WindowTo.After(from) {
			existing = append(existing, record)
		}
	}
	sort.Slice(existing, func(i, j int) bool {
		if existing[i].WindowFrom.Equal(existing[j].WindowFrom) {
			return existing[i].Meter < existing[j].Meter
		}
		return existing[i].WindowFrom.Before(existing[j].WindowFrom)
	})
	plan, err := planUsageImport(command, database, progress, existing)
	if err != nil {
		return UsageImportResult{}, err
	}
	if !apply {
		return plan.Result, nil
	}
	if plan.Result.Revision != command.Request.ExpectedRevision {
		return UsageImportResult{}, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return UsageImportResult{}, err
	}
	for _, record := range plan.After {
		s.usage[importUsageKey(record)] = record
	}
	plan.Progress.UpdatedAt = command.Now
	s.usageProgress[usageProgressKey{database.ID, window}] = plan.Progress
	plan.Result.Applied = true
	s.usageImports[key] = usageImportReceipt{RequestHash: fingerprint, Request: command.Request, ActorID: command.ActorID,
		Policy: command.Policy, Result: plan.Result, Audit: plan}
	return plan.Result, nil
}

func (s *MemoryStore) ReplayUsageImport(ctx context.Context, account, actor string, request UsageImportRequest) (UsageImportResult, error) {
	if err := ctx.Err(); err != nil {
		return UsageImportResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.usageImports[account+"\x00"+request.ImportID]
	if !ok {
		return UsageImportResult{}, ErrNotFound
	}
	if receipt.RequestHash != usageImportRequestHash(actor, request) {
		return UsageImportResult{}, ErrConflict
	}
	return receipt.Result, nil
}
