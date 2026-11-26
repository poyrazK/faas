//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
	"github.com/onebox-faas/faas/pkg/pgarchive"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

func (g *databaseGuest) serveArchiveImports(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		g.receiveArchiveImport(conn)
	}
}

func (g *databaseGuest) reserveArchiveImport(request pgarchive.Request) (dbguest.Response, bool) {
	response := g.response()
	if response.State != dbguest.StateReady || !g.migrationMu.TryLock() {
		response.Error = dbguest.ErrCodeNotReady
		return response, false
	}
	defer g.migrationMu.Unlock()
	if g.migrationFence.Load() || g.migrationRunning.Load() {
		response.Error = dbguest.ErrCodeImportOperationMismatch
		return response, false
	}
	g.adoptRecordedImport()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.imp != nil && g.imp.status.State != dbguest.ImportFailed {
		status := g.imp.status
		response.Import = &status
		if status.OperationID != request.OperationID {
			response.Error = dbguest.ErrCodeImportOperationMismatch
		}
		return response, false
	}
	g.imp = &importJob{status: dbguest.ImportStatus{
		State: dbguest.ImportRunning, StartedAt: time.Now().UTC(), OperationID: request.OperationID, SourceMajor: request.Manifest.Major,
	}}
	return response, true
}

func (g *databaseGuest) receiveArchiveImport(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(databaseControlTimeout))
	reader := bufio.NewReaderSize(conn, pgarchive.ManifestLimit)
	encoder := json.NewEncoder(conn)
	var request pgarchive.Request
	if err := pgarchive.ReadLine(reader, &request); err != nil || !request.Valid() {
		_ = encoder.Encode(pgarchive.Reply{Result: dbguest.Response{Error: dbguest.ErrCodeInvalidArchive}})
		return
	}
	response, reserved := g.reserveArchiveImport(request)
	if !reserved {
		_ = encoder.Encode(pgarchive.Reply{Result: response})
		return
	}
	deadline := time.Now().Add(pgarchive.UploadTimeout)
	_ = conn.SetDeadline(deadline)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	vector, code, err := g.checkArchiveImport(ctx, request)
	if err == nil {
		err = encoder.Encode(pgarchive.Reply{Ready: true})
	}
	if err == nil {
		code, err = g.receiveArchivePayload(ctx, conn, reader, request, vector)
	}
	if err != nil {
		removeArchiveImportFiles()
		if code == "" {
			code = dbguest.ErrCodeImportFailed
		}
		g.finishImport(code, request.Manifest.Major, err)
		_ = encoder.Encode(pgarchive.Reply{Result: g.importResponse()})
		return
	}
	go g.applyArchiveImport(request, vector)
	_ = encoder.Encode(pgarchive.Reply{Result: g.importResponse()})
}

func (g *databaseGuest) checkArchiveImport(ctx context.Context, r pgarchive.Request) (bool, string, error) {
	if r.Manifest.Major > g.major {
		return false, dbguest.ErrCodeSourceNewer, errors.New("archive PostgreSQL major is newer than the target")
	}
	code, err := g.checkImportExtensions(ctx, r.TargetDatabase, r.Manifest.Extensions, nil)
	if err != nil {
		return false, code, err
	}
	vector := slices.ContainsFunc(r.Manifest.Extensions, func(extension pgextension.Installed) bool { return extension.Name == pgextension.VectorName })
	if vector && (r.Manifest.VectorSchema != "public" || !g.vectorLibraryAvailable()) {
		index := slices.IndexFunc(r.Manifest.Extensions, func(extension pgextension.Installed) bool { return extension.Name == pgextension.VectorName })
		return false, dbguest.ErrCodeExtension, &missingExtensionsError{extensions: []pgextension.Installed{r.Manifest.Extensions[index]}}
	}
	if err := g.checkArchiveTargetEmpty(ctx, r.TargetDatabase, vector); err != nil {
		return false, archiveApplyCode(err), err
	}
	total, free, err := importVolume()
	if err != nil {
		return false, dbguest.ErrCodeImportFailed, err
	}
	size := uint64(max(r.Manifest.DatabaseBytes, r.Manifest.SizeBytes)) // positive and bounded by Manifest.Valid
	if need := importSpaceNeeded(size, importSpaceFloor(total)); free < need {
		return false, dbguest.ErrCodeTargetTooSmall, &targetTooSmallError{source: size, free: free, requiredVolume: total - free + need}
	}
	return vector, "", nil
}

func (g *databaseGuest) checkArchiveTargetEmpty(ctx context.Context, target string, vector bool) error {
	if err := g.checkImportTargetEmpty(ctx, target, vector); err != nil {
		return err
	}
	used, err := g.queryIn(ctx, target, archivePublicObjectsSQL)
	if err != nil {
		return err
	}
	if used != "f" {
		return errImportTargetNotEmpty
	}
	return nil
}

