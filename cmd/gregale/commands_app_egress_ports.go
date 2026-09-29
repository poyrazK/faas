package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

const subEgressPorts = "egress-ports"

const egressPortsUsage = "usage: gregale app <slug> egress-ports {show|add <port>|remove <port>|clear}"

// cmdAppEgressPorts shows or edits the app's extra TCP egress ports
// (ADR-361). Guests always reach 80 and 443; everything else they originate
// is dropped unless listed here (Pro and Scale).
func cmdAppEgressPorts(slug string, args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, egressPortsUsage, "apps")
		return 1
	}
	action := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app, err := client.GetApp(context.Background(), slug)
	if err != nil {
		return printErr("Could not load app", err)
	}
	current := append([]int(nil), app.EgressPorts...)
	switch action {
	case "show":
		if jsonOutput {
			return jsonOut(writeJSON(struct {
				Slug        string `json:"slug"`
				BasePorts   []int  `json:"base_ports"`
				EgressPorts []int  `json:"egress_ports"`
			}{Slug: slug, BasePorts: basePortsInt(), EgressPorts: nonNilInts(current)}))
		}
		_, _ = fmt.Fprintf(osStdout, "%s: guests may reach TCP %v", slug, basePortsInt())
		if len(current) > 0 {
			_, _ = fmt.Fprintf(osStdout, " plus %v", current)
		}
		_, _ = fmt.Fprintln(osStdout)
		return 0
	case "clear":
		current = []int{}
	case "add", "remove":
		if len(args) != 2 {
			PrintUsage(os.Stderr, "usage: gregale app <slug> egress-ports "+action+" <port>", "apps")
			return 1
		}
		port, convErr := strconv.Atoi(args[1])
		if convErr != nil || port < 1 || port > 65535 {
			return printErr("Invalid port", fmt.Errorf("%q is not a TCP port (1-65535)", args[1]))
		}
		if action == "add" {
			if slices.Contains(current, port) || slices.Contains(basePortsInt(), port) {
				return 0
			}
			current = append(current, port)
		} else {
			current = slices.DeleteFunc(current, func(p int) bool { return p == port })
		}
	default:
		PrintUsage(os.Stderr, egressPortsUsage, "apps")
		return 1
	}
	updated, err := client.UpdateApp(context.Background(), slug, api.UpdateAppRequest{EgressPorts: &current})
	if err != nil {
		return printErr("Egress ports update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	PrintOK(osStdout, "App %s egress ports: TCP %v plus %v.", slug, basePortsInt(), nonNilInts(updated.EgressPorts))
	return 0
}

func basePortsInt() []int {
	base := api.TenantEgressBasePorts()
	out := make([]int, len(base))
	for i, p := range base {
		out[i] = int(p)
	}
	return out
}

func nonNilInts(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}
