package main

import (
	"log/slog"

	"github.com/onebox-faas/faas/pkg/outbound"
)

// capabilityTools are the capability gateway's built-in tools
// (ADR-20261027090016 §1). Each reads its own switch; a tool that is off is
// never listed.
func capabilityTools(_ *slog.Logger) []outbound.CapabilityTool {
	return nil
}
