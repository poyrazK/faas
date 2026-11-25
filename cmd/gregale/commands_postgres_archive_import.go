package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/pgarchive"
)

func importPostgresArchive(database, path, operation string) int {
	file, manifest, err := openPostgresArchive(path)
	if err != nil {
		return printErr("Could not read PostgreSQL backup", err)
	}
	defer file.Close() //nolint:errcheck
	if err := manifest.ArchiveManifest().Copy(io.Discard, file); err != nil {
		return printErr("PostgreSQL backup does not match its manifest", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return printErr("Could not rewind PostgreSQL backup", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), pgarchive.UploadTimeout+time.Minute)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, database)
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	if operation == "" {
		operation = uuid.NewString()
	} else {
		operation = uuid.MustParse(operation).String()
	}
	// Preserve the request ID for a retry if the upload response is lost.
	if !jsonOutput {
		_, _ = fmt.Fprintf(os.Stderr, "PostgreSQL import %s\n", operation)
	}
	progress, err := client.UploadManagedPostgresArchive(ctx, client.ProjectOrganization(), id, operation, manifest, file)
	if err != nil {
		return printErr("Could not upload PostgreSQL backup "+operation, err)
	}
	return printPostgresImport(progress)
}

func openPostgresArchive(path string) (*os.File, api.ManagedPostgresExportManifest, error) {
	var manifest api.ManagedPostgresExportManifest
	meta, err := os.Open(path + ".manifest.json")
	if err != nil {
		return nil, manifest, err
	}
	defer meta.Close() //nolint:errcheck
	raw, err := io.ReadAll(io.LimitReader(meta, pgarchive.ManifestLimit+1))
	if err != nil {
		return nil, manifest, err
	}
	if len(raw) > pgarchive.ManifestLimit || json.Unmarshal(raw, &manifest) != nil || !manifest.ArchiveManifest().Valid() {
		return nil, manifest, pgarchive.ErrArchive
	}
	if manifest.RestoreReceipt == "" {
		return nil, manifest, errors.New("the companion manifest must include a Gregale restore receipt")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, manifest, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != manifest.SizeBytes {
		_ = file.Close()
		return nil, manifest, errors.Join(pgarchive.ErrArchive, err)
	}
	return file, manifest, nil
}
