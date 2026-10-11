package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func prepareDeliveryHealthCheckpoint(f *deliveryHealthWatchFlags, options *api.EventDeliveryHealthWatchOptions) (*commitRecoveryCheckpoint, error) {
	if *f.checkpoint == "" {
		return nil, nil
	}
	checkpoint, err := newCommitRecoveryCheckpoint(*f.checkpoint)
	if err != nil {
		return nil, err
	}
	if err = checkpoint.lock(); err != nil {
		return nil, err
	}
	fail := func(err error) (*commitRecoveryCheckpoint, error) { _ = checkpoint.close(); return nil, err }
	if *f.resume {
		var saved api.EventDeliveryHealthCheckpoint
		if err = readCommitRecoveryStatusFile(checkpoint.path, &saved); err != nil {
			return fail(err)
		}
		if err = api.ValidateEventDeliveryHealthCheckpoint(saved); err != nil {
			return fail(err)
		}
		options.Resume = &saved
	} else {
		if _, err = os.Lstat(checkpoint.path); err == nil {
			return fail(errors.New("checkpoint already exists; use --resume or a new path"))
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		}
	}
	options.DurableAlerts = true
	return checkpoint, nil
}

// Offline queue operations hold the same stable sidecar lock as the watch.
func cmdEventDeliveryHealthAlerts(args []string) int {
	fs := newFlagSet("events delivery-health-alerts", flag.ContinueOnError)
	path := fs.String("checkpoint", "", "saved delivery health watch checkpoint")
	ackPath := fs.String("ack-alerts", "", "exact incident_id/sequence acknowledgement JSON array")
	replay := fs.Bool("replay", false, "emit pending historical alert JSON lines")
	flags, positional := splitArgsForFlags(args, "replay")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || len(positional) != 0 || *path == "" || (*replay && *ackPath != "") {
		return printErr("Invalid alert queue arguments", errors.New("require --checkpoint; use --replay or --ack-alerts separately"))
	}
	protected := []string{}
	if *ackPath != "" {
		protected = append(protected, *ackPath)
	}
	checkpoint, err := newCommitRecoveryCheckpoint(*path, protected...)
	if err != nil {
		return printErr("Cannot use checkpoint", err)
	}
	if err = checkpoint.lock(); err != nil {
		return printErr("Cannot own checkpoint", err)
	}
	defer func() { _ = checkpoint.close() }()
	return manageDeliveryHealthAlerts(checkpoint, *ackPath, *replay)
}

func manageDeliveryHealthAlerts(checkpoint *commitRecoveryCheckpoint, ackPath string, replay bool) int {
	var saved api.EventDeliveryHealthCheckpoint
	if err := readCommitRecoveryStatusFile(checkpoint.path, &saved); err != nil {
		return printErr("Cannot read checkpoint", err)
	}
	var acks []api.EventDeliveryHealthAcknowledgement
	if ackPath != "" {
		if err := readCommitRecoveryStatusFile(ackPath, &acks); err != nil {
			return printErr("Cannot read acknowledgements", err)
		}
	}
	updated, err := api.AcknowledgeEventDeliveryHealthAlerts(saved, acks)
	if err != nil {
		return printErr("Invalid delivery health alert queue", err)
	}
	if ackPath != "" {
		if err = checkpoint.save(updated); err != nil {
			return printErr("Cannot save acknowledgements", err)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	if replay {
		for _, a := range updated.Pending {
			if err = encoder.Encode(a); err != nil {
				return printErr("Cannot replay alert", err)
			}
		}
		return 0
	}
	reserved := 0
	for _, c := range updated.Conditions {
		if c.IncidentID != "" {
			reserved++
		}
	}
	summary := struct {
		Checkpoint api.EventDeliveryHealthCheckpoint `json:"checkpoint"`
		Pending    int                               `json:"pending_count"`
		Reserved   int                               `json:"reserved_closing_slots"`
		Remaining  int                               `json:"remaining_slots"`
		Capacity   int                               `json:"capacity"`
	}{updated, len(updated.Pending), reserved, api.EventDeliveryHealthAlertQueueMax - len(updated.Pending) - reserved, api.EventDeliveryHealthAlertQueueMax}
	if err = encoder.Encode(summary); err != nil {
		return printErr("Cannot print alert queue", fmt.Errorf("encode queue: %w", err))
	}
	return 0
}
