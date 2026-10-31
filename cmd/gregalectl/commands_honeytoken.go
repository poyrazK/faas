package main

// ADR 20261024014500: honeytokens, decoy Gregale API keys that belong to no
// account. apid pages when one is presented. The commands write PostgreSQL
// directly from a control-plane node, which holds the host HMAC key the
// registry is keyed by.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const dispatchHoneytoken = "honeytoken"

const (
	defaultHoneytokenHMACKey   = "/etc/faas/secrets/host.hmac.key"
	defaultHoneytokenRecipient = "/etc/faas/secrets/fleet.age.pub"
	defaultHoneytokenVar       = "GREGALE_DEPLOY_KEY"
)

func cmdHoneytoken(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, "usage: gregalectl honeytoken <plant|list|retire> [flags]", dispatchHoneytoken)
		return 0
	}
	var err error
	switch args[0] {
	case "plant":
		err = runHoneytokenPlant(args[1:])
	case "list":
		err = runHoneytokenList(args[1:])
	case "retire":
		err = runHoneytokenRetire(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl honeytoken: unknown subcommand %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl honeytoken %s: %v\n", args[0], err)
		return 3
	}
	return 0
}

// honeytokenStore opens the registry on the database named by dsn or dbEnv.
func honeytokenStore(dsn, dbEnv string) (*state.PgStore, func(), error) {
	pool, err := openFleetSealPool(dsn, dbEnv)
	if err != nil {
		return nil, nil, err
	}
	return state.NewPgStore(pool), pool.Close, nil
}

type honeytokenPlantOpts struct {
	label, appID, varName, recipientFile, hmacKeyFile string
	print                                             bool
}

// runHoneytokenPlant mints a decoy API key, registers its digest and plants
// it: sealed into an app secret of an app that never runs, or printed once
// for planting by hand.
func runHoneytokenPlant(args []string) error {
	fs := flag.NewFlagSet("honeytoken plant", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var o honeytokenPlantOpts
	fs.StringVar(&o.label, "label", "", "opaque reference, e.g. ht-2026-10-24-a; it is stored in the database, so it must not say where the token is planted")
	fs.StringVar(&o.appID, "app-id", "", "app whose secrets get the decoy; it must never have been deployed")
	fs.StringVar(&o.varName, "var", defaultHoneytokenVar, "secret name the decoy is stored under")
	fs.StringVar(&o.recipientFile, "recipient-file", defaultHoneytokenRecipient, "recipient app secrets are sealed to (the runtime domain's once it is in domain mode)")
	fs.StringVar(&o.hmacKeyFile, "hmac-key", defaultHoneytokenHMACKey, "apid's host HMAC key")
	fs.BoolVar(&o.print, "print", false, "print the decoy once instead of planting it in an app")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	o.label = strings.TrimSpace(o.label)
	if o.label == "" {
		return errors.New("--label is required")
	}
	if (o.appID == "") == !o.print {
		return errors.New("pass exactly one of --app-id and --print")
	}
	hmacKey, err := readHoneytokenHMACKey(o.hmacKeyFile)
	if err != nil {
		return err
	}
	store, closeStore, err := honeytokenStore(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer closeStore()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, h, err := plantHoneytoken(ctx, store, o, hmacKey)
	if err != nil {
		return err
	}
	out := map[string]any{"id": h.ID, "label": h.Label, "kind": h.Kind}
	if o.print {
		out["token"] = token
	} else {
		out["app_id"], out["var"] = o.appID, o.varName
	}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(out)
	}
	if o.print {
		PrintOK(osStdout, "Honeytoken %s registered. Plant this value, then forget it:\n%s\n", h.ID, token)
		return nil
	}
	PrintOK(osStdout, "Honeytoken %s planted as %s in app %s. Record where in the incident runbook, not in the label.\n", h.ID, o.varName, o.appID)
	return nil
}

func plantHoneytoken(ctx context.Context, store *state.PgStore, o honeytokenPlantOpts, hmacKey []byte) (string, state.Honeytoken, error) {
	var (
		app       state.App
		recipient *age.X25519Recipient
	)
	if !o.print {
		var err error
		if app, err = store.AppByID(ctx, o.appID); err != nil {
			return "", state.Honeytoken{}, fmt.Errorf("load app %s: %w", o.appID, err)
		}
		switch _, err := store.LatestDeployment(ctx, app.ID); {
		case err == nil:
			return "", state.Honeytoken{}, fmt.Errorf("app %s has deployments; plant in an app that never runs, or its workload would receive the decoy", app.ID)
		case !errors.Is(err, state.ErrNotFound):
			return "", state.Honeytoken{}, fmt.Errorf("check app %s deployments: %w", app.ID, err)
		}
		if recipient, err = secretbox.LoadRecipient(o.recipientFile); err != nil {
			return "", state.Honeytoken{}, err
		}
	}
	token, _, err := api.GenerateAPIKey()
	if err != nil {
		return "", state.Honeytoken{}, err
	}
	digest, err := state.HoneytokenDigest(hmacKey, token)
	if err != nil {
		return "", state.Honeytoken{}, err
	}
	h, err := store.CreateHoneytoken(ctx, state.HoneytokenKindAPIKey, digest, o.label)
	if err != nil {
		return "", state.Honeytoken{}, fmt.Errorf("register honeytoken: %w", err)
	}
	if o.print {
		return token, h, nil
	}
	sealed, err := secretbox.SealOne(recipient, o.varName, token, 0)
	if err != nil {
		return "", state.Honeytoken{}, fmt.Errorf("seal honeytoken %s: %w", h.ID, err)
	}
	valueHash, err := secretbox.ValueFingerprint([]byte(token), hmacKey)
	if err != nil {
		return "", state.Honeytoken{}, fmt.Errorf("fingerprint honeytoken %s: %w", h.ID, err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, app.AccountID, app.ID, state.DefaultEnvScope, o.varName, recipient.String(), valueHash, sealed); err != nil {
		return "", state.Honeytoken{}, fmt.Errorf("plant honeytoken %s (registered, not planted; retire it): %w", h.ID, err)
	}
	return token, h, nil
}

func readHoneytokenHMACKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, fmt.Errorf("read host HMAC key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("host HMAC key %s is %d bytes, want 32", path, len(key))
	}
	return key, nil
}

func runHoneytokenList(args []string) error {
	fs := flag.NewFlagSet("honeytoken list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, closeStore, err := honeytokenStore(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer closeStore()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tokens, err := store.ListHoneytokens(ctx)
	if err != nil {
		return err
	}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(tokens)
	}
	for _, h := range tokens {
		status := "active"
		if h.RetiredAt != nil {
			status = "retired " + h.RetiredAt.UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(osStdout, "%s  %s  %s  planted %s  %s\n", h.ID, h.Kind, h.Label, h.PlantedAt.UTC().Format(time.RFC3339), status)
	}
	return nil
}

func runHoneytokenRetire(args []string) error {
	fs := flag.NewFlagSet("honeytoken retire", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	id := fs.String("id", "", "honeytoken id")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return errors.New("--id is required")
	}
	store, closeStore, err := honeytokenStore(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer closeStore()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h, err := store.RetireHoneytoken(ctx, *id)
	if err != nil {
		return fmt.Errorf("retire %s: %w", *id, err)
	}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(h)
	}
	PrintOK(osStdout, "Honeytoken %s retired; it still pages if presented\n", h.ID)
	return nil
}
