package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

func cmdPostgresCapabilities(args []string) int {
	fs := newFlagSet("postgres capabilities", flag.ContinueOnError)
	region := fs.String("region", "", "region (defaults to the configured region)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale postgres capabilities [--region REGION]", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetManagedPostgresCapabilities(context.Background(), *region)
	if err != nil {
		return printErr("Could not load managed PostgreSQL capabilities", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "managed postgres capabilities (%s)\n", result.Region)
	_, _ = fmt.Fprintf(osStdout, "  provisioning_enabled: %t\n", result.ProvisioningEnabled)
	_, _ = fmt.Fprintf(osStdout, "  database_limit:       %d\n", result.DatabaseLimit)
	_, _ = fmt.Fprintf(osStdout, "  postgres_majors:      %v\n", result.PostgresMajors)
	_, _ = fmt.Fprintf(osStdout, "  service_classes:      %s\n", strings.Join(result.ServiceClasses, ", "))
	_, _ = fmt.Fprintf(osStdout, "  availability:         %s\n", strings.Join(result.Availability, ", "))
	_, _ = fmt.Fprintf(osStdout, "  credential_access:    %s\n", strings.Join(result.CredentialAccess, ", "))
	_, _ = fmt.Fprintf(osStdout, "  scale_to_zero:        %t\n", result.ScaleToZero)
	_, _ = fmt.Fprintf(osStdout, "  always_on:            %t\n", result.AlwaysOn)
	_, _ = fmt.Fprintf(osStdout, "  pooled_connections:   %t\n", result.PooledConnections)
	_, _ = fmt.Fprintf(osStdout, "  point_in_time_restore: %t\n", result.PointInTimeRestore)
	_, _ = fmt.Fprintf(osStdout, "  storage_limit:        %s\n", formatPostgresStorage(result.StorageLimitBytes))
	_, _ = fmt.Fprintf(osStdout, "  restore_window:       %s\n", formatPostgresDuration(result.RestoreWindowSeconds))
	return 0
}
