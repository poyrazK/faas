package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/outbound"
)

// capabilityTools are the capability gateway's built-in tools
// (ADR-20261027090016 §1). Each reads its own switch; a tool that is off is
// never listed.
//
// FAAS_OUTBOUND_AGENT_WEB_FETCH=1 lists web_fetch (ADR-20261027090016
// amendment 1). Its fetches leave from this host, so it stays off in
// production until they leave from an agent egress address.
func capabilityTools(log *slog.Logger) []outbound.CapabilityTool {
	webFetch := &outbound.WebFetch{On: strings.TrimSpace(os.Getenv("FAAS_OUTBOUND_AGENT_WEB_FETCH")) == "1"}
	if webFetch.On {
		log.Info("capability gateway: web_fetch is on")
	}
	return []outbound.CapabilityTool{webFetch}
}
