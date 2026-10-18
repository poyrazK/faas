package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
	"github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/wire"
)

func cmdDataRepair(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale data-repair <schema|enroll|serve|doctor|sources|history|plan|get|validate|verify|replace|resolve|rehearse|approve|apply|retention>", "data-repair")
		return 1
	}
	verb := args[0]
	if verb == "doctor" {
		return cmdDataRepairDoctor(args[1:])
	}
	if verb == "sources" {
		return cmdDataRepairSources(args[1:])
	}
	if verb == "history" {
		return cmdDataRepairHistory(args[1:])
	}
	if verb == "retention" {
		return cmdDataRepairRetention(args[1:])
	}
	if verb == "schema" {
		if len(args) != 1 && (len(args) != 2 || args[1] != "--upgrade") {
			return 1
		}
		sql := datarepair.Schema()
		if len(args) == 2 {
			sql = datarepair.UpgradeSchema()
		}
		if _, err := fmt.Fprint(osStdout, sql); err != nil {
			return printErr("Cannot write repair schema", err)
		}
		return 0
	}
	if verb == "serve" || verb == "enroll" {
		return cmdDataRepairLocal(verb, args[1:])
	}
	return cmdDataRepairRemote(verb, args[1:])
}

func cmdDataRepairLocal(verb string, args []string) int {
	fs := newFlagSet("data-repair "+verb, flag.ContinueOnError)
	databaseEnv := fs.String("database-env", "GREGALE_REPAIR_DATABASE_URL", "environment variable holding the application database connection")
	table := fs.String("table", "", "qualified table to enroll")
	fields := fs.String("fields", "", "comma-separated repairable columns")
	listen := fs.String("listen", "127.0.0.1:8796", "executor HTTP listen address")
	scopeFile := fs.String("scope-file", "", "authenticated executor scope JSON")
	tokenEnv := fs.String("token-env", "GREGALE_REPAIR_TOKEN", "environment variable holding the executor token")
	actor := fs.String("actor", "", "recorded operator identity")
	dumpBinary := fs.String("pg-dump", "pg_dump", "operator-installed pg_dump executable")
	image := fs.String("sandbox-image", datarepair.DefaultSandboxImage, "pre-pulled digest-pinned PostgreSQL image")
	coordinator := fs.Bool("managed-coordinator", false, "trust the managed API to forward its recorded approval actor")
	checksFile := fs.String("checks-file", "", "reviewed SQL SELECT returning one boolean after correction")
	policyFile := fs.String("checks-policy-file", "", "operator-owned version 1 JSON policy containing named SQL checks")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return 1
	}
	if *checksFile != "" && *policyFile != "" {
		return printErr("Choose --checks-file or --checks-policy-file", datarepair.ErrInvalid)
	}
	if verb == "serve" && os.Getenv("GREGALE_DATA_REPAIR_ENABLED") != "true" {
		return printErr("Internal selective repair requires GREGALE_DATA_REPAIR_ENABLED=true", datarepair.ErrUnavailable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	dsn := os.Getenv(*databaseEnv)
	if dsn == "" {
		return printErr("Repair database environment variable is empty", datarepair.ErrInvalid)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return printErr("Cannot configure repair database", datarepair.ErrUnavailable)
	}
	defer pool.Close()
	if verb == "enroll" {
		if *table == "" || *fields == "" {
			return printErr("Use --table and --fields", datarepair.ErrInvalid)
		}
		var id string
		if pool.QueryRow(ctx, `SELECT gregale_repair.enroll($1::regclass,$2::text[])`, *table, strings.Split(*fields, ",")).Scan(&id) != nil {
			return printErr("Table enrollment failed; inspect the owner-installed schema and supported table contract", datarepair.ErrInvalid)
		}
		if _, err := fmt.Fprintln(osStdout, id); err != nil {
			return printErr("Cannot write enrollment result", err)
		}
		return 0
	}
	var scope datarepair.Scope
	file, err := openCustomerFile(*scopeFile)
	if err != nil {
		return printErr("Cannot read executor scope", datarepair.ErrInvalid)
	}
	d := json.NewDecoder(io.LimitReader(file, int64(api.DataRepairMaxRequestBytes)))
	d.DisallowUnknownFields()
	err = d.Decode(&scope)
	_ = file.Close()
	if err != nil || scope.Validate() != nil || *actor == "" || len(os.Getenv(*tokenEnv)) < 32 {
		return printErr("Use a valid --scope-file, --actor, and executor token of at least 32 bytes", datarepair.ErrInvalid)
	}
	var checks string
	var named []datarepair.Check
	if *checksFile != "" {
		f, readErr := openCustomerFile(*checksFile)
		if readErr != nil {
			return printErr("Cannot read repair checks", datarepair.ErrInvalid)
		}
		b, readErr := io.ReadAll(io.LimitReader(f, int64(api.DataRepairMaxRequestBytes+1)))
		_ = f.Close()
		if readErr != nil || len(b) > api.DataRepairMaxRequestBytes {
			return printErr("Invalid or oversized repair checks", datarepair.ErrInvalid)
		}
		checks = string(b)
	}
	if *policyFile != "" {
		policy, err := dataRepairCheckPolicyFile(*policyFile)
		if err != nil {
			return printErr("Invalid named repair check policy", err)
		}
		named = policy.Checks
	}
	engine := &datarepair.Engine{Pool: pool, CheckSQL: checks, Checks: named, Rehearser: datarepair.DockerRehearser{SourceURL: dsn, DumpBinary: *dumpBinary, Image: *image, CheckSQL: checks, Checks: named}}
	handler := &repairapi.Handler{Engine: engine, Coordinator: *coordinator, Release: wire.Version, Authenticate: repairapi.Bearer(os.Getenv(*tokenEnv), repairapi.Principal{Scope: scope, Actor: *actor, CanApprove: true, CanApply: true})}
	if *coordinator {
		handler.Exporter = &datarepair.SnapshotExporter{SourceURL: dsn, DumpBinary: *dumpBinary}
	}
	return serveDataRepair(*listen, handler.Routes())
}

func dataRepairCheckPolicyFile(path string) (datarepair.CheckPolicy, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return datarepair.CheckPolicy{}, datarepair.ErrInvalid
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, int64(api.DataRepairMaxRequestBytes+1)))
	if err != nil {
		return datarepair.CheckPolicy{}, datarepair.ErrInvalid
	}
	return datarepair.ParseCheckPolicy(b)
}

