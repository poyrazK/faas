// Command vmmd-guestdisk runs one e2fsprogs tool against a guest-written
// ext4 image inside vmmd's sandbox (ADR-20261025090000): the image owner's
// uid, private namespaces, no_new_privs, resource limits and Landlock. vmmd
// starts it for every read or edit of a VM drive, so it stays a small static
// binary that does not initialize the daemon.
package main

import (
	"os"

	"github.com/onebox-faas/faas/pkg/guestdisk"
)

func main() {
	os.Exit(guestdisk.SandboxMain(os.Args[1:]))
}
