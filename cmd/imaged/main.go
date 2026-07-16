// Command imaged — OCI to rootfs conversion, base images, snapshot GC (spec §4.6)
//
// See docs/faas_implementation_spec.md for this daemon's ownership boundary.
// Do not add a call that bypasses another component's owner (spec §Component
// ownership). Real logic lands in M2.
package main

import "github.com/onebox-faas/faas/pkg/wire"

func main() {
	wire.Daemon("imaged", wire.StubRun("M2"))
}
