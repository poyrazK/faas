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

Commands:
  login        Authenticate this machine (--token for CI)
  logout       Remove the stored token
  whoami       Show the authenticated account
  deploy       Deploy (--image REF | --tarball PATH [--runtime …] [--handler …] [--dockerfile])
  apps         List your apps
  apps -q      Delete an app
  app          Get/update one app
  rollback     Re-promote the previous deployment
  park         Park an app cold (kill all live instances)
  wake         Wake a parked app (pulls out of snapshot)
  domains      Manage custom domains
  crons        Manage scheduled requests
  keys         Manage API keys
  usage        Show this month's usage
  logs         Tail app or deployment logs (--follow)
  version      Print the CLI version
  connect      Connect a third-party service (github)
  deploy       Deploy (--image REF | --tarball PATH | --repo OWNER/NAME)
  open         Open the app's URL (or its dashboard page) in your browser

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
	case "login":
		return cmdLogin(args[1:])
	case "logout":
		return cmdLogout()
	case "whoami":
		return cmdWhoami()
	case "deploy":
		return cmdDeployTarball(args[1:])
	case "connect":
		return cmdConnect(args[1:])
	case "open":
		return cmdOpen(args[1:])
	case "apps":
		// `faas apps -q <slug>` is the delete path.
		if len(args) > 1 && (args[1] == "-q" || args[1] == "--quiet") {
			return cmdAppsRm(args[2:])
		}
		return cmdApps()
	case "app":
		return cmdApp(args[1:])
	case "rollback":
		return cmdRollback(args[1:])
	case "park":
		return cmdPark(args[1:])
	case "wake":
		return cmdWake(args[1:])
	case "domains":
		return cmdDomains(args[1:])
	case "crons":
		return cmdCrons(args[1:])
	case "keys":
		return cmdKeys(args[1:])
	case "usage":
		return cmdUsage(args[1:])
	case "logs":
		return cmdLogs(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "faas: unknown command %q\nRun 'faas help' for usage.\n", args[0])
		return 1
	}
}
