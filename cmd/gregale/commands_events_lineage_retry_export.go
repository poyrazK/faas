package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsLineageRetryExport(args []string) int {
	flags, positional := splitArgsForFlags(args, "raw")
	fs := newFlagSet("subscription-lineage-retry-export", flag.ContinueOnError)
	limit := fs.Int("limit", api.EventExecutionLineageCasesPageDefault, "maximum selected cases (maximum 200)")
	after := fs.Int64("after", 0, "exclusive outbox cursor")
	reason := fs.String("reason", "", "exact reason code")
	rawOutput := fs.Bool("raw", false, "export a legacy unscoped API request body")
	path := fs.String("file", "", "new JSON request path; existing files are never overwritten")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || *path == "" || *after < 0 || *limit < 1 || *limit > api.EventExecutionLineageRetryJobMaxCases {
		PrintUsage(os.Stderr, "usage: gregale event subscription-lineage-retry-export APP SUB --file PATH [--reason CODE] [--limit N] [--cursor ID] [--raw]", "events")
		return 1
	}
	sub, err := uuid.Parse(positional[1])
	if err != nil || sub == uuid.Nil {
		return printErr("Invalid subscription", fmt.Errorf("subscription ID must be a nonzero UUID"))
	}
	positional[1] = sub.String()
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.PreviewEventExecutionLineageRetries(context.Background(), positional[0], positional[1], *reason, *after, *limit)
	if err != nil {
		return printErr("Lineage retry preview failed", err)
	}
	req, reasons, err := lineageRetryExportRequest(preview, positional[1], *after, *limit, *reason)
	if err != nil {
		return printErr("Invalid lineage retry preview", err)
	}
	appID := preview.Cases[0].AppID
	format := lineageRetryBundleFormat
	if *rawOutput {
		format = "raw"
		err = writeLineageRetryExport(*path, req)
	} else {
		err = writeScopedLineageRetryExport(*path, req, appID, positional[1], nil)
	}
	if err != nil {
		return printErr("Lineage retry export failed", err)
	}
	summary := struct {
		Format         string         `json:"format"`
		AppID          string         `json:"app_id"`
		File           string         `json:"file"`
		RequestID      string         `json:"request_id"`
		App            string         `json:"app"`
		SubscriptionID string         `json:"subscription_id"`
		Selected       int            `json:"selected"`
		Reasons        map[string]int `json:"reasons"`
		NextAfter      int64          `json:"next_after,omitempty"`
	}{format, appID, *path, req.RequestID, positional[0], positional[1], len(req.Cases), reasons, preview.NextAfter}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Saved %d cases to %s | %s | request %s | app %s (%s) | subscription %s\n", len(req.Cases), oneLine(*path), format, req.RequestID, oneLine(positional[0]), appID, positional[1])
	keys := make([]string, 0, len(reasons))
	for key := range reasons {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = fmt.Fprintf(osStdout, "Reason %s: %d\n", oneLine(key), reasons[key])
	}
	if preview.NextAfter > 0 {
		_, _ = fmt.Fprintf(osStdout, "More cases may be available: --after %d\n", preview.NextAfter)
	}
	_, _ = fmt.Fprintln(osStdout, "Review the file before submitting with subscription-lineage-retry-job-create. Reuse this file after a timeout.")
	return 0
}

func lineageRetryExportRequest(preview api.EventExecutionLineageCases, sub string, after int64, limit int, reason string) (api.CreateEventExecutionLineageRetryJobRequest, map[string]int, error) {
	req := api.CreateEventExecutionLineageRetryJobRequest{RequestID: uuid.NewString()}
	reasons := make(map[string]int)
	if len(preview.Cases) == 0 || len(preview.Cases) > limit {
		return req, nil, fmt.Errorf("preview must contain between 1 and %d cases", limit)
	}
	appID := ""
	seen := make(map[int64]bool)
	for _, c := range preview.Cases {
		app, err := uuid.Parse(c.AppID)
		if err != nil || app == uuid.Nil || (appID != "" && appID != app.String()) {
			return req, nil, fmt.Errorf("preview cases must share a valid app identity")
		}
		appID = app.String()

		if c.SubscriptionID != sub || c.OutboxID <= after || seen[c.OutboxID] || c.Revision <= 0 || c.AcceptedAt.IsZero() || (c.Status != "unresolved" && c.Status != "conflict") || (reason != "" && c.Reason != reason) {
			return req, nil, fmt.Errorf("case %d has invalid selection identity or filters", c.OutboxID)
		}
		if c.DiagnosticID != "" {
			id, err := uuid.Parse(c.DiagnosticID)
			if err != nil || id == uuid.Nil {
				return req, nil, fmt.Errorf("case %d has invalid diagnostic identity", c.OutboxID)
			}
		}
		seen[c.OutboxID] = true
		reasons[c.Reason]++
		req.Cases = append(req.Cases, api.EventExecutionLineageRetrySelection{OutboxID: c.OutboxID, ExpectedRevision: c.Revision, AcceptedAt: c.AcceptedAt, ExpectedDiagnosticID: c.DiagnosticID})
	}
	return req, reasons, nil
}

func writeLineageRetryExport(path string, req api.CreateEventExecutionLineageRetryJobRequest) error {
	return writeLineageRetryFile(path, req)
}

func writeLineageRetryFile(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > api.EventExecutionLineageRetryJobBodyMaxBytes {
		return fmt.Errorf("request exceeds %d bytes", api.EventExecutionLineageRetryJobBodyMaxBytes)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := file.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
