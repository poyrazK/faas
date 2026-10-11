package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type secretSetWaitOptions struct {
	enabled bool
	timeout time.Duration
}

func finishSecretSetWait(parent context.Context, client *Client, app, scope string, keys []string, restart bool, timeout time.Duration) int {
	receipt := secretsSetReceipt{App: app, Status: "updated", Scope: scopeOrDefault(scope), Keys: keys, WaitForAckRequested: true, AcknowledgementStatus: "unconfirmed"}
	fail := func(title string, err error) int {
		if jsonOutput {
			if code := jsonOut(writeJSON(receipt)); code != 0 {
				return code
			}
		}
		return printConfigurationPartialError(title, err, app, scope, "All requested secret values were saved; application acknowledgement is incomplete.", keys, receipt.WakeID)
	}
	return runSecretUpdateWait(parent, client, app, scope, keys, restart, timeout, &receipt, fail, func() int { return jsonOut(writeJSON(receipt)) })
}

// Shared wait engine keeps command-specific receipts and error reporting intact.
func runSecretUpdateWait(parent context.Context, client *Client, app, scope string, keys []string, restart bool, timeout time.Duration, receipt *secretsSetReceipt, fail func(string, error) int, emit func() int) int {
	signalCtx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	// Freeze the versions observed after saving, before any optional restart.
	// PUT does not return delivery versions, so this confirms observed metadata,
	// rather than asserting identity of plaintext values under concurrent writes.
	list, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return fail("Could not read saved secret versions", err)
	}
	versions := map[string]int64{}
	for _, key := range sortedUniqueStrings(keys) {
		secret, ok := findSecretStatus(list, key, scope)
		if !ok || secret.DeliveryVersion <= 0 {
			return fail("Could not confirm saved secret version", fmt.Errorf("no current delivery version for %s", key))
		}
		versions[key] = secret.DeliveryVersion
	}
	receipt.ObservedDeliveryVersions = versions
	if restart {
		out, err := client.RestartAppFresh(ctx, app)
		if err != nil {
			return fail("Restart failed", err)
		}
		receipt.RestartRequested, receipt.WakeID = true, out.WakeID
		if out.WakeID == "" {
			return fail("Restart identifier missing", errors.New("restart accepted without a wake_id"))
		}
		if !jsonOutput {
			PrintOK(osStdout, "Restart requested after secret update (wake_id=%s)", out.WakeID)
		}
		instance, err := waitForAppWake(ctx, client, app, out.WakeID, timeout, 250*time.Millisecond)
		if err != nil {
			return fail("Waiting for restarted app failed", err)
		}
		if instance.ID == "" {
			return fail("Restart instance missing", errors.New("running instance did not include an ID"))
		}
		receipt.InstanceID = instance.ID
		if !jsonOutput {
			PrintOK(osStdout, "Restart reached running (wake_id=%s, instance_id=%s)", out.WakeID, instance.ID)
		}
	}
	if !jsonOutput {
		_, _ = fmt.Fprintln(osStdout, "Waiting for application acknowledgements of the versions observed after saving; delivery alone is not acknowledgement.")
	}
	ticker := time.NewTicker(secretAckPollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return fail("Waiting for application acknowledgements failed", ctx.Err())
		}
		list, err := client.ListSecretsWithScope(ctx, app, scope)
		if err != nil {
			return fail("Waiting for application acknowledgements failed", err)
		}
		receipt.AcknowledgedKeys = nil
		complete := []string{}
		noTargets := []string{}
		for _, key := range sortedUniqueStrings(keys) {
			secret, ok := findSecretStatus(list, key, scope)
			if !ok || secret.DeliveryVersion != versions[key] {
				return fail("Secret version changed while waiting", fmt.Errorf("%s no longer has the observed delivery version; review current metadata without repeating writes", key))
			}
			if !secret.RuntimeReloadTargetsComplete {
				return fail("Incomplete runtime roster", errors.New("server did not provide a complete authorized runtime roster"))
			}
			if receipt.InstanceID != "" {
				found := false
				for _, target := range secret.RuntimeReloadObservations {
					if target.InstanceID == receipt.InstanceID {
						found = true
						break
					}
				}
				if !found {
					return fail("Restart runtime missing from roster", fmt.Errorf("the restarted instance is not in the authorized runtime roster for %s; review deployment scope and allowlist", key))
				}
			}
			targets, pending, err := secretAckProgressWithRestart(secret, receipt.InstanceID)
			if err != nil {
				return fail("Application acknowledgement failed", err)
			}
			if restart && targets == 0 {
				return fail("No authorized runtime after restart", fmt.Errorf("no active runtime is authorized for %s; check deployment scope and allowlist", key))
			}
			if targets == 0 {
				noTargets = append(noTargets, key)
			} else if pending == 0 {
				complete = append(complete, key)
				receipt.AcknowledgedKeys = complete
			}
		}
		receipt.AcknowledgedKeys = complete
		if len(complete)+len(noTargets) == len(versions) {
			receipt.AcknowledgementStatus = "confirmed"
			if len(noTargets) > 0 {
				receipt.AcknowledgementStatus = "no_active_targets"
				receipt.Warnings = []string{fmt.Sprintf("No active authorized runtimes for %v; no application acknowledgement was observed for these keys. Values remain available for future cold wakes.", noTargets)}
			}
			if jsonOutput {
				return emit()
			}
			if len(complete) > 0 {
				PrintOK(osStdout, "Application acknowledgement confirmed for %d of %d updated keys at the observed versions", len(complete), len(versions))
			} else {
				PrintWarn(osStdout, "No application acknowledgements were observed")
			}
			for _, warning := range receipt.Warnings {
				PrintWarn(osStdout, "%s", warning)
			}
			command := fmt.Sprintf("%s secrets list --app %s --scope %s", recoveryCLI(), recoveryArgument(app), recoveryArgument(scopeOrDefault(scope)))
			for _, key := range sortedUniqueStrings(keys) {
				command += " --key " + recoveryArgument(key)
			}
			_, _ = fmt.Fprintf(osStdout, "Review current metadata (POSIX shells):\n  %s\n", command)
			return 0
		}
		select {
		case <-ctx.Done():
			return fail("Waiting for application acknowledgements failed", ctx.Err())
		case <-ticker.C:
		}
	}
}
