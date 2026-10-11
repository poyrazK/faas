// Command apid — control API (spec §4.2). The daemon lives in
// pkg/apid/apidserver (ADR-977); this binary only starts it.
package main

import "github.com/onebox-faas/faas/pkg/apid/apidserver"

func main() { apidserver.Main() }
