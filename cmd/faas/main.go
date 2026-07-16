// Command faas is the customer-facing CLI and the primary interface to the
// platform (UX spec §3). Everything the platform does is possible from here.
//
// M0 ships the dispatcher, version, and help skeleton; individual commands
// (login, deploy, logs, usage, …) land from M5 onward. Exit codes follow UX spec
// §3.2: 0 ok, 1 user error, 2 auth, 3 platform/infra.
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/wire"
)

const usage = `faas — deploy apps and functions that scale to zero.

Usage:
  faas <command> [flags]

Commands (M5+):
  login        Authenticate this machine
  deploy       Build and deploy the current directory
  apps         List your apps
  logs         Tail an app's logs
  usage        Show usage vs. quota (mirrors your invoice)
  version      Print the CLI version

Run 'faas <command> --help' for command details.
Docs: https://docs.DOMAIN
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Print(usage)
		return 0
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Printf("faas %s\n", wire.Version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "faas: unknown command %q\nRun 'faas help' for usage.\n", args[0])
		return 1
	}
}
