package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresExport(args []string) int {
	fs := newFlagSet("postgres export", 0)
	output := fs.String("output", "", "path for the downloaded PostgreSQL dump")
	requestID := fs.String("request-id", "", "stable request UUID for retries")
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *output == "" || *requestID != "" && !validCustomerOperationUUID(*requestID) {
		PrintUsage(os.Stderr, "usage: gregale postgres export DATABASE --output FILE [--request-id UUID]", "postgres")
		return 1
	}
	for _, path := range []string{*output, *output + ".manifest.json"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				err = os.ErrExist
			}
			return printErr("Could not create "+path, err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	database, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	id := *requestID
	if id == "" {
		id = uuid.NewString()
	}
	org := client.ProjectOrganization()
	job, err := client.StartManagedPostgresExport(ctx, org, database, api.ManagedPostgresExportRequest{RequestID: id})
	if err != nil {
		return printErr("Could not start PostgreSQL export "+id, err)
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(os.Stderr, "PostgreSQL export %s\n", id)
	}
	for job.State == "queued" || job.State == "running" {
		select {
		case <-ctx.Done():
			return printErr("Export "+id+" is still pending. Resume using --request-id", ctx.Err())
		case <-time.After(2 * time.Second):
		}
		job, err = client.GetManagedPostgresExport(ctx, org, database, id)
		if err != nil {
			return printErr("Could not read PostgreSQL export "+id, err)
		}
	}
	if job.State != "ready" || job.Manifest == nil || job.ID != id {
		return printErr("PostgreSQL export "+id+" did not complete", fmt.Errorf("state %q", job.State))
	}
	if err := savePostgresExport(*output, *job.Manifest, func(dst io.Writer) error {
		return client.DownloadManagedPostgresExport(ctx, org, database, id, job.Manifest.SizeBytes, dst)
	}); err != nil {
		return printErr("Could not save PostgreSQL export "+id, err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(job))
	}
	_, _ = fmt.Fprintf(osStdout, "Saved %s and %s.manifest.json\n", *output, *output)
	return 0
}

// Link complete, verified temporary files into place without overwriting
// an existing dump or manifest, even if another process creates one.
func savePostgresExport(path string, manifest api.ManagedPostgresExportManifest, download func(io.Writer) error) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".gregale-postgres-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name()) //nolint:errcheck
	hash := sha256.New()
	err = download(io.MultiWriter(file, hash))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != manifest.SHA256 {
		return errors.New("export checksum does not match the manifest")
	}
	meta, err := os.CreateTemp(filepath.Dir(path), ".gregale-postgres-manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(meta.Name()) //nolint:errcheck
	encoder := json.NewEncoder(meta)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(manifest)
	if closeErr := meta.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	sidecar := path + ".manifest.json"
	if err := os.Link(meta.Name(), sidecar); err != nil {
		return err
	}
	if err := os.Link(file.Name(), path); err != nil {
		_ = os.Remove(sidecar)
		return err
	}
	return nil
}
