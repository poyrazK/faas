// Command builderd — build orchestrator and ephemeral builder microVMs (spec §4.5)
//
// See docs/faas_implementation_spec.md for this daemon's ownership boundary.
// Do not add a call that bypasses another component's owner (spec §Component
// ownership). Real logic lands in M6.
package main

import "github.com/onebox-faas/faas/pkg/wire"

func main() {
	wire.Daemon("builderd", wire.StubRun("M6"))
}
