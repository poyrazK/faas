package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const secretAckPollInterval = 2 * time.Second

func waitForSecretRevocationAck(ctx context.Context, client *api.Client, app, revocationID string) (api.AppSecretRevocationResponse, error) {
	ticker := time.NewTicker(secretAckPollInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return api.AppSecretRevocationResponse{}, fmt.Errorf("%s while waiting for secret removal acknowledgements: %w", secretAckWaitStopped(ctx), err)
		}
		progress, err := client.GetSecretRevocation(ctx, app, revocationID)
		if err != nil {
			return api.AppSecretRevocationResponse{}, fmt.Errorf("read secret revocation status: %w", err)
		}
		switch progress.Status {
		case "complete":
			return progress, nil
		case "blocked", "failed":
			return progress, fmt.Errorf("secret removal acknowledgement %s (%d of %d runtime(s) acknowledged; %d pending)", progress.Status, progress.AcknowledgedCount, progress.TargetCount, progress.PendingCount)
		}
		select {
		case <-ctx.Done():
			return progress, fmt.Errorf("%s waiting for secret removal acknowledgements (%d of %d runtime(s) acknowledged; %d pending)", secretAckWaitStopped(ctx), progress.AcknowledgedCount, progress.TargetCount, progress.PendingCount)
		case <-ticker.C:
		}
	}
}

func secretAckWaitStopped(ctx context.Context) string {
	if ctx.Err() == context.DeadlineExceeded {
		return "timed out"
	}
	return "stopped"
}

func waitForSecretApplicationAck(ctx context.Context, client *api.Client, app, key, scope string) (int, error) {
	return waitForSecretApplicationAckWithRestart(ctx, client, app, key, scope, "")
}

func waitForSecretApplicationAckAfterRestart(ctx context.Context, client *api.Client, app, key, scope, restartInstanceID string) (int, error) {
	if restartInstanceID == "" {
		return 0, fmt.Errorf("restart reached a running instance without an instance ID")
	}
	return waitForSecretApplicationAckWithRestart(ctx, client, app, key, scope, restartInstanceID)
}

func waitForSecretApplicationAckWithRestart(ctx context.Context, client *api.Client, app, key, scope, restartInstanceID string) (int, error) {
	ticker := time.NewTicker(secretAckPollInterval)
	defer ticker.Stop()
	pending, targetCount := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return targetCount, secretAckTimeoutError(ctx, targetCount, pending)
		}
		list, err := client.ListSecretsWithScope(ctx, app, scope)
		if err != nil {
			return targetCount, fmt.Errorf("read current secret status: %w", err)
		}
		secret, ok := findSecretStatus(list, key, scope)
		if !ok {
			return targetCount, fmt.Errorf("rotated secret %s/%s was not present in the status response", scopeOrDefault(scope), key)
		}
		if !secret.RuntimeReloadTargetsComplete {
			return targetCount, fmt.Errorf("server did not provide a complete authorized runtime roster; upgrade the control plane before waiting for acknowledgements")
		}
		targetCount, pending, err = secretAckProgressWithRestart(secret, restartInstanceID)
		if err != nil {
			return targetCount, err
		}
		if restartInstanceID != "" && targetCount == 0 {
			return targetCount, fmt.Errorf("restart reached a running instance, but no active runtime is authorized for secret %s/%s; check the deployment scope and secret allowlist", scopeOrDefault(scope), key)
		}
		if pending == 0 {
			return targetCount, nil
		}
		select {
		case <-ctx.Done():
			return targetCount, secretAckTimeoutError(ctx, targetCount, pending)
		case <-ticker.C:
		}
	}
}

func findSecretStatus(list api.AppSecretListResponse, key, scope string) (api.AppSecretResponse, bool) {
	for _, secret := range list.Secrets {
		if secret.Key == key && scopeOrDefault(secret.Scope) == scopeOrDefault(scope) {
			return secret, true
		}
	}
	return api.AppSecretResponse{}, false
}

func secretAckProgress(secret api.AppSecretResponse) (targetCount, pending int, err error) {
	return secretAckProgressWithRestart(secret, "")
}

func secretAckProgressWithRestart(secret api.AppSecretResponse, restartInstanceID string) (targetCount, pending int, err error) {
	afterRestart := restartInstanceID != ""
	for _, target := range secret.RuntimeReloadObservations {
		targetCount++
		if target.ApplicationAckVersion == secret.DeliveryVersion {
			switch target.ApplicationAck {
			case "applied":
				continue
			case "failed":
				if afterRestart && target.InstanceID != restartInstanceID {
					pending++
					continue
				}
				return targetCount, pending, fmt.Errorf("%s reported that it could not apply the rotated secret", secretRuntimeTargetName(target))
			}
		}
		if target.ReloadSupport != "enabled" {
			if afterRestart {
				pending++
				continue
			}
			return targetCount, pending, fmt.Errorf("%s has reload support %s; opt in with com.gregale.secret-reload-signal or use --restart", secretRuntimeTargetName(target), target.ReloadSupport)
		}
		if target.Reported && target.Version == secret.DeliveryVersion &&
			(target.Projection == "failed" || target.Signal == "failed") {
			if afterRestart && target.InstanceID != restartInstanceID {
				pending++
				continue
			}
			return targetCount, pending, fmt.Errorf("%s could not deliver the rotated secret to the application", secretRuntimeTargetName(target))
		}
		pending++
	}
	return targetCount, pending, nil
}

func secretRuntimeTargetName(target api.SecretRuntimeReloadObservation) string {
	if target.WorkloadName == "" {
		return "runtime " + target.InstanceID
	}
	return fmt.Sprintf("sidecar %s in runtime %s", target.WorkloadName, target.InstanceID)
}

func secretAckTimeoutError(ctx context.Context, targetCount, pending int) error {
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out waiting for app-applied acknowledgement (%d of %d active authorized runtime(s) still pending)", pending, targetCount)
	}
	return fmt.Errorf("stopped waiting for app-applied acknowledgements (%d of %d active authorized runtime(s) still pending)", pending, targetCount)
}
