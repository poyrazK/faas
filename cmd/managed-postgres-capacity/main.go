// Command managed-postgres-capacity refreshes one database's capacity cache.
// Privileged SQL observation stays outside apid and customer request handling.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
)

type capacityOptions struct {
	account, database, allocation string
	watch                         bool
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(os.Stderr, "managed-postgres-capacity:", err)
		os.Exit(1)
	}
}

func parseOptions(args []string, errorOutput io.Writer) (capacityOptions, error) {
	var out capacityOptions
	flags := flag.NewFlagSet("managed-postgres-capacity", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	flags.StringVar(&out.account, "account", "", "account UUID")
	flags.StringVar(&out.database, "database", "", "managed database UUID")
	flags.StringVar(&out.allocation, "allocation", "", "trusted operator allocation JSON file (reread on every observation)")
	flags.BoolVar(&out.watch, "watch", false, "refresh every 15 seconds until interrupted")
	if err := flags.Parse(args); err != nil {
		return out, err
	}
	for _, id := range []string{out.account, out.database} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return out, errors.New("--account and --database require canonical UUIDs")
		}
	}
	if out.allocation == "" || flags.NArg() != 0 {
		return out, errors.New("usage: managed-postgres-capacity --account UUID --database UUID --allocation PATH [--watch]")
	}
	return out, nil
}

func run(ctx context.Context, args []string, getenv func(string) string, output, errorOutput io.Writer) error {
	options, err := parseOptions(args, errorOutput)
	if err != nil {
		return err
	}
	registry, err := managedpostgres.Load(getenv, map[string]managedpostgres.Factory{"neon": neon.New})
	if err != nil || registry == nil {
		return errors.New("managed PostgreSQL configuration unavailable")
	}
	config, err := pgxpool.ParseConfig(getenv("DATABASE_URL"))
	if err != nil || getenv("DATABASE_URL") == "" {
		return errors.New("DATABASE_URL is required and must be valid")
	}
	config.MaxConns = api.DataAPIConnectionCapacityControlPool
	config.MinConns = 0
	config.ConnConfig.RuntimeParams["application_name"] = "gregale-capacity-observer"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return errors.New("capacity catalog connection unavailable")
	}
	defer pool.Close()
	store, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		return errors.New("capacity catalog unavailable")
	}
	return runLoop(ctx, options, output, errorOutput, func(ctx context.Context, allocation *managedpostgres.QualifiedConnectionAllocation) (managedpostgres.ConnectionCapacityResult, error) {
		return managedpostgres.ObserveConnectionCapacity(ctx, registry, store, options.account, options.database, allocation)
	})
}

func readAllocation(path string) *managedpostgres.QualifiedConnectionAllocation {
	// This is an explicit operator file, never a customer-supplied path.
	file, err := os.Open(path) //nolint:forbidigo
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	allocation, err := managedpostgres.DecodeConnectionAllocation(file)
	if err != nil {
		return nil
	}
	return allocation
}

func runLoop(ctx context.Context, options capacityOptions, output, errorOutput io.Writer,
	observe func(context.Context, *managedpostgres.QualifiedConnectionAllocation) (managedpostgres.ConnectionCapacityResult, error)) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result, err := observe(ctx, readAllocation(options.allocation))
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !options.watch {
				return errors.New("capacity observation unavailable; no capacity was qualified")
			}
			_, _ = fmt.Fprintln(errorOutput, "capacity observation unavailable; retrying")
		} else {
			// No backend, provider, dataset, role, host or credential identities
			// from the private evidence file are emitted by this command.
			view := struct {
				State     string `json:"state"`
				Reason    string `json:"reason,omitempty"`
				CheckedAt string `json:"checked_at"`
				ExpiresAt string `json:"expires_at"`
			}{result.State, result.Reason, result.CheckedAt.Format(time.RFC3339Nano), result.ExpiresAt.Format(time.RFC3339Nano)}
			if err := json.NewEncoder(output).Encode(view); err != nil {
				return errors.New("capacity result output failed")
			}
			if !options.watch {
				if result.State != "verified" {
					return errors.New("capacity remains unknown; inspect the observation reason")
				}
				return nil
			}
		}
		timer := time.NewTimer(api.DataAPIConnectionCapacityInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
