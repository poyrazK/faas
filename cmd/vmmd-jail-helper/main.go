// Command vmmd-jail-helper gates native launches and sets up private device
// mounts without initializing the vmmd daemon's database, API or observability.
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/jailsetup"
)

func main() {
	if !jailsetup.Run(os.Args) {
		fmt.Fprintln(os.Stderr, "vmmd-jail-helper: expected a launch or jail device setup command")
		os.Exit(2)
	}
}