func (g *databaseGuest) receiveArchivePayload(ctx context.Context, conn net.Conn, reader io.Reader, r pgarchive.Request, vector bool) (string, error) {
	total, _, err := importVolume()
	if err != nil {
		return dbguest.ErrCodeImportFailed, err
	}
	upload, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	interrupt := context.AfterFunc(upload, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer interrupt()
	go watchImportSpace(upload, stop, importSpaceFloor(total), time.Second, func() (uint64, error) {
		_, free, err := importVolume()
		return free, err
	})
	code, err := g.stageArchiveImport(upload, reader, r, vector)
	if errors.Is(context.Cause(upload), errImportVolumeFull) {
		return dbguest.ErrCodeTargetTooSmall, errImportVolumeFull
	}
	return code, err
}

func (g *databaseGuest) stageArchiveImport(ctx context.Context, reader io.Reader, r pgarchive.Request, vector bool) (string, error) {
	// An interrupted VM may leave yesterday's unpublished scratch files.
	removeArchiveImportFiles()
	file, err := os.OpenFile(databaseImportDump, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return dbguest.ErrCodeImportFailed, err
	}
	err = r.Manifest.Copy(file, io.LimitReader(reader, r.Manifest.SizeBytes))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = pgarchive.ConfirmUpload(reader)
	}
	if err != nil {
		return dbguest.ErrCodeInvalidArchive, err
	}
	if err := os.Chown(databaseImportDump, g.uid, g.gid); err != nil {
		return dbguest.ErrCodeImportFailed, err
	}
	list, err := g.readImportTOC(ctx)
	if err != nil {
		return dbguest.ErrCodeInvalidArchive, err
	}
	if !archiveTOCMatches(list, r.Manifest.Major, r.Manifest.Extensions) {
		return dbguest.ErrCodeInvalidArchive, pgarchive.ErrArchive
	}
	var skip []string
	if vector {
		skip = []string{pgextension.VectorName}
	}
	return dbguest.ErrCodeImportFailed, g.writeImportTOC(list, skip)
}

func (g *databaseGuest) applyArchiveImport(request pgarchive.Request, vector bool) {
	ctx, cancel := context.WithTimeout(context.Background(), databaseImportTimeout)
	defer cancel()
	target := dbguest.ImportSource{TargetDatabase: request.TargetDatabase, OperationID: request.OperationID, Owner: pgarchive.Owner}
	code, err := g.restoreStagedArchive(ctx, target, vector)
	removeArchiveImportFiles()
	g.finishImport(code, request.Manifest.Major, err)
}

func (g *databaseGuest) restoreStagedArchive(ctx context.Context, src dbguest.ImportSource, vector bool) (string, error) {
	// Drain the empty target before the final check. New app sessions
	// wait until the unprivileged restore transaction has settled.
	g.primarySessions.end()
	defer g.primarySessions.resume()
	drained, err := g.queryIn(ctx, "postgres", "SELECT coalesce(bool_and(pg_terminate_backend(pid, 3000)), true) "+
		"FROM pg_catalog.pg_stat_activity WHERE backend_type='client backend' AND datname="+dbmigration.Literal(src.TargetDatabase))
	if err != nil || drained != "t" {
		return dbguest.ErrCodeImportFailed, errors.New("target sessions did not drain")
	}
	if err := g.checkArchiveTargetEmpty(ctx, src.TargetDatabase, vector); err != nil {
		return archiveApplyCode(err), err
	}
	if err := g.prepareImportRole(ctx, src, vector); err != nil {
		return dbguest.ErrCodeImportFailed, err
	}
	total, _, err := importVolume()
	if err != nil {
		return dbguest.ErrCodeImportFailed, err
	}
	restore, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	go watchImportSpace(restore, stop, importSpaceFloor(total), time.Second, func() (uint64, error) {
		_, free, err := importVolume()
		return free, err
	})
	err = g.importCommitted(ctx, src, g.importApply(restore, src, nil))
	if err != nil && errors.Is(context.Cause(restore), errImportVolumeFull) {
		return dbguest.ErrCodeTargetTooSmall, err
	}
	if err == nil {
		_, _ = g.queryIn(ctx, src.TargetDatabase, "ANALYZE")
	}
	return archiveApplyCode(err), err
}

func archiveApplyCode(err error) string {
	if errors.Is(err, errImportTargetNotEmpty) {
		return dbguest.ErrCodeTargetNotEmpty
	}
	return dbguest.ErrCodeImportFailed
}

func removeArchiveImportFiles() {
	_ = os.Remove(databaseImportDump)
	_ = os.Remove(databaseImportList)
}
