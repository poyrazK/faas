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
  deploy       Deploy (--image REF | --tarball PATH | --repo OWNER/NAME | --template NAME)
  apps         List your apps
  apps ls      Alias for 'faas apps'
  apps -q      Delete an app
  app          Get/update one app (faas app <slug> [scale|rename <new>|--ram N|…])
  ps           Show live instances + state for an app
  status       Personal SLO numbers (availability, wake p95, build success)
  env          Pull/push .env <-> sealed secrets (--app <slug>)
  plan         Change plan (free|hobby|pro|scale)
  dashboard    Open the account dashboard in your browser
  rollback     Re-promote the previous deployment
  park         Park an app cold (kill all live instances)
  wake         Wake a parked app (pulls out of snapshot)
  domains      Manage custom domains
  crons        Manage scheduled requests
  keys         Manage API keys
  secrets      Manage env secrets on an app (--app <slug>)
  account      Self-service: export your data, delete account, restore
  usage        Show this month's usage
  logs         Tail app or deployment logs (--follow)
  version      Print the CLI version
  connect      Connect a third-party service (github)
  open         Open the app's URL (or its dashboard page) in your browser

Run 'faas <command> --help' for command details.
Docs: https://docs.DOMAIN
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	// Issue #64 D1: every command accepts --json (top-level). Strip
	// it before dispatch and set jsonOutput so per-command printers
	// switch to NDJSON/indented JSON. FAAS_JSON=1 env also works.
	args = applyJSONFlag(args)
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
		// `faas apps ls` is an alias for the default list action.
		if len(args) > 1 && args[1] == "ls" {
			return cmdApps()
		}
		// `faas apps -q <slug>` is the delete path.
		if len(args) > 1 && (args[1] == "-q" || args[1] == "--quiet") {
			return cmdAppsRm(args[2:])
		}
		return cmdApps()
	case appSlugFallback:
		// Routes to cmdAppDispatch which knows the new scale/rename
		// subcommand form and falls back to the legacy flag-form
		// (commands2.go::cmdApp) for backwards compat.
		return cmdAppDispatch(args[1:])
	case "ps":
		return cmdPS(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "env":
		return cmdEnv(args[1:])
	case "plan":
		return cmdPlan(args[1:])
	case "dashboard":
		return cmdDashboard(args[1:])
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
	case "secrets":
		return cmdSecrets(args[1:])
	case "account":
		return cmdAccount(args[1:])
	case "usage":
		return cmdUsage(args[1:])
	case "logs":
		return cmdLogs(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "faas: unknown command %q\nRun 'faas help' for usage.\n", args[0])
		return 1
	}
}
