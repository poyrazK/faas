// Command meterd — metering and billing: usage to Stripe (spec §4.7)
//
// See docs/faas_implementation_spec.md for this daemon's ownership boundary.
// Do not add a call that bypasses another component's owner (spec §Component
// ownership). Real logic lands in M7.
package main

import "github.com/onebox-faas/faas/pkg/wire"

func main() {
	wire.Daemon("meterd", wire.StubRun("M7"))
}
