package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func readLineageControlHistoryArtifact(path string) (lineageControlHistoryExport, error) {
	var out lineageControlHistoryExport
	if path == "" {
		return out, fmt.Errorf("a history file is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return out, err
	}
	if !info.Mode().IsRegular() || info.Size() > api.EventExecutionLineageRetryControlExportMaxBytes {
		return out, fmt.Errorf("history must be a regular file of at most %d bytes", api.EventExecutionLineageRetryControlExportMaxBytes)
	}
	file, err := openCustomerFile(path)
	if err != nil {
		return out, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, api.EventExecutionLineageRetryControlExportMaxBytes+1))
	if err != nil {
		return out, err
	}
	if len(raw) > api.EventExecutionLineageRetryControlExportMaxBytes {
		return out, fmt.Errorf("history file exceeds %d bytes", api.EventExecutionLineageRetryControlExportMaxBytes)
	}
	if err = rejectDuplicateLineageJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return out, fmt.Errorf("ambiguous history JSON: %w", err)
	}
	if err = validateLineageHistoryJSONShape(raw); err != nil {
		return out, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&out); err != nil {
		return out, err
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return out, fmt.Errorf("history must contain exactly one JSON object")
	}
	return out, validateLineageControlHistoryArtifact(&out)
}

func lineageHistoryObjectFields(raw json.RawMessage, required, optional []string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("history metadata must be a JSON object")
	}
	allowed := make(map[string]bool)
	for _, key := range append(append([]string{}, required...), optional...) {
		allowed[key] = true
	}
	for key, value := range fields {
		if !allowed[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("unknown, case-aliased or null history field %q", key)
		}
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("missing history field %q", key)
		}
	}
	return fields, nil
}

func validateLineageHistoryJSONShape(raw []byte) error {
	fields, err := lineageHistoryObjectFields(raw, []string{"format", "version", "app", "app_id", "subscription_id", "job_request_id", "expected_created_at", "job_retain_until", "observed_at", "filters", "observed_receipts", "through_sequence", "controls"}, nil)
	if err != nil {
		return err
	}
	if _, err = lineageHistoryObjectFields(fields["filters"], nil, []string{"action", "actor_id", "credential_type"}); err != nil {
		return err
	}
	var controls []json.RawMessage
	if err = json.Unmarshal(fields["controls"], &controls); err != nil {
		return err
	}
	for _, receipt := range controls {
		if _, err = lineageHistoryObjectFields(receipt, []string{"command_id", "sequence", "job_request_id", "subscription_id", "app_id", "action", "expected_created_at", "result_status", "processed", "selected", "job_updated_at", "recorded_at", "retain_until"}, []string{"actor_kind", "actor_id", "credential_type"}); err != nil {
			return err
		}
	}
	return nil
}

func validateLineageControlHistoryArtifact(out *lineageControlHistoryExport) error {
	if out.Format != lineageControlHistoryExportFormat || out.Version != 1 {
		return fmt.Errorf("unsupported control-history format or version")
	}
	if out.App == "" || out.ExpectedCreatedAt.IsZero() || out.ObservedAt.IsZero() || !out.JobRetainUntil.After(out.ExpectedCreatedAt) {
		return fmt.Errorf("invalid history scope or timestamps")
	}
	for _, id := range []string{out.AppID, out.SubscriptionID, out.JobRequestID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return fmt.Errorf("history scope IDs must be nonzero UUIDs")
		}
	}
	filter, err := out.Filters.Normalize()
	if err != nil {
		return err
	}
	out.Filters = filter
	if out.Controls == nil || out.ObservedReceipts < 0 || out.ObservedReceipts > api.EventExecutionLineageRetryControlMaxReceipts || out.ThroughSequence != int64(out.ObservedReceipts) || len(out.Controls) > out.ObservedReceipts {
		return fmt.Errorf("invalid history counts or sequence boundary")
	}
	unfiltered := filter == (api.EventExecutionLineageRetryJobControlFilters{})
	if unfiltered && len(out.Controls) != out.ObservedReceipts {
		return fmt.Errorf("unfiltered history must contain every observed receipt")
	}
	selected := 0
	if len(out.Controls) > 0 {
		selected = out.Controls[0].Selected
	}
	if selected < 0 || selected > api.EventExecutionLineageRetryJobMaxCases || (len(out.Controls) > 0 && selected == 0) {
		return fmt.Errorf("invalid receipt selection size")
	}
	job := api.EventExecutionLineageRetryJob{AppID: out.AppID, SubscriptionID: out.SubscriptionID, RequestID: out.JobRequestID, CreatedAt: out.ExpectedCreatedAt, RetainUntil: out.JobRetainUntil, Items: make([]api.EventExecutionLineageRetryJobItem, selected)}
	seen := make(map[string]bool)
	var previous int64
	for i, receipt := range out.Controls {
		if receipt.Sequence <= previous || receipt.Sequence > out.ThroughSequence || (unfiltered && receipt.Sequence != int64(i+1)) {
			return fmt.Errorf("invalid ordered receipt sequence")
		}
		if err = validateLineageControlExportReceipt(receipt, job, receipt.Sequence); err != nil {
			return err
		}
		if !lineageControlExportMatches(receipt, filter) {
			return fmt.Errorf("receipt %d does not match artifact filters", receipt.Sequence)
		}
		command, _ := uuid.Parse(receipt.CommandID)
		if seen[command.String()] {
			return fmt.Errorf("duplicate control command UUID")
		}
		seen[command.String()] = true
		previous = receipt.Sequence
	}
	return nil
}

// Canonicalize only equivalent representations for comparison; do not alter files.
func canonicalLineageControlReceipt(receipt api.EventExecutionLineageRetryJobControlReceipt) api.EventExecutionLineageRetryJobControlReceipt {
	receipt.CommandID = canonicalLineageHistoryUUID(receipt.CommandID)
	receipt.AppID = canonicalLineageHistoryUUID(receipt.AppID)
	receipt.SubscriptionID = canonicalLineageHistoryUUID(receipt.SubscriptionID)
	receipt.JobRequestID = canonicalLineageHistoryUUID(receipt.JobRequestID)
	if receipt.ActorID != "" && receipt.ActorID != "store" {
		receipt.ActorID = canonicalLineageHistoryUUID(receipt.ActorID)
	}
	receipt.ExpectedCreatedAt = receipt.ExpectedCreatedAt.UTC()
	receipt.JobUpdatedAt = receipt.JobUpdatedAt.UTC()
	receipt.RecordedAt = receipt.RecordedAt.UTC()
	receipt.RetainUntil = receipt.RetainUntil.UTC()
	return receipt
}
func canonicalLineageHistoryUUID(id string) string {
	parsed, _ := uuid.Parse(id)
	return parsed.String()
}
