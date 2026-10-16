//go:build !no_pg

package main

import "testing"

func TestPgStandardLogConsumerClosureRunJoinsBeforeShutdown(t *testing.T) {
	standardLogConsumerClosureRun(t, inventoryGatewayPGStore(t), nil)
}
