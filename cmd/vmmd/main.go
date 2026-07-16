// Command vmmd — microVM supervisor: firecracker + jailer, only root component (spec §4.4)
//
// See docs/faas_implementation_spec.md for this daemon's ownership boundary.
// Do not add a call that bypasses another component's owner (spec §Component
// ownership). Real logic lands in M1.
package main

import "github.com/onebox-faas/faas/pkg/wire"

func main() {
	wire.Daemon("vmmd", wire.StubRun("M1"))
}
