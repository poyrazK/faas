package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func cmdPipeline(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale pipeline <connection|create|list|status|wait|pause|resume|resync|verify|delete>", "pipeline")
		return 1
	}
	switch args[0] {
	case "connection":
		return cmdPipelineConnection(args[1:])
	case "create":
		return cmdPipelineCreate(args[1:])
	case "list":
		if len(args) != 1 {
			return printErr("Invalid arguments", errors.New("pipeline list takes no arguments"))
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		items, err := client.ListDataPipelines(context.Background())
		if err != nil {
			return printErr("Could not list pipelines", err)
		}
		return jsonOut(writeJSON(items))
	case "status", "wait", "pause", "resume", "resync", "verify", "delete":
		return cmdPipelineControl(args[0], args[1:])
	default:
		return printErr("Unknown pipeline command", errors.New("use connection, create, list, status, wait, pause, resume, resync, verify or delete"))
	}
}

func cmdPipelineConnection(args []string) int {
	if len(args) == 0 {
		return printErr("Missing connection command", errors.New("use add, list, check, tables, rotate or delete"))
	}
	if args[0] == "check" || args[0] == "tables" {
		return cmdPipelineConnectionCheck(args[0], args[1:])
	}
	fs := newFlagSet("pipeline connection "+args[0], flag.ContinueOnError)
	var file string
	if args[0] == "add" || args[0] == "rotate" {
		fs.StringVar(&file, "url-file", "", "private file containing a verified-TLS PostgreSQL URL")
	}
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	if args[0] == "list" && fs.NArg() != 0 || args[0] != "list" && fs.NArg() != 1 {
		return printErr("Invalid arguments", errors.New("supply a connection name for add or an ID for rotate/delete"))
	}
	var raw string
	if args[0] == "add" || args[0] == "rotate" {
		var err error
		raw, err = readCommitConnectionFile(file)
		if err != nil {
			return printErr("Could not read connection file", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch args[0] {
	case "add":
		request := api.CreateDataConnectionRequest{Name: fs.Arg(0), ConnectionURL: raw}
		out, err := client.CreateDataConnection(api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("pipeline-source", request.Name, raw)), request)
		if err != nil {
			return printErr("Could not register connection", err)
		}
		return jsonOut(writeJSON(out))
	case "list":
		out, err := client.ListDataConnections(ctx)
		if err != nil {
			return printErr("Could not list connections", err)
		}
		return jsonOut(writeJSON(out))
	case "rotate":
		items, err := client.ListDataConnections(ctx)
		if err != nil {
			return printErr("Could not inspect connection", err)
		}
		for _, c := range items.Items {
			if c.ID == fs.Arg(0) {
				out, err := client.RotateDataConnection(ctx, c.ID, api.RotateDataConnectionRequest{ExpectedRevision: c.Revision, ConnectionURL: raw})
				if err != nil {
					return printErr("Could not rotate connection", err)
				}
				return jsonOut(writeJSON(out))
			}
		}
		return printErr("Connection not found", errors.New("use pipeline connection list to inspect owned connections"))
	case "delete":
		if err := client.DeleteDataConnection(ctx, fs.Arg(0)); err != nil {
			return printErr("Could not delete connection", err)
		}
		return 0
	default:
		return printErr("Unknown connection command", errors.New("use add, list, check, tables, rotate or delete"))
	}
}

func cmdPipelineCreate(args []string) int {
	fs := newFlagSet("pipeline create", flag.ContinueOnError)
	connection := fs.String("connection", "", "source connection ID")
	sourceFile := fs.String("url-file", "", "register a source from a private URL file instead of --connection")
	database := fs.String("database", "", "existing empty managed database; otherwise create NAME-mirror")
	region := fs.String("region", "", "placement for an automatically created mirror")
	tables := fs.String("tables", "", "comma-separated public table names")
	subjects := fs.String("read-subjects", "", "comma-separated application JWT subjects allowed to read the mirror")
	wait := fs.Bool("wait", false, "wait until initial copying and catch-up complete")
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum provisioning and pipeline wait (maximum 1h)")
	apiName := unreleasedStringFlag(fs, "api", "deploy a read-only Data API after initial sync")
	authApp := unreleasedStringFlag(fs, "auth", "Auth app for --api")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || ((*connection == "") == (*sourceFile == "")) || *tables == "" || *subjects == "" || *timeout <= 0 || *timeout > time.Hour || (*apiName != "" && (*authApp == "" || !api.ValidAppSlug(*apiName))) {
		return printErr("Invalid pipeline", errors.New("supply NAME, one of --connection/--url-file, --tables and --read-subjects; --api also requires --auth"))
	}
	validationID := *connection
	if validationID == "" {
		validationID = uuid.NewString()
	}
	validation := api.CreateDataPipelineRequest{Name: fs.Arg(0), SourceConnectionID: validationID, DatabaseID: validationID, Tables: strings.Split(*tables, ","), ReadSubjects: strings.Split(*subjects, ",")}
	if err := datapipeline.ValidateCreate(validation); err != nil {
		return printErr("Invalid pipeline", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *sourceFile != "" {
		raw, readErr := readCommitConnectionFile(*sourceFile)
		if readErr != nil {
			return printErr("Could not read source credential", readErr)
		}
		sourceName := fs.Arg(0)
		if len(sourceName) > 32 {
			sourceName = sourceName[:32]
		}
		sourceName += "-source"
		registered, createErr := client.CreateDataConnection(api.ContextWithIdempotencyKey(context.Background(), addResourceIdempotencyKey("pipeline-source", sourceName, raw)), api.CreateDataConnectionRequest{Name: sourceName, ConnectionURL: raw})
		if createErr != nil {
			return printErr("Could not register source; inspect pipeline connection list before retrying", createErr)
		}
		*connection = registered.ID
	} else {
		connections, listErr := client.ListDataConnections(context.Background())
		if listErr != nil {
			return printErr("Could not inspect source connection", listErr)
		}
		found := false
		for _, c := range connections.Items {
			found = found || c.ID == *connection
		}
		if !found {
			return printErr("Source connection not found", errors.New("register a source with pipeline connection add before creating the mirror"))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err = checkConnectionForCreate(ctx, client, *connection, "", strings.Split(*tables, ",")); err != nil {
		return printErr("Source compatibility check failed before mirror provisioning", err)
	}
	var db api.ManagedPostgresDatabase
	if *database != "" {
		db, err = resolveManagedPostgresDatabase(ctx, client, *database)
	} else {
		if len(fs.Arg(0)) > 55 {
			return printErr("Name too long", errors.New("use a name of at most 55 characters or supply --database"))
		}
		if *region == "" {
			caps, capErr := client.GetManagedPostgresCapabilities(ctx, "")
			if capErr != nil {
				return printErr("Could not inspect placement", capErr)
			}
			*region = caps.Region
		}
		req := api.CreateManagedPostgresDatabaseRequest{Name: fs.Arg(0) + "-mirror", Region: *region, PostgresMajor: 16}
		db, err = client.CreateManagedPostgresDatabase(api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("pipeline-mirror", req.Name, req.Region)), req)
	}
	if err != nil {
		return printErr("Could not prepare mirror database", err)
	}
	db, err = waitForManagedPostgresDatabase(ctx, client, db, *timeout)
	if err != nil {
		return printErr("Mirror is retained; retry with --database "+db.ID, err)
	}
	if err = checkConnectionForCreate(ctx, client, *connection, db.ID, strings.Split(*tables, ",")); err != nil {
		return printErr("Mirror check failed; database is retained as "+db.ID, err)
	}
	req := api.CreateDataPipelineRequest{Name: fs.Arg(0), SourceConnectionID: *connection, DatabaseID: db.ID, Tables: strings.Split(*tables, ","), ReadSubjects: strings.Split(*subjects, ",")}
	p, err := client.CreateDataPipeline(api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("pipeline", req.Name, db.ID, *connection)), req)
	if err != nil {
		return printErr("Mirror is retained; retry with --database "+db.ID, err)
	}
	if *wait || *apiName != "" {
		p, err = waitDataPipeline(ctx, client, p, "live", time.Second)
		if err != nil {
			_ = writeJSON(p)
			return printErr("Pipeline is retained for recovery", err)
		}
	}
	if err = writeJSON(p); err != nil {
		return jsonOut(err)
	}
	if *apiName != "" {
		return cmdDataAPICreate([]string{*apiName, "--database", db.ID, "--read-only", "--schema", "public", "--auth", *authApp})
	}
	return 0
}

func cmdPipelineControl(action string, args []string) int {
	fs := newFlagSet("pipeline "+action, flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the requested observation")
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum wait (maximum 1h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *timeout <= 0 || *timeout > time.Hour {
		return printErr("Invalid arguments", errors.New("supply a pipeline ID and a timeout up to 1h"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	p, err := client.GetDataPipeline(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not read pipeline", err)
	}
	if action != "status" && action != "wait" {
		p, err = client.ControlDataPipeline(ctx, p.ID, api.ControlDataPipelineRequest{ExpectedRevision: p.Revision, Action: action})
		if err != nil {
			return printErr("Could not request pipeline action", err)
		}
	}
	if *wait || action == "wait" {
		expected := "live"
		if action == "status" && (p.DesiredState == "paused" || p.DesiredState == "deleted") {
			expected = p.DesiredState
		}
		if action == "pause" {
			expected = "paused"
		}
		if action == "delete" {
			expected = "deleted"
		}
		if action == "verify" {
			expected = "verified"
		}
		p, err = waitDataPipeline(ctx, client, p, expected, time.Second)
		if err != nil {
			_ = writeJSON(p)
			return printErr("Pipeline wait ended", err)
		}
	}
	return jsonOut(writeJSON(p))
}

type pipelineReader interface {
	GetDataPipeline(context.Context, string) (api.DataPipeline, error)
}

func waitDataPipeline(ctx context.Context, client pipelineReader, p api.DataPipeline, expected string, interval time.Duration) (api.DataPipeline, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		// Cleanup is a terminal durable receipt; deleted rows are no longer polled.
		if p.State == "deleted" {
			if expected == "deleted" && p.ObservedRevision == p.Revision && p.Revision > 0 {
				return p, nil
			}
			return p, errors.New("pipeline has been deleted")
		}
		if (p.State == expected && p.ObservationFresh) || (expected == "verified" && p.ObservationFresh && p.Verification == "passed") {
			return p, nil
		}
		if expected == "verified" && p.ObservationFresh && p.Verification == "inconclusive" {
			return p, errors.New("verification was inconclusive; inspect saved progress in Data connections, then retry or use explicit recovery")
		}
		if (p.State == "resync_required" || (p.State == "blocked" && p.Reason != "database_unavailable")) && expected != "deleted" && expected != "paused" {
			return p, fmt.Errorf("pipeline %s: %s", p.State, p.Reason)
		}
		select {
		case <-ctx.Done():
			return p, ctx.Err()
		case <-ticker.C:
			var err error
			p, err = client.GetDataPipeline(ctx, p.ID)
			if err != nil {
				return p, err
			}
		}
	}
}
