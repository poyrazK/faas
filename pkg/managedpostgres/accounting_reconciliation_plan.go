package managedpostgres

import "time"

type reconciliationCoverage struct {
	WindowSeconds                                        int64
	CollectedFrom, CollectedUntil, ObservedAt, UpdatedAt time.Time
	SourceDatabaseID                                     string
}

type reconciliationLedger struct {
	Records                                  int64
	FirstWindow, LastWindow, LastObservation time.Time
	Invalid                                  bool
}

type accountingReconciliationPlan struct {
	Result        AccountingReconciliationResult
	Before, After Database
	Coverage      []reconciliationCoverage
}

func planAccountingReconciliation(command AccountingReconciliationCommand, database Database, coverage []reconciliationCoverage, ledger reconciliationLedger, identityClaimed bool) (accountingReconciliationPlan, error) {
	var plan accountingReconciliationPlan
	r := command.Request
	if !command.Policy.Enabled {
		return plan, ErrUnsupported
	}
	if database.ID != r.DatabaseID || database.ID != command.Expected.ID || database.AccountID != command.Expected.AccountID ||
		database.BackendID != r.BackendID || database.BackendFingerprint != r.BackendFingerprint ||
		database.BackendID != command.Expected.BackendID || database.BackendFingerprint != command.Expected.BackendFingerprint ||
		database.RestoreSourceDatabaseID != command.Expected.RestoreSourceDatabaseID ||
		database.State != StateDeleted || !database.AccountingRequired || database.ProviderResourceID != "" ||
		database.LeaseUntil.After(command.Now) || identityClaimed || !validUsageWindow(command.Policy.Window) {
		return plan, ErrConflict
	}
	if r.ShutdownAt.Before(database.CreatedAt) {
		return plan, ErrInvalid
	}
	if ledger.Invalid || ledger.LastWindow.After(usageEnd(r.ShutdownAt, command.Policy.Window)) ||
		ledger.LastObservation.After(r.ObservedAt) || (ledger.Records > 0 && ledger.FirstWindow.Before(database.CreatedAt.UTC().Truncate(command.Policy.Window))) ||
		(command.SharedAccounting && ledger.Records != 0) {
		return plan, ErrConflict
	}
	for _, previous := range coverage {
		if previous.WindowSeconds != int64(command.Policy.Window/time.Second) || (!command.SharedAccounting && previous.SourceDatabaseID != "") {
			return plan, ErrConflict
		}
	}
	plan.Before, plan.After, plan.Coverage = cloneDatabase(database), cloneDatabase(database), coverage
	shutdown := r.ShutdownAt.UTC()
	plan.After.ProviderResourceID, plan.After.DeletedAt, plan.After.UpdatedAt = r.ProviderResourceID, &shutdown, command.Now.UTC().Truncate(time.Microsecond)
	plan.Result = AccountingReconciliationResult{ReconciliationID: r.ReconciliationID, DatabaseID: database.ID,
		PreviousDeletedAt: plan.Before.DeletedAt, ShutdownAt: shutdown, ObservedAt: r.ObservedAt.UTC(), SharedAccounting: command.SharedAccounting, RequiresUsageRecovery: true}
	if plan.Before.DeletedAt != nil {
		previous := plan.Before.DeletedAt.UTC()
		plan.Result.PreviousDeletedAt = &previous
	}
	r.ExpectedRevision = ""
	plan.Result.Revision = importHash(struct {
		Request  AccountingReconciliationRequest
		Actor    string
		Policy   UsagePolicy
		Shared   bool
		Database Database
		Coverage []reconciliationCoverage
		Ledger   reconciliationLedger
	}{r, command.ActorID, command.Policy, command.SharedAccounting, database, coverage, ledger})
	return plan, nil
}
