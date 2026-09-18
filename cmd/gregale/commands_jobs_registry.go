package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsRegistry(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs registry <list|set|rm> <job> [flags]", "jobs")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdJobsRegistryList(args[1:])
	case "set", subAdd:
		return cmdJobsRegistrySet(args[1:])
	case subRm:
		return cmdJobsRegistryRm(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown jobs registry subcommand %q\n", args[0])
		return 1
	}
}

func cmdJobsRegistryList(args []string) int {
	if len(args) != 1 || !jobSlugPattern.MatchString(args[0]) {
		PrintUsage(os.Stderr, "usage: gregale jobs registry list <job>", "jobs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListJobRegistryCredentials(context.Background(), args[0])
	if err != nil {
		return printErr("List failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(osStdout, "Quota: %d/%d\n", resp.Count, resp.QuotaMax)
	if len(resp.Credentials) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no credentials)")
		return 0
	}
	for _, c := range resp.Credentials {
		last := c.LastUsedAt
		if last == "" {
			last = "-"
		}
		_, _ = fmt.Fprintf(osStdout, "%-40s  %-32s  %s  last_used=%s\n", c.Registry, c.Username, c.CreatedAt, last)
	}
	return 0
}

func cmdJobsRegistrySet(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale jobs registry set <job> --registry <h> --user <u> (--password-stdin|--password <p>)", "jobs")
		return 1
	}
	job := args[0]
	if !jobSlugPattern.MatchString(job) {
		PrintUsage(os.Stderr, "usage: gregale jobs registry set <job> (job must be 3..40 lowercase / digits / hyphens)", "jobs")
		return 1
	}
	fs := newFlagSet("jobs-registry-set", flag.ContinueOnError)
	registry := fs.String("registry", "", "registry host[:port] (required, lowercase DNS[:port])")
	username := fs.String("user", "", "username (required)")
	password := fs.String("password", "", "password (compatibility; visible in shell history; prefer --password-stdin)")
	passwordStdin := fs.Bool("password-stdin", false, "read password/token from stdin")
	if err := fs.Parse(args[1:]); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	passwordFromArg := *password != ""
	if *passwordStdin && passwordFromArg {
		return printErr("Invalid flags", fmt.Errorf("--password and --password-stdin are mutually exclusive"))
	}
	if *passwordStdin {
		body, err := io.ReadAll(io.LimitReader(osStdin, int64(api.MaxRegistryPasswordBytes)+2))
		if err != nil {
			return printErr("Could not read registry password", err)
		}
		*password = strings.TrimSuffix(strings.TrimSuffix(string(body), "\n"), "\r")
	} else if *password == "" && stdinIsTTY() {
		value, err := readInteractivePassword(bufio.NewReader(osStdin), "Registry password/token: ")
		if err != nil {
			return printErr("Could not read registry password", err)
		}
		*password = value
	}
	if *registry == "" || *username == "" || *password == "" {
		PrintUsage(os.Stderr, "usage: gregale jobs registry set <job> --registry <h> --user <u> (--password-stdin|--password <p>)", "jobs")
		return 1
	}
	registryAPI, err := registryInputForAPI(*registry)
	if err != nil {
		return printErr("Invalid --registry", err)
	}
	if len(*username) > api.MaxRegistryUsernameLen || len(*password) > api.MaxRegistryPasswordBytes {
		return printErr("Invalid credential", fmt.Errorf("username/password exceeds the supported limit"))
	}
	if passwordFromArg {
		PrintWarn(osStderr, "--password is visible to shell history and process inspection; prefer --password-stdin")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.SetJobRegistryCredential(context.Background(), job, registryAPI, *username, *password)
	if err != nil {
		return printErr("Set failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Registry credential for %s set.", resp.Registry)
	_, _ = fmt.Fprintf(osStdout, "  username:    %s\n", resp.Username)
	_, _ = fmt.Fprintf(osStdout, "  created_at:  %s\n", resp.CreatedAt)
	_, _ = fmt.Fprintf(osStdout, "  updated_at:  %s\n", resp.UpdatedAt)
	return 0
}

func cmdJobsRegistryRm(args []string) int {
	if len(args) != 3 || args[1] != "--registry" || !jobSlugPattern.MatchString(args[0]) || args[2] == "" {
		PrintUsage(os.Stderr, "usage: gregale jobs registry rm <job> --registry <h>", "jobs")
		return 1
	}
	registryAPI, err := registryInputForAPI(args[2])
	if err != nil {
		return printErr("Invalid --registry", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteJobRegistryCredential(context.Background(), args[0], registryAPI); err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"registry": args[2], "deleted": true}))
	}
	PrintOK(osStdout, "Registry credential for %s removed.", args[2])
	return 0
}
