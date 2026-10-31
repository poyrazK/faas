// cmd/gregale/cmd_domains_hosts.go — `gregale domain host set|get|clear`
// (ADR-520 amendment 2): a wildcard domain's optional host allowlist. With
// one, only the listed names below the wildcard get certificates, so random
// server names cannot spend the wildcard's new-host budget.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

const domainHostUsage = "usage: gregale domain host <set|get|clear> <*.domain> [names...]\n"

func cmdDomainsHost(args []string) int {
	if len(args) == 0 {
		printCommandValidation(os.Stderr, domainHostUsage)
		return 1
	}
	switch args[0] {
	case "set":
		return cmdDomainsHostSet(args[1:])
	case subGet, subDomainsShow:
		return cmdDomainsHostGet(args[1:])
	case "clear":
		return cmdDomainsHostClear(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown domain host subcommand %q\n"+domainHostUsage, args[0])
	return 1
}

// cmdDomainsHostSet replaces the allowlist: `gregale domain host set
// '*.example.com' app api eu.shop`.
func cmdDomainsHostSet(args []string) int {
	if len(args) < 2 {
		printCommandValidation(os.Stderr, "usage: gregale domain host set <*.domain> <name>...\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := client.SetDomainHosts(ctx, args[0], args[1:])
	if err != nil {
		return printErr("Could not set the host allowlist", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Only these names below %s get certificates: %s", out.Domain, strings.Join(out.Names, ", "))
	return 0
}

func cmdDomainsHostGet(args []string) int {
	if len(args) != 1 {
		printCommandValidation(os.Stderr, "usage: gregale domain host get <*.domain>\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := client.GetDomainHosts(ctx, args[0])
	if err != nil {
		return printErr("Could not read the host allowlist", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if !out.Restricted {
		_, _ = fmt.Fprintf(osStdout, "No allowlist: every name below %s may get a certificate.\n", out.Domain)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Names below %s that may get a certificate:\n", out.Domain)
	for _, name := range out.Names {
		_, _ = fmt.Fprintf(osStdout, "  %s\n", name)
	}
	return 0
}

func cmdDomainsHostClear(args []string) int {
	if len(args) != 1 {
		printCommandValidation(os.Stderr, "usage: gregale domain host clear <*.domain>\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.ClearDomainHosts(ctx, args[0]); err != nil {
		return printErr("Could not clear the host allowlist", err)
	}
	PrintOK(osStdout, "Every name below %s may get a certificate again", args[0])
	return 0
}
