package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const secretAckPollInterval = 2 * time.Second

func waitForSecretApplicationAck(ctx context.Context, client *api.Client, app, key, scope string) (int, error) {
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
		targetCount, pending, err = secretAckProgress(secret)
		if err != nil {
			return targetCount, err
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
	for _, target := range secret.RuntimeReloadObservations {
		targetCount++
		if target.ApplicationAckVersion == secret.DeliveryVersion {
			switch target.ApplicationAck {
			case "applied":
				continue
			case "failed":
				return targetCount, pending, fmt.Errorf("runtime %s reported that it could not apply the rotated secret", target.InstanceID)
			}
		}
		if target.ReloadSupport != "enabled" {
			return targetCount, pending, fmt.Errorf("runtime %s has reload support %s; redeploy with com.gregale.secret-reload-signal or use --restart", target.InstanceID, target.ReloadSupport)
		}
		if target.Reported && target.Version == secret.DeliveryVersion &&
			(target.Projection == "failed" || target.Signal == "failed") {
			return targetCount, pending, fmt.Errorf("runtime %s could not deliver the rotated secret to the application", target.InstanceID)
		}
		pending++
	}
	return targetCount, pending, nil
}

func secretAckTimeoutError(ctx context.Context, targetCount, pending int) error {
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out waiting for app-applied acknowledgement (%d of %d active authorized runtime(s) still pending)", pending, targetCount)
	}
	return fmt.Errorf("stopped waiting for app-applied acknowledgements (%d of %d active authorized runtime(s) still pending)", pending, targetCount)
}
