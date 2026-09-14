// commands_tenant_surfaces.go — gregale CLI for the tenant
// surfaces customer-facing surface (issue #879 / ADR-100 PR-C).
//
// Subcommand family mirrors the closest precedent (custom_domains:
// commands2.go:1366 cmdDomains). Three top-level verbs +
// two hostname-scoped sub-verbs:
//
//	gregale tenant-surfaces list --app <slug>
//	gregale tenant-surfaces add --app <slug> --name <n> [--hostname <h>...]
//	gregale tenant-surfaces rm --app <slug> <surface-id>
//	gregale tenant-surfaces hostname add --app <slug> --surface <id> --hostname <h>
//	gregale tenant-surfaces hostname rm  --app <slug> --surface <id> <hostname>
//
// `add` prints the TXT records the customer must publish for
// DNS-01 verification (mirrors cmdDomains's print). The
// underlying SDK methods live in pkg/api/client.go and are
// how this file avoids direct HTTP calls (per Tier A8 / CLI is
// HTTP-only, never direct DB access).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdTenantSurfaces dispatches list/add/rm + the hostname
// subcommand. Returns 1 on bad usage / error, 0 on success.
func cmdTenantSurfaces(args []string) int {
	parent, _ := lookupCliCommand("tenant-surfaces")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale tenant-surfaces <list|add|rm|hostname> [args]", "tenant-surfaces")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdTenantSurfacesList(args[1:])
	case subAdd:
		return cmdTenantSurfacesAdd(args[1:])
	case subRm:
		return cmdTenantSurfacesRm(args[1:])
	case "hostname":
		return cmdTenantSurfacesHostname(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown tenant-surfaces subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	if sug != "" {
		fmt.Fprintf(os.Stderr, "did you mean: %s\n", sug)
	}
	return 1
}

// cmdTenantSurfacesList — `tenant-surfaces list --app <slug>`.
func cmdTenantSurfacesList(args []string) int {
	fs := newFlagSet("tenant-surfaces-list", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale tenant-surfaces list --app <slug>", "tenant-surfaces")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	surfaces, err := client.ListTenantSurfaces(context.Background(), *slug)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(surfaces))
	}
	if len(surfaces) == 0 {
		fmt.Println("No tenant surfaces.")
		return 0
	}
	fmt.Printf("%-32s %-15s %-12s %s\n", "NAME", "CERT_KIND", "CERT_STATE", "HOSTNAMES")
	for _, s := range surfaces {
		hns := ""
		for i, h := range s.Hostnames {
			if i > 0 {
				hns += ","
			}
			hns += h.Hostname
		}
		fmt.Printf("%-32s %-15s %-12s %s\n", s.Name, s.CertKind, s.CertState, hns)
	}
	return 0
}

// cmdTenantSurfacesAdd — `tenant-surfaces add --app <slug>
// --name <n> [--hostname <h>...]`. Idempotent on a fresh
// surface; the first call creates the surface and seeds the
// hostnames. The TXT records are printed so the customer can
// publish them.
func cmdTenantSurfacesAdd(args []string) int {
	fs := newFlagSet("tenant-surfaces-add", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	name := fs.String("name", "", "surface name (required)")
	var hostnames stringListFlag
	fs.Var(&hostnames, "hostname", "hostname to attach (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *name == "" {
		PrintUsage(os.Stderr, "usage: gregale tenant-surfaces add --app <slug> --name <name> [--hostname <h>...]", "tenant-surfaces")
		return 1
	}
	// Mirrors the cron precedent: the slug IS the AppID the
	// server resolves. The server validates
	// req.AppID == app.ID in handlers_tenant_surfaces.go:64.
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	surf, err := client.CreateTenantSurface(context.Background(), *slug, api.CreateTenantSurfaceRequest{
		AppID:     *slug,
		Name:      *name,
		CertKind:  "per_host_san",
		Hostnames: hostnames,
	})
	if err != nil {
		return printErr("Could not add tenant surface", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON([]api.TenantSurfaceResponse{surf}))
	}
	fmt.Printf("Surface %s created.\n", surf.ID)
	if len(surf.Hostnames) > 0 {
		fmt.Println("\nAdd these TXT records to your DNS:")
		for _, h := range surf.Hostnames {
			fmt.Printf("  %s  TXT  %s\n", h.TXTRecord, h.ChallengeToken)
		}
		fmt.Println("\nRun 'gregale tenant-surfaces list --app " + *slug + "' to see when verification completes.")
	}
	return 0
}

// cmdTenantSurfacesRm — `tenant-surfaces rm --app <slug> <surface-id>`.
func cmdTenantSurfacesRm(args []string) int {
	fs := newFlagSet("tenant-surfaces-rm", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale tenant-surfaces rm --app <slug> <surface-id>", "tenant-surfaces")
		return 1
	}
	surfaceID := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteTenantSurface(context.Background(), *slug, surfaceID); err != nil {
		return printErr("Delete failed", err)
	}
	PrintOK(osStdout, "Removed")
	return 0
}

// cmdTenantSurfacesHostname — hostname subcommand dispatcher.
func cmdTenantSurfacesHostname(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale tenant-surfaces hostname <add|rm> [args]", "tenant-surfaces")
		return 1
	}
	switch args[0] {
	case subAdd:
		return cmdTenantSurfacesHostnameAdd(args[1:])
	case subRm:
		return cmdTenantSurfacesHostnameRm(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown tenant-surfaces hostname subcommand %q\n", args[0])
	return 1
}

// cmdTenantSurfacesHostnameAdd — `tenant-surfaces hostname add
// --app <slug> --surface <id> --hostname <h>`. Prints the TXT
// record so the customer can publish it.
func cmdTenantSurfacesHostnameAdd(args []string) int {
	fs := newFlagSet("tenant-surfaces-hostname-add", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	surfaceID := fs.String("surface", "", "surface id (required)")
	hostname := fs.String("hostname", "", "hostname to attach (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *surfaceID == "" || *hostname == "" {
		PrintUsage(os.Stderr, "usage: gregale tenant-surfaces hostname add --app <slug> --surface <id> --hostname <h>", "tenant-surfaces")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	h, err := client.AddTenantHostname(context.Background(), *slug, *surfaceID, api.AddTenantHostnameRequest{
		Hostname: *hostname,
	})
	if err != nil {
		return printErr("Could not add hostname", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON([]api.TenantHostnameResponse{h}))
	}
	fmt.Printf("Hostname %s added to surface %s.\n", h.Hostname, *surfaceID)
	fmt.Printf("\nAdd this TXT record to your DNS:\n\n  %s  TXT  %s\n\n", h.TXTRecord, h.ChallengeToken)
	return 0
}

// cmdTenantSurfacesHostnameRm — `tenant-surfaces hostname rm
// --app <slug> --surface <id> <hostname>`.
func cmdTenantSurfacesHostnameRm(args []string) int {
	fs := newFlagSet("tenant-surfaces-hostname-rm", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	surfaceID := fs.String("surface", "", "surface id (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *surfaceID == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale tenant-surfaces hostname rm --app <slug> --surface <id> <hostname>", "tenant-surfaces")
		return 1
	}
	hostname := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.RemoveTenantHostname(context.Background(), *slug, *surfaceID, hostname); err != nil {
		return printErr("Remove failed", err)
	}
	PrintOK(osStdout, "Removed")
	return 0
}

// stringListFlag is a flag.Value that collects repeated
// --hostname flags into a slice. Used by the add subcommand so
// the customer can pass --hostname h1 --hostname h2 in one call.
type stringListFlag []string

func (s *stringListFlag) String() string     { return fmt.Sprintf("%v", *s) }
func (s *stringListFlag) Set(v string) error { *s = append(*s, v); return nil }
