package managedpostgres

import (
	"math"
	"time"
)

type usageImportPlan struct {
	Result   UsageImportResult
	Progress UsageProgress
	Before   []UsageRecord
	After    []UsageRecord
}

func planUsageImport(command UsageImportCommand, database Database, progress UsageProgress, existing []UsageRecord) (usageImportPlan, error) {
	var plan usageImportPlan
	if len(command.Records) != len(command.Request.Windows) {
		return plan, ErrInvalid
	}
	if database.AccountID != command.Expected.AccountID || database.ID != command.Expected.ID ||
		database.BackendID != command.Expected.BackendID || database.BackendFingerprint != command.Expected.BackendFingerprint ||
		database.ProviderResourceID == "" || database.ProviderResourceID != command.Expected.ProviderResourceID ||
		database.LeaseUntil.After(command.Now) || progress.SourceDatabaseID != "" {
		return plan, ErrConflict
	}
	prior := make(map[usageKey]UsageRecord, len(existing))
	for _, row := range existing {
		prior[importUsageKey(row)] = row
	}
	plan.Result = UsageImportResult{ImportID: command.Request.ImportID, DatabaseID: database.ID, WindowCount: len(command.Records)}
	plan.Before = existing
	for index, records := range command.Records {
		if _, err := validateUsageRecords(records); err != nil {
			return plan, err
		}
		first := records[0]
		requested := command.Request.Windows[index]
		if first.AccountID != database.AccountID || first.DatabaseID != database.ID || first.BackendID != database.BackendID ||
			first.BackendFingerprint != database.BackendFingerprint || !first.WindowFrom.Equal(requested.From) ||
			!first.WindowTo.Equal(requested.To) || !first.ObservedAt.Equal(requested.ObservedAt) {
			return plan, ErrInvalid
		}
		if first.WindowFrom.Before(database.CreatedAt.UTC().Truncate(first.WindowTo.Sub(first.WindowFrom))) {
			return plan, ErrInvalid
		}
		if err := validateUsageDatabaseWindow(database, first); err != nil {
			return plan, err
		}
		var err error
		progress, err = advanceUsageProgress(progress, first, database.CreatedAt)
		if err != nil {
			return plan, err
		}
		for _, record := range records {
			if previous, ok := prior[importUsageKey(record)]; ok {
				if record.ObservedAt.Before(previous.ObservedAt) || (record.ObservedAt.Equal(previous.ObservedAt) &&
					(record.Quantity != previous.Quantity || record.CostMillicents != previous.CostMillicents)) {
					return plan, ErrConflict
				}
				if previous.CostMillicents > math.MaxInt64-plan.Result.PreviousCostMillicents {
					return plan, ErrInvalid
				}
				plan.Result.PreviousCostMillicents += previous.CostMillicents
				delete(prior, importUsageKey(record))
			}
			if record.CostMillicents > math.MaxInt64-plan.Result.ImportedCostMillicents {
				return plan, ErrInvalid
			}
			plan.Result.ImportedCostMillicents += record.CostMillicents
			plan.After = append(plan.After, record)
		}
	}
	// Reject incomplete or incompatible existing windows rather than dropping
	// old meters or hiding overlapping periods in an operator import.
	if len(prior) != 0 {
		return plan, ErrConflict
	}
	plan.Result.CostDeltaMillicents = plan.Result.ImportedCostMillicents - plan.Result.PreviousCostMillicents
	plan.Result.CollectedFrom, plan.Result.CollectedUntil, plan.Result.ObservedAt = progress.CollectedFrom, progress.CollectedUntil, progress.ObservedAt
	// UpdatedAt is a local write timestamp, not accounting evidence.
	progress.UpdatedAt = time.Time{}
	plan.Progress = progress
	request := command.Request
	request.ExpectedRevision = ""
	plan.Result.Revision = importHash(struct {
		Request       UsageImportRequest
		Actor         string
		Policy        UsagePolicy
		Database      Database
		Progress      UsageProgress
		Before, After []UsageRecord
	}{request, command.ActorID, command.Policy, database, progress, existing, plan.After})
	return plan, nil
}

func importUsageKey(r UsageRecord) usageKey {
	return usageKey{databaseID: r.DatabaseID, from: r.WindowFrom.UTC(), to: r.WindowTo.UTC(), meter: r.Meter}
}
