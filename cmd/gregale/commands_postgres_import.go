package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresImport(args []string) int {
	fs := newFlagSet("postgres import", 0)
	check := fs.Bool("check", false, "check the source and target before copying")
	archive := fs.String("from-file", "", "restore a Gregale export and its companion manifest")
	sourceEnv := fs.String("source-env", "SOURCE_DATABASE_URL", "environment variable with the source PostgreSQL URL")
	requestID := fs.String("request-id", "", "stable operation UUID for retries")
	var skip stringListFlag
	fs.Var(&skip, "skip-extension", "omit this source extension from the copy (repeatable)")
	const usage = "usage: gregale postgres import DATABASE [--from-file ARCHIVE | --source-env ENV] [--check] [--request-id UUID] [--skip-extension NAME]"
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *sourceEnv == "" || *check && *requestID != "" || *requestID != "" && !validCustomerOperationUUID(*requestID) {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	var archiveFlag, sourceFlag bool
	fs.Visit(func(f *flag.Flag) {
		archiveFlag = archiveFlag || f.Name == "from-file"
		sourceFlag = sourceFlag || f.Name == "source-env"
	})
	if archiveFlag {
		if *archive == "" || sourceFlag || *check || len(skip) != 0 {
			return printErr("Invalid PostgreSQL archive import options", errors.New("use --from-file with --request-id or the default request ID"))
		}
		return importPostgresArchive(fs.Arg(0), *archive, *requestID)
	}
	source, err := parsePostgresImportURL(os.Getenv(*sourceEnv))
	if err != nil {
		return printErr("Invalid source database URL in "+*sourceEnv, err)
	}
	source.SkipExtensions = skip
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	org := client.ProjectOrganization()
	var result api.ManagedPostgresImportStatus
	if *check {
		if org == "" {
			result, err = client.CheckManagedPostgresImport(ctx, id, source)
		} else {
			result, err = client.CheckOrgManagedPostgresImport(ctx, org, id, source)
		}
		if err != nil {
			return printErr("Could not check PostgreSQL import", err)
		}
		return printPostgresImport(result)
	}
	operation := uuid.NewString()
	if *requestID != "" {
		operation = uuid.MustParse(*requestID).String()
	}
	request := api.ManagedPostgresImportRequest{OperationID: operation, Source: source}
	if org == "" {
		result, err = client.StartManagedPostgresImport(ctx, id, request)
	} else {
		result, err = client.StartOrgManagedPostgresImport(ctx, org, id, request)
	}
	if err != nil {
		return printErr("Could not start PostgreSQL import "+operation, err)
	}
	return printPostgresImport(result)
}

func cmdPostgresImportStatus(args []string) int {
	if len(args) != 2 || !validCustomerOperationUUID(args[1]) {
		PrintUsage(os.Stderr, "usage: gregale postgres import-status DATABASE OPERATION_ID", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, args[0])
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	operation := uuid.MustParse(args[1]).String()
	var result api.ManagedPostgresImportStatus
	if org := client.ProjectOrganization(); org != "" {
		result, err = client.GetOrgManagedPostgresImport(ctx, org, id, operation)
	} else {
		result, err = client.GetManagedPostgresImport(ctx, id, operation)
	}
	if err != nil {
		return printErr("Could not read PostgreSQL import status", err)
	}
	return printPostgresImport(result)
}

func parsePostgresImportURL(raw string) (api.ManagedPostgresImportSource, error) {
	source, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (source.Scheme != "postgres" && source.Scheme != "postgresql") ||
		source.Hostname() == "" || source.User == nil || source.User.Username() == "" || source.Fragment != "" {
		return api.ManagedPostgresImportSource{}, errors.New("expected a postgres:// or postgresql:// URL with a host, database, username and password")
	}
	database := strings.TrimPrefix(source.Path, "/")
	password, hasPassword := source.User.Password()
	if database == "" || !hasPassword || password == "" {
		return api.ManagedPostgresImportSource{}, errors.New("the database and password are required")
	}
	port := 5432
	if value := source.Port(); value != "" {
		port, err = strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return api.ManagedPostgresImportSource{}, errors.New("invalid port")
		}
	}
	options, err := url.ParseQuery(source.RawQuery)
	if err != nil {
		return api.ManagedPostgresImportSource{}, errors.New("invalid source URL options")
	}
	for key, values := range options {
		if len(values) == 1 {
			value := values[0]
			if key == "sslmode" && (value == "require" || value == "verify-ca" || value == "verify-full") {
				continue
			}
			if key == "channel_binding" && (value == "prefer" || value == "disable") {
				continue
			}
		}
		return api.ManagedPostgresImportSource{}, errors.New("unsupported source URL options; use a URL for the direct PostgreSQL endpoint with verified TLS")
	}
	return api.ManagedPostgresImportSource{Host: source.Hostname(), Port: port, Database: database, User: source.User.Username(), Password: password}, nil
}

func printPostgresImport(result api.ManagedPostgresImportStatus) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		renderPostgresImport(osStdout, result)
	}
	if result.State == "failed" {
		return 1
	}
	return 0
}

func renderPostgresImport(w io.Writer, result api.ManagedPostgresImportStatus) {
	_, _ = fmt.Fprintf(w, "  PostgreSQL import: %s\n", result.State)
	if result.OperationID != "" {
		_, _ = fmt.Fprintf(w, "  Operation ID: %s\n", result.OperationID)
	}
	if result.ErrorCode != "" {
		_, _ = fmt.Fprintf(w, "  Reason: %s\n", strings.ReplaceAll(result.ErrorCode, "_", " "))
	}
	if result.SourceMajor != 0 {
		_, _ = fmt.Fprintf(w, "  Source PostgreSQL major: %d\n", result.SourceMajor)
	}
	if len(result.IncompatibleExtensions) != 0 {
		_, _ = fmt.Fprintln(w, "  Unavailable extension versions:")
		for _, e := range result.IncompatibleExtensions {
			_, _ = fmt.Fprintf(w, "    %s %s\n", e.Name, e.Version)
		}
	} else if len(result.MissingExtensions) != 0 {
		_, _ = fmt.Fprintf(w, "  Unavailable extensions: %s\n", strings.Join(result.MissingExtensions, ", "))
	}
	if result.RequiredVolumeBytes != 0 {
		_, _ = fmt.Fprintf(w, "  Required storage: %s\n", formatPostgresStorage(result.RequiredVolumeBytes))
	}
}
