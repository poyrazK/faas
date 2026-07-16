// Command gatewayd — edge proxy: TLS, routing, wake-blocking, rate limits (spec §4.1)
//
// See docs/faas_implementation_spec.md for this daemon's ownership boundary.
// Do not add a call that bypasses another component's owner (spec §Component
// ownership). Real logic lands in M4.
package main

import "github.com/onebox-faas/faas/pkg/wire"

func main() {
	wire.Daemon("gatewayd", wire.StubRun("M4"))
}
