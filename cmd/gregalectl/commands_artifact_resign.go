package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/signd"
	"github.com/onebox-faas/faas/pkg/signd/signpg"
)

// cmdArtifactResign is the re-sign pass of ADR-20261109170000 §5 step 3. It
// runs once on the control plane, with signd's key, before sign.key leaves the
// compute nodes: every stored layer's valid v1 signature becomes a v2 one
// bound to its key, and app layers get their artifact_signatures row. It is
// idempotent; --dry-run reads and verifies but writes nothing.
func cmdArtifactResign(args []string) int {
	const op = "gregalectl artifact resign"
	fs := flag.NewFlagSet("artifact resign", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	envFile := fs.String("env-file", "", "storage.env of the artifact store (required)")
	pgDSN := fs.String("pg-dsn", "", "PostgreSQL DSN (default: DATABASE_URL from --db-env)")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	signKey := fs.String("sign-key", cosign.DefaultSignKeyPath, "the fleet signing key signd holds")
	dryRun := fs.Bool("dry-run", false, "verify and count, write nothing")
	asJSON := fs.Bool("json", false, "emit a machine-readable report")
	if err := fs.Parse(args); err != nil {
		return printErr(op, err)
	}
	if *envFile == "" {
		return printErr(op, fmt.Errorf("--env-file is required"))
	}
	key, err := cosign.LoadPrivateKeyFile(*signKey)
	if err != nil {
		return printErr(op, err)
	}
	cleanup, err := loadStorageEnv(*envFile)
	if err != nil {
		return printErr(op, err)
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	objects, err := artifactBackendFromEnv(ctx)
	if err != nil {
		return printErr(op, err)
	}
	pool, err := openFleetSealPool(*pgDSN, *dbEnv)
	if err != nil {
		return printErr(op, err)
	}
	defer pool.Close()
	report, err := signd.Resign(ctx, signpg.New(pool), objects, key, *dryRun, nil)
	if err != nil {
		return printErr(op, err)
	}
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return printErr(op, err)
		}
	} else {
		printResignReport(os.Stdout, report)
	}
	if len(report.Problems) > 0 {
		return 2
	}
	return 0
}

func printResignReport(w io.Writer, r signd.ResignReport) {
	verb := "converted"
	if r.DryRun {
		verb = "would convert"
	}
	_, _ = fmt.Fprintf(w, "%s %d v1 signatures; %d already v2; %d rows recorded\n", verb, r.Converted, r.Current, r.Recorded)
	for _, p := range r.Problems {
		_, _ = fmt.Fprintf(w, "left alone: %s (deployment %s): %s\n", p.StorageKey, p.DeploymentID, p.Reason)
	}
}
