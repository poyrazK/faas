package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const lineageControlHistoryExportFormat = "gregale.event-lineage-control-history"

// This local artifact is evidence, not a request that can be submitted for replay.
type lineageControlHistoryExport struct {
	Format            string                                            `json:"format"`
	Version           int                                               `json:"version"`
	App               string                                            `json:"app"`
	AppID             string                                            `json:"app_id"`
	SubscriptionID    string                                            `json:"subscription_id"`
	JobRequestID      string                                            `json:"job_request_id"`
	ExpectedCreatedAt time.Time                                         `json:"expected_created_at"`
	JobRetainUntil    time.Time                                         `json:"job_retain_until"`
	ObservedAt        time.Time                                         `json:"observed_at"`
	Filters           api.EventExecutionLineageRetryJobControlFilters   `json:"filters"`
	ObservedReceipts  int                                               `json:"observed_receipts"`
	ThroughSequence   int64                                             `json:"through_sequence"`
	Controls          []api.EventExecutionLineageRetryJobControlReceipt `json:"controls"`
}

func cmdEventsLineageControlHistoryExport(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("subscription-lineage-retry-job-controls-export", flag.ContinueOnError)
	file := fs.String("file", "", "new private history JSON file; never overwrite")
	expected := fs.String("expected-created-at", "", "required original job creation timestamp")
	action := fs.String("action", "", "exact action: pause, resume or cancel")
	actor := fs.String("actor-id", "", "exact actor UUID or store")
	credential := fs.String("credential-type", "", "exact original credential type")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum export duration")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 3 || rejectUnexpectedFlagArgs(fs) || *file == "" {
		PrintUsage(os.Stderr, "usage: gregale events subscription-lineage-retry-job-controls-export APP SUB REQUEST_UUID --expected-created-at TIMESTAMP --file PATH [--action ACTION] [--actor-id UUID_OR_STORE] [--credential-type TYPE] [--timeout 2m]", "events")
		return 1
	}
	sub, subErr := uuid.Parse(positional[1])
	id, idErr := uuid.Parse(positional[2])
	created, timeErr := time.Parse(time.RFC3339Nano, *expected)
	if subErr != nil || idErr != nil || sub == uuid.Nil || id == uuid.Nil || timeErr != nil || created.IsZero() || *timeout <= 0 {
		return printErr("Invalid history export guards", fmt.Errorf("subscription/job IDs must be nonzero UUIDs, creation timestamp must be RFC3339, and timeout must be positive"))
	}
	filter, err := (api.EventExecutionLineageRetryJobControlFilters{Action: *action, ActorID: *actor, CredentialType: *credential}).Normalize()
	if err != nil {
		return printErr("Invalid history filters", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	return exportLineageControlHistory(ctx, client, positional[0], sub.String(), id.String(), created, *file, filter)
}

func exportLineageControlHistory(ctx context.Context, client *api.Client, app, sub, id string, expected time.Time, path string, filter api.EventExecutionLineageRetryJobControlFilters) int {
	job, err := readLineageControlExportJob(ctx, client, app, sub, id, expected)
	if err != nil {
		return lineageControlExportError(ctx, "History export job unavailable", err)
	}
	// The whole unfiltered journal fits in one guarded GET. Selecting locally also
	// avoids relying on filter support in older API readers during rolling upgrades.
	readCtx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	history, err := client.ListEventExecutionLineageRetryJobControls(readCtx, app, sub, id, expected, 0, api.EventExecutionLineageRetryControlPageMax)
	cancel()
	if err != nil {
		return lineageControlExportError(ctx, "Control history unavailable", err)
	}
	bundle, err := buildLineageControlHistoryExport(job, history, app, filter, time.Now().UTC())
	if err != nil {
		return lineageControlExportError(ctx, "Invalid control history", err)
	}
	if err = writeLineageControlHistoryExport(ctx, path, bundle); err != nil {
		return lineageControlExportError(ctx, "Control history export failed", err)
	}
	summary := struct {
		Format            string                                          `json:"format"`
		File              string                                          `json:"file"`
		AppID             string                                          `json:"app_id"`
		SubscriptionID    string                                          `json:"subscription_id"`
		JobRequestID      string                                          `json:"job_request_id"`
		ExpectedCreatedAt time.Time                                       `json:"expected_created_at"`
		ObservedReceipts  int                                             `json:"observed_receipts"`
		ExportedReceipts  int                                             `json:"exported_receipts"`
		ThroughSequence   int64                                           `json:"through_sequence"`
		Filters           api.EventExecutionLineageRetryJobControlFilters `json:"filters"`
	}{bundle.Format, path, bundle.AppID, sub, id, expected, bundle.ObservedReceipts, len(bundle.Controls), bundle.ThroughSequence, filter}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Saved %d of %d observed control receipts to %s | job %s | through sequence %d\n", len(bundle.Controls), bundle.ObservedReceipts, oneLine(path), id, bundle.ThroughSequence)
	return 0
}

func readLineageControlExportJob(ctx context.Context, client *api.Client, app, sub, id string, expected time.Time) (api.EventExecutionLineageRetryJob, error) {
	readCtx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	defer cancel()
	job, err := client.GetEventExecutionLineageRetryJob(readCtx, app, sub, id)
	if err != nil {
		return job, err
	}
	if !sameBindingDeployment(job.RequestID, id) || !sameBindingDeployment(job.SubscriptionID, sub) || !job.CreatedAt.Equal(expected) {
		return job, fmt.Errorf("job identity changed; use its reviewed original creation timestamp")
	}
	_, err = summarizeLineageRetryWatch(job)
	return job, err
}

func lineageControlExportError(ctx context.Context, message string, err error) int {
	code := printErr(message, err)
	if errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	return code
}
