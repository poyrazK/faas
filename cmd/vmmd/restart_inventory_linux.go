//go:build linux

// adr: 398
// adr: 404
package main

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

func recoverRestartResources(ctx context.Context, mgr *fcvm.Manager, jailRoot, journalDir string, log *slog.Logger) (*fcvm.ResourceJournal, error) {
	journal, err := fcvm.OpenResourceJournal(journalDir)
	if err != nil {
		return nil, err
	}
	if err = mgr.WithResourceJournal(journal); err != nil {
		_ = journal.Close()
		return nil, err
	}
	rep, err := mgr.RecoverRestartQuarantine(ctx, jailRoot)
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	log.Info("vmmd: restart resource quarantine", "slots", rep.Slots,
		"instances", rep.Instances, "processes", rep.Processes,
		"journal_records", rep.JournalRecords, "journal_process_matches", rep.JournalProcessMatches,
		"reclaimed_prepared_records", rep.ReclaimedPreparedRecords,
		"ownership_reconciliation_required", rep.Instances > 0 || rep.Slots > 0)
	return journal, nil
}
