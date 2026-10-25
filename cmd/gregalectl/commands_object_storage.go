package main

// ADR 20261025090000: after a control-plane database restore, the object
// bucket catalog can disagree with the providers. `object-storage reconcile`
// lists each backend's Gregale buckets and compares them with the catalog,
// read-only. It runs on a control-plane node, which holds the provider
// registry and credentials.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const dispatchObjectStorage = "object-storage"

const (
	defaultObjectStorageConfig      = "/etc/faas/object-storage.json"
	defaultObjectStorageProviderEnv = "/etc/faas/secrets/object-storage/provider.env"
	// exitObjectStorageFindings reports a reconcile that ran and found
	// disagreements; errors keep the usual exit code 3.
	exitObjectStorageFindings = 4
)

type objectStorageReconcileReport struct {
	Backends []objectStorageReconcileBackend  `json:"backends"`
	Findings []objectstorage.ReconcileFinding `json:"findings"`
}

type objectStorageReconcileBackend struct {
	ID       string `json:"id"`
	Physical int    `json:"physical_buckets"`
	Catalog  int    `json:"catalog_buckets"`
}

func cmdObjectStorage(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, "usage: gregalectl object-storage reconcile [flags]", dispatchObjectStorage)
		return 0
	}
	if args[0] != "reconcile" {
		fmt.Fprintf(os.Stderr, "gregalectl object-storage: unknown subcommand %q\n", args[0])
		return 2
	}
	findings, err := runObjectStorageReconcile(args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl object-storage reconcile: %v\n", err)
		return 3
	}
	if findings > 0 {
		return exitObjectStorageFindings
	}
	return 0
}

func runObjectStorageReconcile(args []string) (int, error) {
	fs := flag.NewFlagSet("object-storage reconcile", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	config := fs.String("config", defaultObjectStorageConfig, "object storage provider registry JSON")
	providerEnv := fs.String("provider-env", defaultObjectStorageProviderEnv, "provider credential env file (optional)")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	env, err := readObjectStorageProviderEnv(*providerEnv)
	if err != nil {
		return 0, err
	}
	getenv := func(name string) string {
		if name == "FAAS_OBJECT_STORAGE_CONFIG" {
			return *config
		}
		if value, ok := env[name]; ok {
			return value
		}
		return os.Getenv(name)
	}
	registry, err := objectstorage.Load(getenv)
	if err != nil {
		return 0, err
	}
	if registry == nil {
		return 0, errors.New("no object storage configuration")
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return 0, err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := reconcileObjectStorage(ctx, registry, state.NewPgStore(pool))
	if err != nil {
		return 0, err
	}
	if *jsonOut || jsonOutput {
		return len(report.Findings), json.NewEncoder(osStdout).Encode(report)
	}
	for _, b := range report.Backends {
		_, _ = fmt.Fprintf(osStdout, "backend %s: %d physical buckets, %d catalog buckets\n", b.ID, b.Physical, b.Catalog)
	}
	for _, f := range report.Findings {
		_, _ = fmt.Fprintf(osStdout, "%s  %s  %s  bucket=%s account=%s state=%s\n", f.Kind, f.BackendID, f.PhysicalName, f.BucketID, f.AccountID, f.State)
	}
	if len(report.Findings) == 0 {
		_, _ = fmt.Fprintln(osStdout, "object storage catalog agrees with every backend")
	}
	return len(report.Findings), nil
}

// reconcileObjectStorage compares every configured backend with the buckets
// bound to it in the catalog.
func reconcileObjectStorage(ctx context.Context, registry *objectstorage.Registry, store state.ObjectStorageProviderUsageStore) (objectStorageReconcileReport, error) {
	report := objectStorageReconcileReport{Backends: []objectStorageReconcileBackend{}, Findings: []objectstorage.ReconcileFinding{}}
	for _, backend := range registry.Backends() {
		lister, ok := backend.Provider.(objectstorage.BucketLister)
		if !ok {
			return report, fmt.Errorf("backend %s cannot list buckets", backend.ID)
		}
		physical, err := lister.ListBucketNames(ctx, objectstorage.PhysicalBucketPrefix)
		if err != nil {
			return report, fmt.Errorf("list buckets on backend %s: %w", backend.ID, err)
		}
		catalog, err := store.ListObjectStorageProviderBuckets(ctx, backend.ID, backend.Fingerprint)
		if err != nil {
			return report, fmt.Errorf("list catalog buckets for backend %s: %w", backend.ID, err)
		}
		report.Backends = append(report.Backends, objectStorageReconcileBackend{ID: backend.ID, Physical: len(physical), Catalog: len(catalog)})
		report.Findings = append(report.Findings, objectstorage.ReconcileBuckets(backend.ID, physical, catalog)...)
	}
	return report, nil
}

// readObjectStorageProviderEnv reads KEY=VALUE lines from the root-only
// provider env file the s3_gateway_service role installs. A missing file is
// fine: GCS with attached ADC has none.
func readObjectStorageProviderEnv(path string) (map[string]string, error) {
	out := map[string]string{}
	if path == "" {
		return out, nil
	}
	//nolint:forbidigo // operator-supplied, deploy-managed environment file.
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open provider env: %w", err)
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return out, scanner.Err()
}
