// adr: 118 — trigger dispatch responses are bounded control-plane inputs.

package sched

import (
	"strings"
	"testing"
)

func TestReadGatewayDispatchResponseRejectsOversizedBody(t *testing.T) {
	if _, err := readGatewayDispatchResponse(strings.NewReader(strings.Repeat("x", gatewayDispatchResponseMaxBytes+1))); err == nil {
		t.Fatal("readGatewayDispatchResponse unexpectedly accepted an oversized body")
	}
}
