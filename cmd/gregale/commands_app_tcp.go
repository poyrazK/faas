package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const subTCPListeners = "tcp"

// cmdAppsTCP implements the app-owned raw TCP listener surface:
//
//	gregale apps tcp <slug> [list]
//	gregale apps tcp <slug> add --name NAME --guest-port PORT [--public-port PORT]
//	gregale apps tcp <slug> enable NAME
//	gregale apps tcp <slug> disable NAME
//	gregale apps tcp <slug> rm NAME
func cmdAppsTCP(slug string, args []string) int {
	if slug == "" {
		PrintUsage(os.Stderr, "usage: gregale apps tcp <slug> [list|add|enable|disable|rm]", "apps")
		return 1
	}
	verb := "list"
	if len(args) > 0 {
		verb = args[0]
		args = args[1:]
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	switch verb {
	case "list":
		if len(args) != 0 {
			PrintUsage(os.Stderr, "usage: gregale apps tcp <slug> list", "apps")
			return 1
		}
		listeners, err := client.ListAppTCPListeners(context.Background(), slug)
		if err != nil {
			return printErr("Request failed", err)
		}
		if jsonOutput {
			return jsonOut(writeNDJSON(listeners))
		}
		_, _ = fmt.Fprintf(osStdout, "%-24s %-10s %-10s %s\n", "NAME", "GUEST", "PUBLIC", "ENABLED")
		for _, listener := range listeners {
			_, _ = fmt.Fprintf(osStdout, "%-24s %-10d %-10d %t\n", listener.Name, listener.GuestPort, listener.PublicPort, listener.Enabled)
		}
		return 0

	case "add":
		fs := newFlagSet("apps tcp add", flag.ContinueOnError)
		name := fs.String("name", "", "listener name (required)")
		guestPort := fs.Int("guest-port", 0, "workload TCP port (required)")
		publicPort := fs.Int("public-port", 0, "stable public TCP port (40000-49999)")
		if err := fs.Parse(args); err != nil {
			return 1
		}
		if *name == "" || *guestPort == 0 || fs.NArg() != 0 {
			PrintUsage(os.Stderr, "usage: gregale apps tcp <slug> add --name NAME --guest-port PORT [--public-port PORT]", "apps")
			return 1
		}
		out, err := client.CreateAppTCPListener(context.Background(), slug, api.CreateTCPListenerRequest{
			Name: *name, GuestPort: *guestPort, PublicPort: *publicPort,
		})
		if err != nil {
			return printErr("Create failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		PrintOK(osStdout, "Created TCP listener %s on public port %d", out.Name, out.PublicPort)
		return 0

	case "enable", "disable":
		if len(args) != 1 {
			PrintUsage(os.Stderr, "usage: gregale apps tcp <slug> "+verb+" NAME", "apps")
			return 1
		}
		enabled := verb == "enable"
		out, err := client.UpdateAppTCPListener(context.Background(), slug, args[0], api.UpdateTCPListenerRequest{Enabled: &enabled})
		if err != nil {
			return printErr("Request failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		status := "Disabled"
		if enabled {
			status = "Enabled"
		}
		PrintOK(osStdout, "%s TCP listener %s", status, out.Name)
		return 0

	case "rm", "delete":
		if len(args) != 1 {
			PrintUsage(os.Stderr, "usage: gregale apps tcp <slug> rm NAME", "apps")
			return 1
		}
		if err := client.DeleteAppTCPListener(context.Background(), slug, args[0]); err != nil {
			return printErr("Delete failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"slug": slug, "name": args[0], "deleted": true}))
		}
		PrintOK(osStdout, "Deleted TCP listener %s", args[0])
		return 0
	default:
		PrintUsage(os.Stderr, "usage: gregale apps tcp <slug> [list|add|enable|disable|rm]", "apps")
		return 1
	}
}
