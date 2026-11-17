package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

type DevConfiguration = devConfiguration

var LoadDevConfigurationForTest = loadDevConfiguration
var VerifyDevConfigurationForTest = verifyDevConfiguration
var CheckDevConfigurationForTest = checkDevConfiguration

func EvaluateDevConfigurationForTest(input devConfigurationInput, metadata *api.AppSecretListResponse) *devConfiguration {
	return input.evaluate(metadata)
}

func SyncValidatedDevConfigurationForTest(ctx context.Context, client *Client, app, path string, input devConfigurationInput) error {
	state := &devEnvSyncState{}
	file := input.files[0]
	_, err := state.syncPairs(ctx, client, app, path, file.pairs, file.fingerprint, "developer config")
	return err
}
