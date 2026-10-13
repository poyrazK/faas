package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func buildLineageControlHistoryExport(job api.EventExecutionLineageRetryJob, history api.EventExecutionLineageRetryJobControls, app string, filter api.EventExecutionLineageRetryJobControlFilters, observed time.Time) (lineageControlHistoryExport, error) {
	out := lineageControlHistoryExport{Format: lineageControlHistoryExportFormat, Version: 1, App: app, AppID: job.AppID, SubscriptionID: job.SubscriptionID, JobRequestID: job.RequestID, ExpectedCreatedAt: job.CreatedAt, JobRetainUntil: job.RetainUntil, ObservedAt: observed, Filters: filter, ObservedReceipts: len(history.Controls), Controls: make([]api.EventExecutionLineageRetryJobControlReceipt, 0)}
	if history.NextAfter != 0 || len(history.Controls) > api.EventExecutionLineageRetryControlMaxReceipts {
		return out, fmt.Errorf("history must be a complete bounded single-read snapshot")
	}
	seen := make(map[string]bool)
	for i, receipt := range history.Controls {
		if err := validateLineageControlExportReceipt(receipt, job, int64(i+1)); err != nil {
			return out, err
		}
		command, _ := uuid.Parse(receipt.CommandID)
		if seen[command.String()] {
			return out, fmt.Errorf("duplicate control command UUID")
		}
		seen[command.String()] = true
		out.ThroughSequence = receipt.Sequence
		if lineageControlExportMatches(receipt, filter) {
			out.Controls = append(out.Controls, receipt)
		}
	}
	return out, nil
}

func validateLineageControlExportReceipt(receipt api.EventExecutionLineageRetryJobControlReceipt, job api.EventExecutionLineageRetryJob, sequence int64) error {
	command, err := uuid.Parse(receipt.CommandID)
	if err != nil || command == uuid.Nil || receipt.Sequence != sequence ||
		!sameBindingDeployment(receipt.AppID, job.AppID) || !sameBindingDeployment(receipt.SubscriptionID, job.SubscriptionID) || !sameBindingDeployment(receipt.JobRequestID, job.RequestID) ||
		!receipt.ExpectedCreatedAt.Equal(job.CreatedAt) || !receipt.RetainUntil.Equal(job.RetainUntil) ||
		receipt.Selected != len(job.Items) || receipt.Processed < 0 || receipt.Processed > receipt.Selected || receipt.JobUpdatedAt.IsZero() || receipt.RecordedAt.IsZero() || !receipt.RetainUntil.After(receipt.RecordedAt) {
		return fmt.Errorf("invalid scope, sequence or metadata for control receipt %d", sequence)
	}
	valid := receipt.Action == "pause" && receipt.ResultStatus == "paused" ||
		receipt.Action == "resume" && receipt.ResultStatus == "pending" ||
		receipt.Action == "cancel" && (receipt.ResultStatus == "cancelled" || receipt.ResultStatus == "completed")
	if !valid || (receipt.ResultStatus == "completed" && receipt.Processed != receipt.Selected) || (receipt.ResultStatus != "completed" && receipt.Processed >= receipt.Selected) {
		return fmt.Errorf("invalid original outcome for control receipt %d", sequence)
	}
	return validateLineageControlExportActor(receipt)
}

func validateLineageControlExportActor(receipt api.EventExecutionLineageRetryJobControlReceipt) error {
	if receipt.ActorKind == "" && receipt.ActorID == "" && receipt.CredentialType == "" {
		return nil
	}
	if receipt.ActorKind == "internal" && receipt.ActorID == "store" && receipt.CredentialType == "internal" {
		return nil
	}
	id, err := uuid.Parse(receipt.ActorID)
	if err == nil && id != uuid.Nil {
		if receipt.ActorKind == "account" && receipt.CredentialType == "session" {
			return nil
		}
		if receipt.ActorKind == "credential" && (receipt.CredentialType == "api_key" || receipt.CredentialType == "deploy_token" || receipt.CredentialType == "platform_tenant_token") {
			return nil
		}
	}
	return fmt.Errorf("invalid actor metadata for control receipt %d", receipt.Sequence)
}

func lineageControlExportMatches(receipt api.EventExecutionLineageRetryJobControlReceipt, filter api.EventExecutionLineageRetryJobControlFilters) bool {
	actorMatches := filter.ActorID == "" || receipt.ActorID == filter.ActorID || (filter.ActorID != "store" && sameBindingDeployment(receipt.ActorID, filter.ActorID))
	return (filter.Action == "" || receipt.Action == filter.Action) && actorMatches && (filter.CredentialType == "" || receipt.CredentialType == filter.CredentialType)
}

func writeLineageControlHistoryExport(ctx context.Context, path string, bundle lineageControlHistoryExport) error {
	return writeLineagePrivateArtifact(ctx, path, bundle, api.EventExecutionLineageRetryControlExportMaxBytes)
}

func writeLineagePrivateArtifact(ctx context.Context, path string, value any, maxBytes int) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maxBytes {
		return fmt.Errorf("artifact exceeds %d bytes", maxBytes)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Write a private temporary file in the destination directory, then publish
	// its completed inode with a no-replace hard link. Never expose a partial target.
	temp, err := os.CreateTemp(filepath.Dir(path), ".gregale-lineage-controls-*")
	if err != nil {
		return err
	}
	defer func() { _ = temp.Close(); _ = os.Remove(temp.Name()) }()
	n, err := temp.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Link(temp.Name(), path)
}
