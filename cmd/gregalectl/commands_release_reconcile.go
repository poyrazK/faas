package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

type releaseReconcileReport struct {
	Scanned   int      `json:"scanned"`
	Eligible  []string `json:"eligible"`
	Removed   []string `json:"removed"`
	Protected []string `json:"protected,omitempty"`
	DryRun    bool     `json:"dry_run"`
	Cutoff    string   `json:"cutoff"`
}

func cmdReleaseReconcile(args []string) int {
	if len(args) > 0 && (args[0] == flagHelpLong || args[0] == flagHelpShort) {
		PrintUsage(os.Stderr, "usage: gregalectl release reconcile [--retention 24h] [--releases-root PATH] [--dry-run]", "release")
		return 0
	}
	fs := flag.NewFlagSet("release reconcile", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	releasesRoot := fs.String("releases-root", "/opt/faas/releases", "releases root directory")
	retention := fs.Duration("retention", 24*time.Hour, "minimum age before an unapplied row is abandoned")
	dryRun := fs.Bool("dry-run", false, "report eligible rows without deleting them")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *retention <= 0 {
		_, _ = fmt.Fprintln(os.Stderr, "gregalectl release reconcile: --retention must be positive and positional arguments are not accepted")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := openPgPoolFromEnv(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release reconcile: %v\n", err)
		return 3
	}
	defer pool.Close()
	store := releaseinstall.NewStore(pool)
	rows, err := store.ListAllBundles(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release reconcile: list bundles: %v\n", err)
		return 3
	}
	cutoff := time.Now().UTC().Add(-*retention)
	eligible, err := abandonedBundleCandidates(rows, *releasesRoot, cutoff)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release reconcile: inspect bundles: %v\n", err)
		return 3
	}
	report := releaseReconcileReport{Scanned: len(rows), DryRun: *dryRun, Cutoff: cutoff.Format(time.RFC3339)}
	for _, row := range eligible {
		report.Eligible = append(report.Eligible, row.GitSHA)
		if *dryRun {
			continue
		}
		deleted, err := store.DeleteAbandonedBundle(ctx, row.GitSHA, cutoff)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gregalectl release reconcile: delete %s: %v\n", row.GitSHA, err)
			return 3
		}
		if deleted {
			report.Removed = append(report.Removed, row.GitSHA)
		} else {
			report.Protected = append(report.Protected, row.GitSHA)
		}
	}
	if jsonEnabled() {
		jsonEmit(os.Stdout, report)
	} else if *dryRun {
		_, _ = fmt.Fprintf(os.Stdout, "release reconcile: %d eligible of %d scanned (dry run)\n", len(report.Eligible), report.Scanned)
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "release reconcile: removed %d abandoned row(s), protected %d, scanned %d\n", len(report.Removed), len(report.Protected), report.Scanned)
	}
	return 0
}

func abandonedBundleCandidates(rows []releaseinstall.BundleRow, releasesRoot string, cutoff time.Time) ([]releaseinstall.BundleRow, error) {
	var eligible []releaseinstall.BundleRow
	for _, row := range rows {
		if row.AppliedAt != nil || !row.CreatedAt.Before(cutoff) {
			continue
		}
		hasNewerApplied := false
		for _, newer := range rows {
			if newer.AppliedAt != nil && newer.CreatedAt.After(row.CreatedAt) {
				hasNewerApplied = true
				break
			}
		}
		if !hasNewerApplied {
			continue
		}
		onDisk, err := releaseinstall.IsBundleOnDisk(releasesRoot, row.GitSHA)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", row.GitSHA, err)
		}
		if !onDisk {
			eligible = append(eligible, row)
		}
	}
	return eligible, nil
}
