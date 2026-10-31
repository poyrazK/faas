package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/state"
)

const dispatchCredentials = "credentials"

func cmdCredentials(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, "usage: gregalectl credentials <keygen|status> [flags]", dispatchCredentials)
		return 0
	}
	var err error
	switch args[0] {
	case "keygen":
		err = runCredentialsKeygen(args[1:])
	case "status":
		err = runCredentialsStatus(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl credentials: unknown subcommand %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl credentials %s: %v\n", args[0], err)
		return 3
	}
	return 0
}

// runCredentialsKeygen writes apid's account credential key ring (ADR
// 20261029202403). It runs offline, on the operator's machine; the
// control-plane role installs the ring for apid. With --rotate-from it
// writes a new current key ahead of every key of an existing ring.
func runCredentialsKeygen(args []string) error {
	fs := flag.NewFlagSet("credentials keygen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("out", "", "new ring path (written 0600; must not exist)")
	rotateFrom := fs.String("rotate-from", "", "existing ring whose keys follow the new one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("--out is required")
	}
	kid, previous := 1, []byte(nil)
	if *rotateFrom != "" {
		data, err := os.ReadFile(*rotateFrom)
		if err != nil {
			return fmt.Errorf("read --rotate-from: %w", err)
		}
		ring, err := credhash.ParseRing(data)
		if err != nil {
			return err
		}
		if kid, err = ring.NextKeyID(); err != nil {
			return err
		}
		previous = data
	}
	line, err := credhash.NewKeyLine(kid)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append([]byte(line+"\n"), previous...))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	PrintOK(osStdout, "Credential key ring written to %s; current key %d\n", *out, kid)
	return nil
}

// runCredentialsStatus counts every account credential column's stored
// values by key, plain hashes as key -1. It reads lengths and key IDs only,
// so it needs no key.
func runCredentialsStatus(args []string) error {
	fs := flag.NewFlagSet("credentials status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN (default: FAAS_PG_DSN, DATABASE_URL, or deploy env)")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rows, err := state.NewPgStore(pool).CredentialHashStatus(ctx)
	if err != nil {
		return err
	}
	if *jsonOut || jsonOutput {
		if rows == nil {
			rows = []state.CredentialColumnStatus{}
		}
		return json.NewEncoder(osStdout).Encode(rows)
	}
	for _, r := range rows {
		key := fmt.Sprint(r.KeyID)
		if r.KeyID < 0 {
			key = "plain"
		}
		_, _ = fmt.Fprintf(osStdout, "%s key=%s rows=%d\n", r.Column, key, r.Rows)
	}
	return nil
}
