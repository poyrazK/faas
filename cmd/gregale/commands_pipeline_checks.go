package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type connectionCheckClient interface {
	ListDataConnections(context.Context) (api.DataConnectionList, error)
	RequestDataConnectionCheck(context.Context, string, api.RequestDataConnectionCheck) (api.DataConnectionCheck, error)
	GetDataConnectionCheck(context.Context, string) (api.DataConnectionCheck, error)
}

func requestConnectionCheck(ctx context.Context, client connectionCheckClient, id, database string) (api.DataConnectionCheck, error) {
	connections, err := client.ListDataConnections(ctx)
	if err != nil {
		return api.DataConnectionCheck{}, err
	}
	for _, c := range connections.Items {
		if c.ID != id {
			continue
		}
		request := api.RequestDataConnectionCheck{ExpectedRevision: c.Revision, DatabaseID: database}
		ctx = api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("pipeline-check", id, fmt.Sprint(c.Revision), database))
		return client.RequestDataConnectionCheck(ctx, id, request)
	}
	return api.DataConnectionCheck{}, errors.New("source connection not found; use pipeline connection list")
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
	check, err := requestConnectionCheck(ctx, client, id, database)
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
			return errors.New("a selected table is unsupported or outside discovery limits; inspect connection tables")
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
	if action == "check" {
		fs.StringVar(&database, "database", "", "also check an existing managed mirror ID")
	}
	wait := fs.Bool("wait", true, "wait for a fresh result")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum wait, up to 1h")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *timeout <= 0 || *timeout > time.Hour {
		return printErr("Invalid arguments", errors.New("supply a connection ID and a timeout up to 1h"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var check api.DataConnectionCheck
	if action == "tables" {
		check, err = client.GetDataConnectionCheck(ctx, fs.Arg(0))
	} else {
		check, err = requestConnectionCheck(ctx, client, fs.Arg(0), database)
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