func serveDataRepair(listen string, handler http.Handler) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: time.Duration(api.DataRepairTimeoutSeconds+10) * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	fmt.Fprintln(os.Stderr, "Selective repair executor listening on", listen)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return printErr("Repair executor stopped", datarepair.ErrUnavailable)
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
	}
	return 0
}

func repairEndpoint(base string) (*url.URL, error) {
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return nil, datarepair.ErrInvalid
	}
	if u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1")) {
		return nil, datarepair.ErrInvalid
	}
	return u, nil
}

func cmdDataRepairRemote(verb string, args []string) int {
	for _, arg := range args {
		if arg == "--app" || strings.HasPrefix(arg, "--app=") {
			return cmdDataRepairManaged(verb, args)
		}
	}
	fs := newFlagSet("data-repair "+verb, flag.ContinueOnError)
	base := fs.String("url", "http://127.0.0.1:8796", "executor URL; remote executors require HTTPS")
	tokenEnv := fs.String("token-env", "GREGALE_REPAIR_TOKEN", "environment variable holding executor token")
	digest := fs.String("digest", "", "exact reviewed plan digest")
	repairID := fs.String("repair-operation", "", "stable UUID saved before applying; reuse to reconcile unknown outcomes")
	yes := fs.Bool("yes", false, "approve this exact plan or submit its approved correction")
	flags, pos := splitArgsForFlags(args, "yes")
	if fs.Parse(flags) != nil || len(pos) != 1 || fs.NArg() != 0 {
		return 1
	}
	u, err := repairEndpoint(*base)
	if err != nil {
		return printErr("Use an HTTPS executor URL or local loopback address", err)
	}
	method, path := http.MethodPost, "/v1/data-repair/plans"
	body := map[string]string{}
	switch verb {
	case "plan":
		body["operation_id"] = pos[0]
	case "get":
		method = http.MethodGet
		path += "/" + url.PathEscape(pos[0])
	case "replace":
		if !*yes || *digest == "" || !dataRepairUUID(pos[0]) || !dataRepairUUID(*repairID) || *repairID == pos[0] {
			return printErr("Use --digest, --yes and a saved new --repair-operation UUID for the replacement", datarepair.ErrInvalid)
		}
		path += "/" + url.PathEscape(pos[0]) + "/replace"
		body["digest"], body["plan_id"] = *digest, *repairID
	case "resolve":
		if !*yes || *digest == "" || !dataRepairUUID(pos[0]) || !dataRepairUUID(*repairID) {
			return printErr("Use --digest, --yes and the original --repair-operation UUID to resolve apply authority", datarepair.ErrInvalid)
		}
		path += "/" + url.PathEscape(pos[0]) + "/resolve"
		body["digest"], body["repair_operation_id"] = *digest, *repairID
	case "validate", "verify", "rehearse", "approve", "apply":
		if *digest == "" || ((verb == "approve" || verb == "apply") && !*yes) || verb == "apply" && *repairID == "" {
			return printErr("Provide --digest; approval/apply require --yes and apply requires --repair-operation", datarepair.ErrInvalid)
		}
		path += "/" + url.PathEscape(pos[0]) + "/" + verb
		body["digest"] = *digest
		if verb == "apply" {
			body["repair_operation_id"] = *repairID
		}
	default:
		return printErr("Unknown data-repair command", datarepair.ErrInvalid)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	raw, _ := json.Marshal(body)
	if method == http.MethodGet {
		raw = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.DataRepairTimeoutSeconds+10)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(raw))
	if err != nil {
		return 1
	}
	token := os.Getenv(*tokenEnv)
	if token == "" {
		return printErr("Repair executor token is empty", datarepair.ErrInvalid)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return printErr("Executor request failed; inspect the plan before retrying apply with the same repair operation ID", datarepair.ErrUnavailable)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		var problem struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, int64(api.DataRepairMaxRequestBytes))).Decode(&problem) == nil && problem.Detail != "" {
			return printErr("Repair refused: "+problem.Detail, fmt.Errorf("HTTP %d (%s)", response.StatusCode, problem.Code))
		}
		return printErr("Repair request refused; inspect the plan and executor status", fmt.Errorf("HTTP %d", response.StatusCode))
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, int64(api.DataRepairMaxPlanBytes+1)))
	if err != nil || len(raw) > api.DataRepairMaxPlanBytes {
		return printErr("Repair response exceeded its bound", datarepair.ErrUnavailable)
	}
	if verb == "validate" {
		return printDataRepairValidation(raw, pos[0], *digest)
	}
	if verb == "verify" {
		return printDataRepairVerification(raw, pos[0], *digest, false)
	}
	var plan datarepair.Plan
	if json.Unmarshal(raw, &plan) != nil {
		return printErr("Invalid repair response", datarepair.ErrUnavailable)
	}
	if verb == "replace" && !validReplacementCLI(plan, pos[0], *digest, *repairID) {
		return printErr("Replacement response did not match its saved identity", datarepair.ErrUnavailable)
	}
	if verb == "resolve" && (plan.Scope.Validate() != nil || !plan.ResolvedApply(pos[0], *digest, *repairID)) {
		return printErr("Source did not provide a matching receipt or permanent closure", datarepair.ErrUnavailable)
	}
	encoded, _ := json.MarshalIndent(plan, "", "  ")
	if _, err := fmt.Fprintln(osStdout, string(encoded)); err != nil {
		return printErr("Cannot write repair result", err)
	}
	return 0
}
