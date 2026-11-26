package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresRecoveryRegion(args []string) int {
	fs := newFlagSet("postgres recovery-region", flag.ContinueOnError)
	region := fs.String("region", "", "configured recovery region")
	off := fs.Bool("off", false, "disable regional recovery")
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || (*region == "") == !*off {
		PrintUsage(os.Stderr, "usage: gregale postgres recovery-region DATABASE (--region REGION | --off)", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	request := api.SetManagedPostgresRecoveryRegionRequest{Region: region}
	if org := client.ProjectOrganization(); org != "" {
		err = client.SetOrgManagedPostgresRecoveryRegion(ctx, org, id, request)
	} else {
		err = client.SetManagedPostgresRecoveryRegion(ctx, id, request)
	}
	if err != nil {
		return printErr("Could not update PostgreSQL recovery region", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(request))
	}
	if *off {
		_, _ = fmt.Fprintln(osStdout, "Regional recovery disabled")
	} else {
		_, _ = fmt.Fprintf(osStdout, "Recovery region set to %s\n", *region)
	}
	return 0
}
