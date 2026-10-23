package main

// ADR 20261022111812: seal domains. One age identity per reading service;
// writers hold only public recipients.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const dispatchSealDomain = "seal-domain"

func cmdSealDomain(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, "usage: gregalectl seal-domain <init|verify|migrate> --domain NAME [flags]", dispatchSealDomain)
		return 0
	}
	var err error
	switch args[0] {
	case "init":
		err = runSealDomainInit(args[1:])
	case "verify":
		err = runSealDomainVerify(args[1:])
	case "migrate":
		err = runSealDomainMigrate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl seal-domain: unknown subcommand %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl seal-domain %s: %v\n", args[0], err)
		return 3
	}
	return 0
}

func parseSealDomainFlag(name string) (sealdomain.Domain, error) {
	if name == "" {
		return "", errors.New("--domain is required")
	}
	d, err := sealdomain.Parse(name)
	if err != nil {
		return "", err
	}
	if spec, _ := sealdomain.Lookup(d); spec.Dedicated {
		return "", fmt.Errorf("domain %s keeps its dedicated identity and is not managed here", d)
	}
	return d, nil
}

// runSealDomainInit writes a new domain identity and its recipient. It runs
// offline, on the operator's machine: the seal_domains role stages the
// files on the nodes that run the domain's readers.
func runSealDomainInit(args []string) error {
	fs := flag.NewFlagSet("seal-domain init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	domainName := fs.String("domain", "", "seal domain to create")
	dir := fs.String("dir", sealdomain.DefaultDir, "directory for <domain>.age and <domain>.age.pub")
	force := fs.Bool("force", false, "replace an existing identity (requires a complete migrate before readers drop the old one)")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional argument")
	}
	d, err := parseSealDomainFlag(*domainName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return err
	}
	keyPath, pubPath := sealdomain.IdentityPath(*dir, d), sealdomain.RecipientPath(*dir, d)
	if !*force {
		if _, err := os.Stat(keyPath); err == nil {
			return fmt.Errorf("%s already exists (use --force only for a coordinated domain rotation)", keyPath)
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
	report := map[string]string{"domain": string(d), "identity_path": keyPath, "recipient_path": pubPath, "recipient": id.Recipient().String()}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(report)
	}
	PrintOK(osStdout, "Seal domain %s identity written to %s; recipient %s\n", d, keyPath, id.Recipient())
	return nil
}

type sealDomainVerifyReport struct {
	Domain       sealdomain.Domain `json:"domain"`
	Recipient    string            `json:"recipient"`
	ProbeWritten bool              `json:"probe_written"`
	Ready        bool              `json:"ready"`
}

// runSealDomainVerify proves that this node's staged identity opens the
// domain's probe. With --publish it first writes the probe when none exists
// (the first node to stage a new domain); a probe sealed to another
// recipient always fails, because writers would then seal to a key this
// node cannot open.
func runSealDomainVerify(args []string) error {
	fs := flag.NewFlagSet("seal-domain verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	domainName := fs.String("domain", "", "seal domain to verify")
	dir := fs.String("dir", sealdomain.DefaultDir, "directory holding <domain>.age")
	publish := fs.Bool("publish", false, "write the probe when the domain has none yet")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	metricsFile := fs.String("metrics-file", "", "optional node_exporter textfile output")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := parseSealDomainFlag(*domainName)
	if err != nil {
		return err
	}
	report := sealDomainVerifyReport{Domain: d}
	fail := func(err error) error {
		_ = writeSealDomainMetric(*metricsFile, d, false)
		return err
	}
	id, err := secretbox.LoadHostKey(sealdomain.IdentityPath(*dir, d))
	if err != nil {
		return fail(err)
	}
	report.Recipient = id.Recipient().String()
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return fail(err)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := state.NewPgStore(pool)
	probe, err := store.LoadSealDomainProbe(ctx, string(d))
	if errors.Is(err, state.ErrNotFound) && *publish {
		blob, sealErr := sealdomain.SealProbe(d, id.Recipient())
		if sealErr != nil {
			return fail(sealErr)
		}
		if err := store.UpsertSealDomainProbe(ctx, string(d), report.Recipient, blob); err != nil {
			return fail(err)
		}
		report.ProbeWritten = true
		probe, err = store.LoadSealDomainProbe(ctx, string(d))
	}
	if err != nil {
		return fail(fmt.Errorf("load %s probe: %w", d, err))
	}
	if probe.Recipient != report.Recipient {
		return fail(fmt.Errorf("the %s probe is sealed to %s but this node stages %s", d, probe.Recipient, report.Recipient))
	}
	if err := sealdomain.OpenProbe(d, []*age.X25519Identity{id}, probe.SealedBlob); err != nil {
		return fail(err)
	}
	report.Ready = true
	if err := writeSealDomainMetric(*metricsFile, d, true); err != nil {
		return err
	}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(report)
	}
	PrintOK(osStdout, "Seal domain %s ready: recipient %s, probe verified\n", d, report.Recipient)
	return nil
}

// runSealDomainMigrate re-seals a domain's stored envelopes (ADR
// 20261022111812, step 3). For outbound it re-seals every credential to
// the domain alone, so it refuses to run until the operator states that
// every outboundd holds the domain identity. For source it writes the
// sealed copy of webhook secrets stored only in plaintext.
func runSealDomainMigrate(args []string) error {
	fs := flag.NewFlagSet("seal-domain migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	domainName := fs.String("domain", "", "seal domain to migrate (outbound, runtime or source)")
	dir := fs.String("dir", sealdomain.DefaultDir, "directory holding <domain>.age")
	fleetKey := fs.String("fleet-key", secretbox.DefaultFleetKeyPath, "fleet.age identity (its .previous twin is loaded when present)")
	hostKey := fs.String("host-key", secretbox.DefaultHostKeyPath, "host.age identity for legacy rows (optional)")
	readersVerified := fs.Bool("readers-verified", false, "every reader of the domain verified its identity (gregalectl seal-domain verify) and the previous release left the rollback window")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN (default: FAAS_PG_DSN, DATABASE_URL, or deploy env)")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := parseSealDomainFlag(*domainName)
	if err != nil {
		return err
	}
	if d != sealdomain.Outbound && d != sealdomain.Source && d != sealdomain.Runtime {
		return fmt.Errorf("domain %s is not wired in this release; only outbound, runtime and source migrate", d)
	}
	fleet, err := secretbox.LoadHostKey(*fleetKey)
	if err != nil {
		return fmt.Errorf("load fleet identity: %w", err)
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := db.MigrateUp(ctx, pool); err != nil {
		return fmt.Errorf("apply schema before seal-domain migration: %w", err)
	}
	store := state.NewPgStore(pool)
	var report sealmigrate.MigrationReport
	switch d {
	case sealdomain.Source:
		if !*readersVerified {
			// Before every githubd holds the source identity, a missing
			// copy goes to the fleet recipient githubd opens today.
			report, err = sealmigrate.SealGithubWebhookSecrets(ctx, store, []*age.X25519Recipient{fleet.Recipient()})
			break
		}
		report, err = migrateDomain(ctx, store, d, *dir, *fleetKey, *hostKey)
	case sealdomain.Outbound:
		if !*readersVerified {
			return errors.New("re-sealed outbound credentials no longer open with the fleet identity; pass --readers-verified once every outboundd verified the outbound domain and the previous release left the rollback window")
		}
		report, err = migrateDomain(ctx, store, d, *dir, *fleetKey, *hostKey)
	case sealdomain.Runtime:
		if !*readersVerified {
			return errors.New("re-sealed runtime secrets no longer open with the fleet identity; pass --readers-verified once every vmmd reports faas_seal_domain_loaded{domain=\"runtime\"} 1 and the previous release left the rollback window")
		}
		report, err = migrateDomain(ctx, store, d, *dir, *fleetKey, *hostKey)
	}
	if err != nil {
		return err
	}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(report)
	}
	PrintOK(osStdout, "Seal domain %s migrated: %d scanned, %d re-sealed, %d current, %d changed concurrently, %d failed\n",
		report.Domain, report.Scanned, report.Resealed, report.Current, report.Changed, report.Failed)
	if report.Failed > 0 {
		return fmt.Errorf("%d envelopes opened with no supplied identity", report.Failed)
	}
	return nil
}

// migrateDomain re-seals a wired domain's rows to its identity after
// checking that the identity is the one the domain's probe was sealed to.
func migrateDomain(ctx context.Context, store *state.PgStore, d sealdomain.Domain, dir, fleetKey, hostKey string) (sealmigrate.MigrationReport, error) {
	domain, err := secretbox.LoadHostKey(sealdomain.IdentityPath(dir, d))
	if err != nil {
		return sealmigrate.MigrationReport{}, fmt.Errorf("load %s domain identity: %w", d, err)
	}
	probe, err := store.LoadSealDomainProbe(ctx, string(d))
	if err != nil {
		return sealmigrate.MigrationReport{}, fmt.Errorf("load %s probe (run seal-domain verify --publish first): %w", d, err)
	}
	if probe.Recipient != domain.Recipient().String() {
		return sealmigrate.MigrationReport{}, fmt.Errorf("the %s probe is sealed to %s, not this identity's %s", d, probe.Recipient, domain.Recipient())
	}
	legacy, err := sealdomain.LoadReaderIdentities("", fleetKey, hostKey)
	if err != nil {
		return sealmigrate.MigrationReport{}, err
	}
	switch d {
	case sealdomain.Runtime:
		return sealmigrate.MigrateRuntime(ctx, store, domain, legacy)
	case sealdomain.Source:
		return sealmigrate.MigrateSource(ctx, store, domain, legacy)
	}
	return sealmigrate.MigrateOutbound(ctx, store, domain, legacy)
}

func writeSealDomainMetric(path string, d sealdomain.Domain, ready bool) error {
	if path == "" {
		return nil
	}
	value := 0
	if ready {
		value = 1
	}
	body := "# HELP faas_seal_domain_ready 1 when this node opened the seal domain's probe with its staged identity.\n" +
		"# TYPE faas_seal_domain_ready gauge\n" +
		"faas_seal_domain_ready{domain=\"" + string(d) + "\"} " + strconv.Itoa(value) + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".seal-domain-prom-*")
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
