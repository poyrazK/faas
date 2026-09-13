package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/fleetseal"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const dispatchFleetSeal = "fleet-seal"

func cmdFleetSeal(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, "usage: gregalectl fleet-seal <init|migrate|verify> [flags]", "fleet-seal")
		return 0
	}
	var err error
	switch args[0] {
	case "init":
		err = runFleetSealInit(args[1:])
	case "migrate":
		err = runFleetSealMigrate(args[1:])
	case "verify":
		err = runFleetSealVerify(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl fleet-seal: unknown subcommand %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl fleet-seal %s: %v\n", args[0], err)
		return 3
	}
	return 0
}

func runFleetSealInit(args []string) error {
	fs := flag.NewFlagSet("fleet-seal init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", filepath.Dir(secretbox.DefaultFleetKeyPath), "directory for fleet.age and fleet.age.pub")
	force := fs.Bool("force", false, "replace an existing fleet identity (requires a complete migrate before adding capacity)")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional argument")
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return err
	}
	keyPath := filepath.Join(*dir, "fleet.age")
	pubPath := filepath.Join(*dir, "fleet.age.pub")
	if !*force {
		if _, err := os.Stat(keyPath); err == nil {
			return fmt.Errorf("%s already exists (use --force only for a coordinated fleet migration)", keyPath)
		}
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return err
	}
	if err := secretbox.WriteHostKeyAtPath(keyPath, id); err != nil {
		return err
	}
	if err := secretbox.WriteRecipientFile(pubPath, id); err != nil {
		return err
	}
	report := map[string]string{"identity_path": keyPath, "recipient_path": pubPath, "recipient": id.Recipient().String()}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(report)
	}
	PrintOK(osStdout, "Fleet seal identity written to %s; recipient %s\n", keyPath, id.Recipient())
	return nil
}

func runFleetSealMigrate(args []string) error {
	fs := flag.NewFlagSet("fleet-seal migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fleetKey := fs.String("fleet-key", secretbox.DefaultFleetKeyPath, "fleet.age identity")
	legacyDir := fs.String("legacy-host-dir", filepath.Dir(secretbox.DefaultHostKeyPath), "directory containing host.age and optional host.age.previous")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN (default: FAAS_PG_DSN, DATABASE_URL, or deploy env)")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional argument")
	}
	fleet, err := secretbox.LoadHostKey(*fleetKey)
	if err != nil {
		return err
	}
	legacy, err := secretbox.LoadHostKeys(*legacyDir)
	if err != nil && !errors.Is(err, secretbox.ErrHostKeyNotFound) {
		return err
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := db.MigrateUp(ctx, pool); err != nil {
		return fmt.Errorf("apply schema before fleet migration: %w", err)
	}
	report, err := fleetseal.Migrate(ctx, state.NewPgStore(pool), fleet, legacy)
	if err != nil {
		return err
	}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(report)
	}
	PrintOK(osStdout, "Fleet domain migrated: cluster kid %s, %d/%d customer secrets re-sealed, probe written\n", report.ClusterKID, report.SecretsResealed, report.SecretsScanned)
	return nil
}

func runFleetSealVerify(args []string) error {
	fs := flag.NewFlagSet("fleet-seal verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fleetKey := fs.String("fleet-key", secretbox.DefaultFleetKeyPath, "staged fleet.age identity")
	hostKey := fs.String("host-key", secretbox.DefaultHostKeyPath, "per-host host.age identity")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	metricsFile := fs.String("metrics-file", "", "optional node_exporter textfile output")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fleet, err := secretbox.LoadHostKey(*fleetKey)
	if err != nil {
		_ = writeFleetSealMetric(*metricsFile, fleetseal.VerificationReport{}, false)
		return err
	}
	host, err := secretbox.LoadHostKey(*hostKey)
	if err != nil {
		_ = writeFleetSealMetric(*metricsFile, fleetseal.VerificationReport{FleetRecipient: fleet.Recipient().String()}, false)
		return err
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		_ = writeFleetSealMetric(*metricsFile, fleetseal.VerificationReport{FleetRecipient: fleet.Recipient().String()}, false)
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := fleetseal.Verify(ctx, state.NewPgStore(pool), fleet, host)
	if metricErr := writeFleetSealMetric(*metricsFile, report, err == nil && report.Ready); metricErr != nil && err == nil {
		err = metricErr
	}
	if err != nil {
		return err
	}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(report)
	}
	PrintOK(osStdout, "Fleet seal ready: recipient %s, cluster kid %s, customer probe and JWT round-trip verified\n", report.FleetRecipient, report.ClusterKID)
	return nil
}

func openFleetSealPool(explicit, envFile string) (*pgxpool.Pool, error) {
	dsn := strings.TrimSpace(explicit)
	if dsn == "" && envFile != "" {
		if value, ok := readDatabaseEnvFile(envFile); ok {
			dsn = value
		}
	}
	if dsn == "" {
		dsn = resolveSecretsDSN()
	}
	if dsn == "" {
		return nil, errors.New("database DSN not set")
	}
	return openPgPoolFromDSN(dsn)
}

func writeFleetSealMetric(path string, report fleetseal.VerificationReport, ready bool) error {
	if path == "" {
		return nil
	}
	value := 0
	if ready {
		value = 1
	}
	body := "# HELP faas_fleet_seal_domain_ready 1 when this node opened the shared customer probe and cluster signing key.\n" +
		"# TYPE faas_fleet_seal_domain_ready gauge\n" +
		"faas_fleet_seal_domain_ready " + strconv.Itoa(value) + "\n"
	if report.ClusterKID != "" {
		body += "# HELP faas_fleet_cluster_signing_key_info Active shared cluster signing key on this node.\n" +
			"# TYPE faas_fleet_cluster_signing_key_info gauge\n" +
			"faas_fleet_cluster_signing_key_info{kid=\"" + strings.ReplaceAll(report.ClusterKID, "\"", "") + "\"} " + strconv.Itoa(value) + "\n"
	}
	if ready {
		body += "# HELP faas_fleet_seal_verified_timestamp_seconds Unix time of the last successful fleet seal verification.\n" +
			"# TYPE faas_fleet_seal_verified_timestamp_seconds gauge\n" +
			fmt.Sprintf("faas_fleet_seal_verified_timestamp_seconds %d\n", time.Now().Unix())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fleet-seal-prom-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
