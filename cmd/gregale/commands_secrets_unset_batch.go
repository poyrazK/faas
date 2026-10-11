package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type secretBatchRemoval struct {
	Key               string `json:"key"`
	RevocationID      string `json:"revocation_id"`
	Status            string `json:"revocation_status"`
	TargetCount       int    `json:"target_count"`
	AcknowledgedCount int    `json:"acknowledged_count"`
	PendingCount      int    `json:"pending_count"`
}

type secretsUnsetBatchReceipt struct {
	App                 string               `json:"app"`
	Scope               string               `json:"scope"`
	Status              string               `json:"status"`
	RequestedKeys       []string             `json:"requested_keys"`
	RemovedKeys         []string             `json:"removed_keys"`
	Removals            []secretBatchRemoval `json:"removals"`
	UnconfirmedKey      string               `json:"unconfirmed_key,omitempty"`
	RestartRequested    bool                 `json:"restart_requested"`
	WakeID              string               `json:"wake_id,omitempty"`
	WaitForAckRequested bool                 `json:"wait_for_ack_requested"`
}

func batchRemoval(key string, row api.AppSecretRevocationResponse) secretBatchRemoval {
	return secretBatchRemoval{key, row.ID, row.Status, row.TargetCount, row.AcknowledgedCount, row.PendingCount}
}

func executeSecretsUnsetBatch(client *Client, app string, keys []string, scope string, restart, wait bool, timeout time.Duration) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	receipt := secretsUnsetBatchReceipt{App: app, Scope: scopeOrDefault(scope), Status: "removing", RequestedKeys: keys, RemovedKeys: []string{}, Removals: []secretBatchRemoval{}, WaitForAckRequested: wait}
	fail := func(title string, err error) int {
		if errors.Is(ctx.Err(), context.Canceled) {
			err = fmt.Errorf("%s: %w", err, context.Canceled)
		}
		receipt.Status = "incomplete"
		if jsonOutput {
			if code := jsonOut(writeJSON(receipt)); code != 0 {
				return code
			}
		}
		if len(receipt.RemovedKeys) == 0 {
			return printErr(title, err)
		}
		return printConfigurationPartialError(title, err, app, scope, "Secret removal saved for the confirmed keys; remaining steps are unconfirmed.", receipt.RemovedKeys, receipt.WakeID)
	}
	for _, key := range keys {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		row, err := client.UnsetSecretWithScopeAndStatus(readCtx, app, key, scope)
		cancel()
		if err != nil {
			receipt.UnconfirmedKey = key
			return fail("Removal of "+key+" was not confirmed", err)
		}
		receipt.RemovedKeys = append(receipt.RemovedKeys, key)
		receipt.Removals = append(receipt.Removals, batchRemoval(key, row))
		if !jsonOutput {
			PrintOK(osStdout, "%s unset (scope=%s, revocation=%s)", key, scopeOrDefault(scope), row.ID)
		}
	}
	if wait {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if restart {
		restartCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		out, err := client.RestartAppFresh(restartCtx, app)
		cancel()
		if err != nil {
			return fail("Secrets removed, but restart was not confirmed", err)
		}
		receipt.RestartRequested, receipt.WakeID = true, out.WakeID
		if out.WakeID == "" {
			return fail("Restart identifier missing", errors.New("restart was accepted without a wake identifier; inspect current operations before retrying"))
		}
	}
	if wait {
		for i, row := range receipt.Removals {
			if row.RevocationID == "" {
				return fail("Removal acknowledgement unavailable", fmt.Errorf("%s has no revocation identifier", row.Key))
			}
			progress, err := waitForSecretRevocationAck(ctx, client, app, row.RevocationID)
			if progress.ID != "" {
				receipt.Removals[i] = batchRemoval(row.Key, progress)
			}
			if err != nil {
				return fail("Removal acknowledgement incomplete for "+row.Key, err)
			}
			if !jsonOutput {
				PrintOK(osStdout, "%s: all %d authorized runtime(s) acknowledged removal", row.Key, progress.AcknowledgedCount)
			}
		}
	}
	receipt.Status = "deleted"
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	if restart {
		PrintOK(osStdout, "One restart requested after all removals (wake_id=%s)", receipt.WakeID)
	} else {
		PrintWarn(osStdout, "Running instances may retain removed secrets until reload or their next cold wake.")
	}
	printSecretUpdateNextSteps(app, scope, receipt.WakeID, keys...)
	return 0
}
