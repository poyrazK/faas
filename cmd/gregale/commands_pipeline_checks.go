package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type connectionCheckClient interface {
	ListDataConnections(context.Context) (api.DataConnectionList, error)
	RequestDataConnectionCheck(context.Context, string, api.RequestDataConnectionCheck) (api.DataConnectionCheck, error)
	GetDataConnectionCheck(context.Context, string) (api.DataConnectionCheck, error)
}

func requestConnectionCheck(ctx context.Context, client connectionCheckClient, id, database string, selections ...api.RequestDataConnectionCheck) (api.DataConnectionCheck, error) {
	var err error
	id, err = canonicalPipelineID(id)
	if err != nil {
		return api.DataConnectionCheck{}, err
	}
	if database != "" {
		database, err = canonicalPipelineID(database)
		if err != nil {
			return api.DataConnectionCheck{}, err
		}
	}
	connections, err := client.ListDataConnections(ctx)
	if err != nil {
		return api.DataConnectionCheck{}, err
	}
	for _, c := range connections.Items {
		if c.ID != id {
			continue
		}
		request := api.RequestDataConnectionCheck{ExpectedRevision: c.Revision, DatabaseID: database}
		if len(selections) > 0 {
			request.Tables, request.Search = selections[0].Tables, selections[0].Search
		}
		ctx = api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("pipeline-check", id, fmt.Sprint(c.Revision), database, strings.Join(request.Tables, ","), request.Search))
		return client.RequestDataConnectionCheck(ctx, id, request)
	}
	return api.DataConnectionCheck{}, errors.New("source connection not found; use pipeline connection list")
}

func canonicalPipelineID(value string) (string, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return "", errors.New("supply a valid UUID")
	}
	return id.String(), nil
}

func waitConnectionCheck(ctx context.Context, client connectionCheckClient, check api.DataConnectionCheck, interval time.Duration) (api.DataConnectionCheck, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	id := check.ID
	for {
		if check.ID != id || check.State == "stale" {
			return check, errors.New("connection check changed; run it again")
		}
		if check.State == "passed" || check.State == "blocked" {
			if !check.Fresh {
				return check, errors.New("connection check expired; run it again")
			}
			if check.State == "blocked" {
				return check, errors.New("connection check found blockers; follow the reported remediation")
			}
			return check, nil
		}
		select {
		case <-ctx.Done():
			return check, ctx.Err()
		case <-ticker.C:
			var err error
			check, err = client.GetDataConnectionCheck(ctx, check.ConnectionID)
			if err != nil {
				return check, err
			}
		}
	}
}

func checkConnectionForCreate(ctx context.Context, client connectionCheckClient, id, database string, tables []string) error {
	check, err := requestConnectionCheck(ctx, client, id, database, api.RequestDataConnectionCheck{Tables: tables})
	if err == nil {
		check, err = waitConnectionCheck(ctx, client, check, time.Second)
	}
	if err == nil {
		err = checkCapacityForCreate(check, database != "")
	}
	if err != nil {
		_ = writeJSON(check)
		return err
	}
	for _, name := range tables {
		found := false
		for _, table := range check.Tables {
			if table.Name == name && table.Supported {
				found = true
				break
			}
		}
		if !found {
			_ = writeJSON(check)
			return errors.New("a selected table is unsupported or unavailable; inspect connection tables --tables with the exact selection")
		}
	}
	return nil
}

func checkCapacityForCreate(check api.DataConnectionCheck, target bool) error {
	if check.Capacity == nil || !freshCapacity(check.Capacity.Source) || (target && (!freshCapacity(check.Capacity.Destination) || check.Capacity.State != "ready")) {
		return errors.New("capacity is not ready; follow the connection check remediation before provisioning or starting a sync")
	}
	return nil
}

func freshCapacity(r *api.DataEndpointCapacity) bool {
	if r == nil || r.State != "ready" || r.RequiredConnections < 1 || r.ReservedConnections < 0 || r.UsableConnections-r.ReservedConnections < r.RequiredConnections {
		return false
	}
	observed, first := time.Parse(time.RFC3339Nano, r.ObservedAt)
	expires, second := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	now := time.Now()
	return first == nil && second == nil && !now.Before(observed) && now.Before(expires) && expires.After(observed) && expires.Sub(observed) <= api.DataPipelineCapacityMaxAge
}

func cmdPipelineConnectionCheck(action string, args []string) int {
	fs := newFlagSet("pipeline connection "+action, flag.ContinueOnError)
	var database string
	tables := fs.String("tables", "", "inspect these comma-separated table names, including tables beyond the default discovery page")
	search := fs.String("search", "", "discover table names containing this lowercase text")
	if action == "check" {
		fs.StringVar(&database, "database", "", "also check an existing managed mirror ID")
	}
	wait := fs.Bool("wait", true, "wait for a fresh result")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum wait, up to 1h")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *timeout <= 0 || *timeout > time.Hour || (*tables != "" && *search != "") {
		return printErr("Invalid arguments", errors.New("supply a connection ID and a timeout up to 1h"))
	}
	id, err := canonicalPipelineID(fs.Arg(0))
	if err != nil {
		return printErr("Invalid connection ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var check api.DataConnectionCheck
	selection := api.RequestDataConnectionCheck{Search: *search}
	if *tables != "" {
		selection.Tables = strings.Split(*tables, ",")
		for i := range selection.Tables {
			selection.Tables[i] = strings.TrimSpace(selection.Tables[i])
		}
	}
	if action == "tables" && *tables == "" && *search == "" {
		check, err = client.GetDataConnectionCheck(ctx, id)
	} else {
		check, err = requestConnectionCheck(ctx, client, id, database, selection)
	}
	if err == nil && *wait {
		check, err = waitConnectionCheck(ctx, client, check, time.Second)
	}
	if writeErr := writeJSON(check); writeErr != nil {
		return jsonOut(writeErr)
	}
	if err != nil {
		return printErr("Connection check ended; run connection check to refresh", err)
	}
	return 0
}
