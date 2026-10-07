//go:build !no_pg

package main

import "testing"

func TestPgStandardLogHealthProductionSender(t *testing.T) {
	standardLogHealthProductionSender(t, inventoryGatewayPGStore(t))
}
