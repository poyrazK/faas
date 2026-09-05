// Command vmmd-jail-helper sets up the jailer's private device mounts without
// initializing the vmmd daemon's database, API, and observability dependencies.
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/jailsetup"
)

func main() {
	if !jailsetup.Run(os.Args) {
		fmt.Fprintln(os.Stderr, "vmmd-jail-helper: expected a jail device setup command")
		os.Exit(2)
	}
}
