// Command apid — control API and only writer to customer-intent tables (spec §4.2)
//
// See docs/faas_implementation_spec.md for this daemon's ownership boundary.
// Do not add a call that bypasses another component's owner (spec §Component
// ownership). Real logic lands in M5.
package main

import "github.com/onebox-faas/faas/pkg/wire"

func main() {
	wire.Daemon("apid", wire.StubRun("M5"))
}
