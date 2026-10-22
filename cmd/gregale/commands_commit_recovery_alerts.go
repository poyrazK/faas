package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// Queue operations are entirely local and share watch checkpoint ownership.
func cmdCommitRecoveryAlerts(args []string) int {
	fs := newFlagSet("commit recovery-alerts", flag.ContinueOnError)
	path := fs.String("checkpoint", "", "saved recovery watch checkpoint")
	acks := fs.String("ack-alerts", "", "exact incident_id/sequence acknowledgements JSON")
	replay := fs.Bool("replay", false, "emit pending historical alerts as JSON lines")
	flags, positional := splitArgsForFlags(args, "replay")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || len(positional) != 0 || *path == "" || (*replay && *acks != "") {
		return printErr("Invalid alert queue arguments", fmt.Errorf("require --checkpoint; use --replay or --ack-alerts separately"))
	}
	protected := []string{}
	if *acks != "" {
		protected = append(protected, *acks)
	}
	checkpoint, err := newCommitRecoveryCheckpoint(*path, protected...)
	if err != nil {
		return printErr("Cannot use recovery checkpoint", err)
	}
	if err = checkpoint.lock(); err != nil {
		return printErr("Cannot own recovery checkpoint", err)
	}
	defer func() { _ = checkpoint.close() }()
	return manageCommitRecoveryAlerts(checkpoint, *acks, *replay)
}

func manageCommitRecoveryAlerts(checkpoint *commitRecoveryCheckpoint, ackPath string, replay bool) int {
	var status api.CommitRecoveryStatusResult
	if err := readCommitRecoveryStatusFile(checkpoint.path, &status); err != nil {
		return printErr("Cannot read recovery checkpoint", err)
	}
	var acknowledgements []api.CommitRecoveryAlertAcknowledgement
	if ackPath != "" {
		if err := readCommitRecoveryStatusFile(ackPath, &acknowledgements); err != nil {
			return printErr("Cannot read alert acknowledgements", err)
		}
	}
	updated, err := api.AcknowledgeCommitRecoveryAlerts(status, acknowledgements)
	if err != nil {
		return printErr("Invalid recovery alert queue", err)
	}
	if ackPath != "" {
		if err = checkpoint.save(updated); err != nil {
			return printErr("Cannot save alert acknowledgements", err)
		}
	}
	return printCommitRecoveryAlertQueue(updated.Monitoring.AlertOutbox, replay)
}

func printCommitRecoveryAlertQueue(outbox *api.CommitRecoveryAlertOutbox, replay bool) int {
	encoder := json.NewEncoder(os.Stdout)
	if replay {
		for _, event := range outbox.Pending {
			if err := encoder.Encode(event); err != nil {
				return printErr("Cannot replay alert", err)
			}
		}
		return 0
	}
	used := len(outbox.Pending) + len(outbox.Active)
	summary := struct {
		Outbox    *api.CommitRecoveryAlertOutbox `json:"alert_outbox"`
		Pending   int                            `json:"pending_count"`
		Reserved  int                            `json:"reserved_closing_slots"`
		Remaining int                            `json:"remaining_slots"`
		Capacity  int                            `json:"capacity"`
	}{outbox, len(outbox.Pending), len(outbox.Active), api.CommitRecoveryAlertQueueMax - used, api.CommitRecoveryAlertQueueMax}
	if err := encoder.Encode(summary); err != nil {
		return printErr("Cannot print alert queue", err)
	}
	return 0
}
