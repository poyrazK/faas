package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresUsageImport(args []string) int {
	fs := newFlagSet("postgres usage-import", flag.ContinueOnError)
	file := fs.String("file", "", "normalized retained evidence JSON file (required)")
	apply := fs.Bool("apply", false, "apply using the revision returned by preview")
	sessionFile := fs.String("session-file", "", "private file containing an operator session cookie (required for apply)")
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *file == "" {
		PrintUsage(osStderr, "usage: gregale postgres usage-import ACCOUNT_ID --file FILE [--apply]", "postgres")
		return 1
	}
	account, err := uuid.Parse(fs.Arg(0))
	if err != nil {
		return printErr("Invalid account ID", err)
	}
	request, err := readPostgresUsageImport(*file)
	if err != nil {
		return printErr("Invalid usage import file", err)
	}
	if *apply && request.ExpectedRevision == "" {
		return printErr("Preview revision is required for --apply", fmt.Errorf("set expected_revision to the revision returned by preview"))
	}
	if !*apply && request.ExpectedRevision != "" {
		return printErr("Preview file contains expected_revision", fmt.Errorf("remove expected_revision before previewing"))
	}
	client, err := postgresAccountingClient(*sessionFile, *apply)
	if err != nil {
		return printErr("Not logged in", err)
	}
	var result api.ManagedPostgresUsageImportResult
	if *apply {
		result, err = client.ApplyManagedPostgresUsageImport(context.Background(), account.String(), request)
	} else {
		result, err = client.PreviewManagedPostgresUsageImport(context.Background(), account.String(), request)
	}
	if err != nil {
		return printErr("Could not import managed PostgreSQL usage", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Usage import %s (applied=%t)\n  database: %s\n  windows: %d\n  previous cost: %d millicents\n  imported cost: %d millicents\n  cost delta: %d millicents\n  collected until: %s\n  observed at: %s\n  revision: %s\n",
		result.ImportID, result.Applied, result.DatabaseID, result.WindowCount, result.PreviousCostMillicents,
		result.ImportedCostMillicents, result.CostDeltaMillicents, result.CollectedUntil.Format("2006-01-02T15:04:05Z07:00"),
		result.ObservedAt.Format("2006-01-02T15:04:05Z07:00"), result.Revision)
	return 0
}

// The ordinary CLI login is an API key and cannot satisfy strict operator
// step-up. Load an explicit session from a private file; never put it in args,
// persist it in the normal key store, or combine it with bearer authorization.
func postgresAccountingClient(sessionPath string, apply bool) (*api.Client, error) {
	if sessionPath == "" {
		if apply {
			return nil, fmt.Errorf("--apply requires --session-file with a recently stepped-up operator session")
		}
		return authedClient()
	}
	file, err := openCustomerFile(sessionPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("operator session file must be a private regular file (mode 0600)")
	}
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(string(data))
	if value == "" || len(data) > 8192 {
		return nil, fmt.Errorf("invalid operator session file")
	}
	cookie := &http.Cookie{Name: "faas_sid", Value: value, Path: "/"}
	if err := cookie.Valid(); err != nil {
		return nil, fmt.Errorf("invalid operator session cookie")
	}
	endpoint, err := url.Parse(apiBase())
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	cookie.Secure = endpoint.Scheme == "https"
	jar.SetCookies(endpoint, []*http.Cookie{cookie})
	client := api.NewClient(endpoint.String(), "")
	client.HTTPClient().Jar = jar
	return client, nil
}

func readPostgresUsageImport(path string) (api.ManagedPostgresUsageImportRequest, error) {
	return readPostgresAccountingEvidence[api.ManagedPostgresUsageImportRequest](path, 1<<20)
}

func readPostgresAccountingEvidence[T any](path string, limit int64) (T, error) {
	var request T
	file, err := openCustomerFile(path)
	if err != nil {
		return request, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return request, err
	}
	if int64(len(data)) > limit {
		return request, fmt.Errorf("accounting evidence exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return request, fmt.Errorf("expected exactly one JSON object")
	}
	return request, nil
}
