// adr: 118 — trigger dispatch responses are bounded control-plane inputs.

package sched_test

import (
	"github.com/onebox-faas/faas/pkg/sched"
	"strings"
	"testing"
)

func TestReadGatewayDispatchResponseRejectsOversizedBody(t *testing.T) {
	if _, err := sched.ReadGatewayDispatchResponse(strings.NewReader(strings.Repeat("x", sched.GatewayDispatchResponseMaxBytes+1))); err == nil {
		t.Fatal("readGatewayDispatchResponse unexpectedly accepted an oversized body")
	}
}
