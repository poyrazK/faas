package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

func cmdDataRepairDoctor(args []string) int {
	fs := newFlagSet("data-repair doctor", flag.ContinueOnError)
	profile := fs.String("profile", "executor", "executor API diagnostics, or local capture writer diagnostics")
	databaseEnv := fs.String("database-env", "", "capture profile: environment variable holding the application's writer connection")
	scopeFile := fs.String("scope-file", "", "capture profile: trusted application scope JSON")
	release := fs.String("release", "", "capture profile: configured application release")
	receipt := fs.String("receipt-protocol", "managed", "capture profile: managed or customer operation receipt")
	app := fs.String("app", "", "hosted application slug; uses the normal login")
	base := fs.String("url", "http://127.0.0.1:8796", "executor URL")
	tokenEnv := fs.String("token-env", "GREGALE_REPAIR_TOKEN", "executor token environment variable")
	table := fs.String("table", "", "exact qualified enrolled table name")
	fs.String("environment", "", "configured hosted environment")
	fs.String("customer", "", "configured hosted customer UUID; empty selects account scope")
	flags, pos := splitArgsForFlags(args)
	if fs.Parse(flags) != nil || len(pos) != 0 || fs.NArg() != 0 || !datarepair.DoctorTable(*table) {
		return printErr("Specify one exact qualified table with --table", datarepair.ErrInvalid)
	}
	if *profile == "capture" {
		return dataRepairWriterDoctor(fs, *databaseEnv, *scopeFile, *table, *release, *receipt)
	}
	if *profile != "executor" {
		return printErr("Choose executor or capture readiness", datarepair.ErrInvalid)
	}
	values := url.Values{"table": {*table}}
	invalid := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "database-env" || f.Name == "scope-file" || f.Name == "release" || f.Name == "receipt-protocol" {
			invalid = true
		}
		if *app == "" && (f.Name == "environment" || f.Name == "customer") || *app != "" && (f.Name == "url" || f.Name == "token-env") {
			invalid = true
		}
		if f.Name == "environment" || f.Name == "customer" {
			values.Set(f.Name, f.Value.String())
		}
	})
	if invalid {
		return printErr("Use hosted scope flags with --app, or executor --url/--token-env", datarepair.ErrInvalid)
	}
	endpoint, token, path := *base, os.Getenv(*tokenEnv), "/v1/data-repair/doctor"
	if *app != "" {
		endpoint, token, path = apiBase(), loadToken(), "/v1/apps/"+url.PathEscape(*app)+"/data-repair/doctor"
	}
	u, err := repairEndpoint(endpoint)
	if err != nil || token == "" {
		return printErr("Use an authorized HTTPS API or loopback executor", datarepair.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.DataRepairDoctorTimeoutSeconds)*time.Second)
	defer cancel()
	raw, err := managedRepairRequestLimit(ctx, strings.TrimRight(u.String(), "/")+path+"?"+values.Encode(), token, nil, "", api.DataRepairDoctorMaxResponseBytes)
	if err != nil {
		return printErr("Cannot inspect repair readiness", err)
	}
	return printDataRepairDoctor(raw, values, *app != "")
}

func dataRepairWriterDoctor(fs *flag.FlagSet, databaseEnv, scopeFile, table, release, receipt string) int {
	invalid := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "profile", "database-env", "scope-file", "table", "release", "receipt-protocol":
		default:
			invalid = true
		}
	})
	if invalid || databaseEnv == "" || os.Getenv(databaseEnv) == "" {
		return printErr("Capture readiness requires explicit local --database-env and --scope-file; API and hosted selectors are not accepted", datarepair.ErrInvalid)
	}
	scope, err := dataRepairRetentionScope(scopeFile)
	if err != nil {
		return printErr("Use a valid trusted application --scope-file", datarepair.ErrInvalid)
	}
	options, err := (datarepair.CaptureReadinessOptions{Scope: scope, Table: table, Release: release, ReceiptProtocol: receipt}).Normalize()
	if err != nil {
		return printErr("Use a qualified table, configured release and managed/customer receipt protocol", datarepair.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.DataRepairDoctorTimeoutSeconds)*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv(databaseEnv))
	if err != nil {
		return printErr("Cannot configure application writer diagnostics", datarepair.ErrUnavailable)
	}
	defer pool.Close()
	report, err := (&datarepair.Engine{Pool: pool}).CheckCapture(ctx, options)
	if err != nil {
		return printErr("Cannot inspect capture readiness", err)
	}
	raw, _ := json.MarshalIndent(report, "", "  ")
	if _, err := fmt.Fprintln(osStdout, string(raw)); err != nil {
		return printErr("Cannot write capture readiness", err)
	}
	if report.Status != "ready" {
		return 1
	}
	return 0
}

func printDataRepairDoctor(raw []byte, values url.Values, hosted bool) int {
	var report datarepair.DoctorReport
	if len(raw) > api.DataRepairDoctorMaxResponseBytes || json.Unmarshal(raw, &report) != nil || !report.Valid() || report.Table != values.Get("table") ||
		len(report.Findings) != 9+boolInt(hosted) || values.Has("environment") && report.Scope.Environment != values.Get("environment") ||
		values.Has("customer") && report.Scope.PlatformTenantID != values.Get("customer") {
		return printErr("Invalid repair readiness response", datarepair.ErrUnavailable)
	}
	pretty, _ := json.MarshalIndent(report, "", "  ")
	if _, err := fmt.Fprintln(osStdout, string(pretty)); err != nil {
		return printErr("Cannot write repair readiness", err)
	}
	if report.Status != "ready" {
		return 1
	}
	return 0
}
