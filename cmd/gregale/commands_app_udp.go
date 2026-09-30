package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const subUDPListeners = "udp"

// cmdAppsUDP implements the app-owned raw UDP listener surface:
//
//	gregale apps udp <slug> [list]
//	gregale apps udp <slug> add --name NAME --guest-port PORT [--public-port PORT]
//	gregale apps udp <slug> enable NAME
//	gregale apps udp <slug> disable NAME
//	gregale apps udp <slug> rm NAME
func cmdAppsUDP(slug string, args []string) int {
	if slug == "" {
		PrintUsage(os.Stderr, "usage: gregale apps udp <slug> [list|add|enable|disable|rm]", "apps")
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
			PrintUsage(os.Stderr, "usage: gregale apps udp <slug> list", "apps")
			return 1
		}
		listeners, err := client.ListAppUDPListeners(context.Background(), slug)
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
		fs := newFlagSet("apps udp add", flag.ContinueOnError)
		name := fs.String("name", "", "listener name (required)")
		guestPort := fs.Int("guest-port", 0, "workload UDP port (required)")
		publicPort := fs.Int("public-port", 0, fmt.Sprintf("stable public UDP port (%d-%d)", api.UDPListenerPublicPortMin, api.UDPListenerPublicPortMax))
		if err := fs.Parse(args); err != nil {
			return 1
		}
		if *name == "" || *guestPort == 0 || fs.NArg() != 0 {
			PrintUsage(os.Stderr, "usage: gregale apps udp <slug> add --name NAME --guest-port PORT [--public-port PORT]", "apps")
			return 1
		}
		out, err := client.CreateAppUDPListener(context.Background(), slug, api.CreateUDPListenerRequest{
			Name: *name, GuestPort: *guestPort, PublicPort: *publicPort,
		})
		if err != nil {
			return printErr("Create failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		PrintOK(osStdout, "Created disabled UDP listener %s on public port %d; enable it with gregale apps udp %s enable %s", out.Name, out.PublicPort, slug, out.Name)
		return 0

	case "enable", "disable":
		if len(args) != 1 {
			PrintUsage(os.Stderr, "usage: gregale apps udp <slug> "+verb+" NAME", "apps")
			return 1
		}
		enabled := verb == "enable"
		out, err := client.UpdateAppUDPListener(context.Background(), slug, args[0], api.UpdateUDPListenerRequest{Enabled: &enabled})
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
		PrintOK(osStdout, "%s UDP listener %s", status, out.Name)
		return 0

	case "rm", "delete":
		if len(args) != 1 {
			PrintUsage(os.Stderr, "usage: gregale apps udp <slug> rm NAME", "apps")
			return 1
		}
		if err := client.DeleteAppUDPListener(context.Background(), slug, args[0]); err != nil {
			return printErr("Delete failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"slug": slug, "name": args[0], "deleted": true}))
		}
		PrintOK(osStdout, "Deleted UDP listener %s", args[0])
		return 0
	default:
		PrintUsage(os.Stderr, "usage: gregale apps udp <slug> [list|add|enable|disable|rm]", "apps")
		return 1
	}
}
