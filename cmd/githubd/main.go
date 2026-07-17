// Command githubd — GitHub App integration daemon (spec §14 M7.5, ADR-012).
//
// githubd owns: push-webhook receiver, Checks-API status writer, OAuth
// callback handler, per-repo install-token cache. It is the SOLE outbound
// caller to api.github.com (Checks + install-token exchange); its inbound
// public surface is gatewayd at /webhooks/github (HMAC-verified at the
// edge). It talks to apid over gRPC on /run/faas/githubd.sock
// (ADR-015 unix-socket DAC; apid is the only caller in v1.0).
//
// Slice 1 ships the stub daemon so the gRPC contract surface can land
// without committing to a business-logic shape; slices 7-8 wire the real
// Service impl. Replace wire.StubRun("M7.5") in slice 7.
package main

import "github.com/onebox-faas/faas/pkg/wire"

func main() {
	wire.Daemon("githubd", wire.StubRun("M7.5"))
}
