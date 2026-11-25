package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresBranchMasked(args []string) int {
	const usage = "usage: gregale postgres branch masked <database> <name> --policy FILE [--ttl DURATION]"
	fs := newFlagSet("postgres branch masked", flag.ContinueOnError)
	path := fs.String("policy", "", "JSON file selecting tables, columns and masking actions")
	ttl := fs.String("ttl", "", "expire the masked branch after this duration (defaults to 2h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 2 || *path == "" {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	seconds, err := postgresBranchTTL(*ttl)
	if err != nil {
		return printErr("Invalid branch TTL", err)
	}
	if seconds > int64(api.ManagedPostgresAllowanceBranchTTLMax/time.Second) {
		return printErr("Invalid branch TTL", errors.New("masked branches expire within 7 days"))
	}
	tables, err := readMaskedBranchPolicy(*path)
	if err != nil {
		return printErr("Could not read masking policy", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := api.CreateManagedPostgresMaskedBranchRequest{Name: fs.Arg(1), TTLSeconds: seconds, Tables: tables}
	var branch api.ManagedPostgresDatabase
	if projectOrganization == "" {
		id, err := postgresDatabaseID(ctx, client, fs.Arg(0))
		if err != nil {
			return printErr("Could not find managed PostgreSQL database", err)
		}
		branch, err = client.CreateManagedPostgresMaskedBranch(ctx, id, request)
		if err != nil {
			return printErr("Could not create masked branch", err)
		}
	} else {
		catalog, err := client.ListOrgManagedPostgresDatabases(ctx, projectOrganization)
		if err != nil {
			return printErr("Could not list organization databases", err)
		}
		source, err := resolveManagedPostgresDatabase(ctx, managedPostgresCatalog{items: catalog.Items}, fs.Arg(0))
		if err != nil {
			return printErr("Could not find managed PostgreSQL database", err)
		}
		branch, err = client.CreateOrgManagedPostgresMaskedBranch(ctx, projectOrganization, source.ID, request)
		if err != nil {
			return printErr("Could not create masked branch", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(branch))
	}
	renderPostgresDatabase(osStdout, branch)
	_, _ = fmt.Fprintf(osStdout, "Check validation with gregale postgres status %s\n", branch.ID)
	return 0
}

func readMaskedBranchPolicy(path string) ([]api.ManagedPostgresMaskedTable, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, api.ManagedPostgresMaskedPolicyMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > api.ManagedPostgresMaskedPolicyMaxBytes {
		return nil, errors.New("masking policy is larger than 32 KiB")
	}
	var policy struct {
		Tables []api.ManagedPostgresMaskedTable `json:"tables"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&policy) != nil || decoder.Decode(new(any)) != io.EOF || len(policy.Tables) == 0 {
		return nil, errors.New("expected a JSON object with a nonempty tables list")
	}
	return policy.Tables, nil
}
