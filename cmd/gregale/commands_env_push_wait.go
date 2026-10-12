package main

import (
	"context"
	"time"
)

func finishEnvPushWait(ctx context.Context, client *Client, receipt envPushReceipt, restart bool, timeout time.Duration) int {
	progress := secretsSetReceipt{App: receipt.AppSlug, Scope: receipt.Scope, Keys: receipt.UpdatedKeys, WaitForAckRequested: true, AcknowledgementStatus: "unconfirmed"}
	syncReceipt := func() {
		receipt.WaitForAckRequested = true
		receipt.AcknowledgementStatus = progress.AcknowledgementStatus
		receipt.AcknowledgedKeys = progress.AcknowledgedKeys
		receipt.ObservedDeliveryVersions = progress.ObservedDeliveryVersions
		receipt.InstanceID, receipt.WakeID = progress.InstanceID, progress.WakeID
		receipt.RestartRequested = progress.RestartRequested
		receipt.Warnings = progress.Warnings
		if progress.RestartRequested {
			receipt.ApplyMode = "fresh_restart_requested"
		}
	}
	fail := func(title string, err error) int {
		syncReceipt()
		receipt.Result = "acknowledgement_failed"
		if title == "Restart failed" || title == "Restart identifier missing" {
			receipt.Result = "restart_failed"
		}
		return envPushFailure(receipt, title, err)
	}
	emit := func() int { syncReceipt(); return jsonOut(writeJSON(receipt)) }
	return runSecretUpdateWait(ctx, client, receipt.AppSlug, receipt.Scope, receipt.UpdatedKeys, restart, timeout, &progress, fail, emit)
}
