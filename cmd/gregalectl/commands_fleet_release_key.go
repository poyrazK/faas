package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// Records a release-secret retirement barrier. Other encryption domains retain
// their own migration and identity-file retirement procedures.
func runFleetSealRetireReleaseKey(args []string) error {
	fs := flag.NewFlagSet("fleet-seal retire-release-key", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	kid := fs.String("kid", "", "previous age recipient to retire from release-secret storage")
	currentKey := fs.String("current-key", secretbox.DefaultFleetKeyPath, "current fleet identity, retained throughout rotation")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN (default: deploy environment)")
	dbEnv := fs.String("db-env", "", "systemd database environment file")
	dry := fs.Bool("dry-run", false, "read dependencies without recording retirement")
	jsonOut := fs.Bool("json", false, "emit recipient and retirement status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional argument")
	}
	if _, err := age.ParseX25519Recipient(*kid); err != nil {
		return errors.New("require a valid previous age recipient with --kid")
	}
	current, err := secretbox.LoadHostKey(*currentKey)
	if err != nil {
		return errors.New("current fleet identity unavailable")
	}
	if current.Recipient().String() == *kid {
		return errors.New("cannot retire the current encryption key")
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return errors.New("database access is required for encrypted release retirement")
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := state.NewPgStore(pool)
	if err := retireHostAgeReleaseSecrets(ctx, store, *kid, *dry); err != nil {
		return err
	}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(struct {
			Kid     string `json:"kid"`
			DryRun  bool   `json:"dry_run"`
			Retired bool   `json:"retired"`
		}{*kid, *dry, !*dry})
	}
	PrintOK(osStdout, "Release-secret key retirement verified (dry_run=%t); encrypted release writes under this recipient are refused after retirement\n", *dry)
	return nil
}
