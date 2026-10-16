//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// databaseImportTimeout bounds one import, dump and restore together.
const databaseImportTimeout = 12 * time.Hour

// importJob is the guest's one import. Its state lives in memory, and a
// finished one is also recorded in the target (its lineage), so it outlives
// the VM. A VM that stops mid-import rolled the restore back, so the target
// is empty again and the import can simply start over.
type importJob struct {
	status dbguest.ImportStatus
}

// startImport begins copying src into this database in the background,
// under src.OperationID. A running or finished import under that ID is
// reported unchanged, a failed one starts again, and any other ID while one
// is running or done is refused. A copy finished before this VM booted is
// found in the target's recorded lineage.
func (g *databaseGuest) startImport(src *dbguest.ImportSource) dbguest.Response {
	resp := g.response()
	if g.currentState() != dbguest.StateReady {
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	g.mu.Lock()
	known := g.imp != nil
	g.mu.Unlock()
	if !known {
		g.adoptRecordedImport()
	}
	g.mu.Lock()
	if g.imp != nil && g.imp.status.State != dbguest.ImportFailed {
		status := g.imp.status
		g.mu.Unlock()
		resp.Import = &status
		if status.OperationID != src.OperationID {
			resp.Error = dbguest.ErrCodeImportOperationMismatch
		}
		return resp
	}
	g.imp = &importJob{status: dbguest.ImportStatus{State: dbguest.ImportRunning, StartedAt: time.Now().UTC(), OperationID: src.OperationID}}
	status := g.imp.status
	g.mu.Unlock()
	if err := g.serveImportBridge(); err != nil {
		g.finishImport(dbguest.ErrCodeImportFailed, 0, fmt.Errorf("serve the import bridge: %w", err))
		return g.importResponse()
	}
	go g.runImport(*src)
	resp.Import = &status
	return resp
}

// importResponse reports the import, if any: one this VM ran, or one a
// target records from before it booted.
func (g *databaseGuest) importResponse() dbguest.Response {
	resp := g.response()
	g.mu.Lock()
	known := g.imp != nil
	g.mu.Unlock()
	if !known && g.currentState() == dbguest.StateReady {
		g.adoptRecordedImport()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	status := dbguest.ImportStatus{State: dbguest.ImportNone}
	if g.imp != nil {
		status = g.imp.status
	}
	resp.Import = &status
	return resp
}

// adoptRecordedImport takes a finished import recorded in a database's
// comment (inside the restore transaction) as this VM's import, so its
// operation ID outlives the VM that ran it.
func (g *databaseGuest) adoptRecordedImport() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := g.queryIn(ctx, "postgres", importLineageQuery)
	if err != nil {
		g.log.Warn("database import: read recorded lineage", "err", err)
		return
	}
	recorded := recordedImports(out)
	if len(recorded) == 0 {
		return
	}
	// One customer database per VM, so one record at most.
	var id string
	for _, rid := range recorded {
		id = rid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.imp == nil {
		g.imp = &importJob{status: dbguest.ImportStatus{State: dbguest.ImportDone, OperationID: id}}
	}
}

func (g *databaseGuest) finishImport(code string, sourceMajor int, err error) {
	if err != nil {
		g.log.Warn("database import failed", "code", code, "err", err)
	} else {
		g.log.Info("database import finished", "source_major", sourceMajor)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.imp == nil {
		return
	}
	g.imp.status.FinishedAt = time.Now().UTC()
	g.imp.status.SourceMajor = sourceMajor
	g.imp.status.State, g.imp.status.Error, g.imp.status.MissingExtensions, g.imp.status.RequiredVolumeBytes = dbguest.ImportDone, "", nil, 0
	if err != nil {
		g.imp.status.State, g.imp.status.Error = dbguest.ImportFailed, code
		var missing *missingExtensionsError
		if errors.As(err, &missing) {
			g.imp.status.MissingExtensions = dbguest.ReportableExtensions(missing.names)
		}
		var small *targetTooSmallError
		if errors.As(err, &small) {
			g.imp.status.RequiredVolumeBytes = int64(min(small.requiredVolume, 1<<62)) //nolint:gosec // bounded
		}
	}
}

// targetTooSmallError is a source that does not fit on the target volume.
type targetTooSmallError struct{ source, free, requiredVolume uint64 }

func (e *targetTooSmallError) Error() string {
	return fmt.Sprintf("source of %d bytes needs a volume of %d bytes; %d bytes free", e.source, e.requiredVolume, e.free)
}

// importVolume reports the target volume's size and the space free to
// the database user.
func importVolume() (total, free uint64, err error) {
	var fs unix.Statfs_t
	if err := unix.Statfs(databaseMountPoint, &fs); err != nil {
		return 0, 0, err
	}
	return fs.Blocks * uint64(fs.Bsize), fs.Bavail * uint64(fs.Bsize), nil //nolint:gosec // block size is positive
}

// missingExtensionsError names the source extensions the owner cannot
// create.
type missingExtensionsError struct{ names []string }

func (e *missingExtensionsError) Error() string {
	return "extensions not available to the owner: " + strings.Join(e.names, ", ")
}

// runImport checks the source and target, dumps the source and restores
// it into the target in one transaction as its owner.
func (g *databaseGuest) runImport(src dbguest.ImportSource) {
	ctx, cancel := context.WithTimeout(context.Background(), databaseImportTimeout)
	defer cancel()
	defer func() {
		_ = os.Remove(databaseImportDump)
		_ = os.Remove(databaseImportList)
	}()
	code, major, err := g.importChecks(ctx, src)
	if err == nil {
		code, err = g.importWithinSpace(ctx, src)
	}
	if err == nil {
		// Fresh statistics: a restore leaves the planner without any.
		_, err = g.queryIn(ctx, src.TargetDatabase, "ANALYZE")
	}
	g.finishImport(code, major, err)
}

// importWithinSpace copies src unless the target volume cannot hold it,
// and stops the copy if free space falls to the floor anyway.
func (g *databaseGuest) importWithinSpace(ctx context.Context, src dbguest.ImportSource) (string, error) {
	total, free, err := importVolume()
	if err != nil {
		return dbguest.ErrCodeImportFailed, fmt.Errorf("target volume: %w", err)
	}
	out, err := g.sourceQuery(ctx, src, importSourceSizeQuery)
	if err != nil {
		return dbguest.ErrCodeSourceDown, err
	}
	size, err := strconv.ParseUint(out, 10, 64)
	if err != nil {
		return dbguest.ErrCodeImportFailed, fmt.Errorf("source size %q", out)
	}
	floor := importSpaceFloor(total)
	if need := importSpaceNeeded(size, floor); free < need {
		return dbguest.ErrCodeTargetTooSmall, &targetTooSmallError{source: size, free: free, requiredVolume: total - free + need}
	}
	copyCtx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	go watchImportSpace(copyCtx, stop, floor, time.Second, func() (uint64, error) {
		_, free, err := importVolume()
		return free, err
	})
	err = g.importCommitted(ctx, src, g.importCopy(copyCtx, src))
	if err != nil && errors.Is(context.Cause(copyCtx), errImportVolumeFull) {
		return dbguest.ErrCodeTargetTooSmall, errors.Join(errImportVolumeFull, err)
	}
	return dbguest.ErrCodeImportFailed, err
}

// importCommitted decides from the target's recorded lineage whether the
// restore committed, whatever the tools reported: a restore committed
// under src.OperationID is done even if a tool failed after its COMMIT, and
// one that did not commit failed even if no tool said so.
func (g *databaseGuest) importCommitted(ctx context.Context, src dbguest.ImportSource, copyErr error) error {
	id, err := g.recordedImport(ctx, src.TargetDatabase)
	switch {
	case err != nil:
		return errors.Join(copyErr, fmt.Errorf("read recorded lineage: %w", err))
	case id == src.OperationID:
		if copyErr != nil {
			g.log.Warn("database import: committed despite a tool error", "err", copyErr)
		}
		return nil
	case copyErr != nil:
		return copyErr
	default:
		return errors.New("restore did not commit")
	}
}

// importChecks refuses a source newer than these binaries, extensions the
// target cannot create as a non-superuser, and a target that is not empty.
func (g *databaseGuest) importChecks(ctx context.Context, src dbguest.ImportSource) (string, int, error) {
	out, err := g.sourceQuery(ctx, src, importSourceMajorQuery)
	if err != nil {
		return dbguest.ErrCodeSourceDown, 0, err
	}
	major, err := strconv.Atoi(out)
	if err != nil {
		return dbguest.ErrCodeImportFailed, 0, fmt.Errorf("source major %q", out)
	}
	if major > g.major {
		return dbguest.ErrCodeSourceNewer, major, fmt.Errorf("source PostgreSQL %d, binaries %d", major, g.major)
	}
	extensions, err := g.sourceQuery(ctx, src, importExtensionsQuery)
	if err != nil {
		return dbguest.ErrCodeSourceDown, major, err
	}
	trusted, err := g.queryIn(ctx, src.TargetDatabase, importTrustedQuery)
	if err != nil {
		return dbguest.ErrCodeImportFailed, major, err
	}
	if missing := missingExtensions(extensions, trusted, src.SkipExtensions); len(missing) > 0 {
		return dbguest.ErrCodeExtension, major, &missingExtensionsError{names: missing}
	}
	used, err := g.queryIn(ctx, src.TargetDatabase, importTargetUsedQuery)
	if err != nil {
		return dbguest.ErrCodeImportFailed, major, err
	}
	if used != "0" {
		return dbguest.ErrCodeTargetNotEmpty, major, fmt.Errorf("target database holds %s user objects", used)
	}
	return "", major, nil
}

// importCopy dumps the source and restores the filtered dump.
func (g *databaseGuest) importCopy(ctx context.Context, src dbguest.ImportSource) error {
	dump := importDumpArgs(g.major, importConnString(src))
	if err := g.importTool(ctx, src, dump); err != nil {
		return fmt.Errorf("pg_dump: %w", err)
	}
	listArgs := []string{databaseBinDir(g.major) + "/pg_restore", "--list", databaseImportDump}
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, listArgs[0], listArgs[1:]...))
	list, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("pg_restore --list: %w", err)
	}
	if err := os.WriteFile(databaseImportList, []byte(filterImportTOC(string(list), src.SkipExtensions)), 0o600); err != nil {
		return err
	}
	if err := os.Chown(databaseImportList, g.uid, g.gid); err != nil {
		return err
	}
	if _, err := g.queryIn(ctx, "postgres", importRoleStatement(src.Owner)); err != nil {
		return fmt.Errorf("import role: %w", err)
	}
	if err := g.importApply(ctx, src); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	return nil
}

// importApply pipes pg_restore's script into psql. Either one failing
// fails the import; whether the copy committed is the target's recorded
// lineage, which runImport reads afterwards.
func (g *databaseGuest) importApply(ctx context.Context, src dbguest.ImportSource) error {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	script := importScriptArgs(g.major, hex.EncodeToString(key))
	restore := g.asDatabaseUser(exec.CommandContext(ctx, script[0], script[1:]...))
	var restoreErr bytes.Buffer
	restore.Stderr = &limitedWriter{w: &restoreErr, n: 4096}
	out, err := restore.StdoutPipe()
	if err != nil {
		return err
	}
	if err := restore.Start(); err != nil {
		return fmt.Errorf("pg_restore: %w", err)
	}
	apply := importApplyArgs(g.major, src.TargetDatabase)
	psql := g.asDatabaseUser(exec.CommandContext(ctx, apply[0], apply[1:]...))
	psql.Stdin = io.MultiReader(strings.NewReader(importScriptPrologue(src.TargetDatabase, src.Owner, src.OperationID)), out)
	psql.Stdout = io.Discard
	var psqlErr bytes.Buffer
	psql.Stderr = &limitedWriter{w: &psqlErr, n: 4096}
	applyErr := psql.Run()
	// psql read to the end or stopped at an error: either way pg_restore
	// has nothing left to write to.
	_ = out.Close()
	restoreWait := restore.Wait()
	if applyErr != nil {
		return fmt.Errorf("psql: %w: %s", applyErr, strings.TrimSpace(psqlErr.String()))
	}
	if restoreWait != nil {
		return fmt.Errorf("pg_restore: %w: %s", restoreWait, strings.TrimSpace(restoreErr.String()))
	}
	return nil
}

// recordedImport reads the operation ID target records, if any.
func (g *databaseGuest) recordedImport(ctx context.Context, target string) (string, error) {
	out, err := g.queryIn(ctx, "postgres", importLineageQuery)
	if err != nil {
		return "", err
	}
	return recordedImports(out)[target], nil
}

// importTool runs a dump or restore tool as the database user, with the
// source password in its environment only.
func (g *databaseGuest) importTool(ctx context.Context, src dbguest.ImportSource, args []string) error {
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, args[0], args[1:]...))
	cmd.Env = append(cmd.Env, "PGPASSWORD="+src.Password)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Run(); err != nil {
		// pg_dump and pg_restore never print the password.
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// sourceQuery runs one query on the source and returns its single value.
func (g *databaseGuest) sourceQuery(ctx context.Context, src dbguest.ImportSource, sql string) (string, error) {
	args := []string{databaseBinDir(g.major) + "/psql", "-X", "-q", "-tA", "-v", "ON_ERROR_STOP=1", "-d", importConnString(src), "-c", sql}
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, args[0], args[1:]...))
	cmd.Env = append(cmd.Env, "PGPASSWORD="+src.Password)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// queryIn runs one statement as the superuser in database and returns its
// single value.
func (g *databaseGuest) queryIn(ctx context.Context, database, sql string) (string, error) {
	args := databasePsqlArgs(g.major)
	args = append(args[:len(args)-2], "-tA", "-c", sql)
	for i, arg := range args {
		if arg == "-d" {
			args[i+1] = database
		}
	}
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, args[0], args[1:]...))
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// serveImportBridge serves the socket the import tools connect to, once:
// each connection goes to the host's import relay.
func (g *databaseGuest) serveImportBridge() error {
	g.importOnce.Do(func() {
		g.importErr = g.listenImportBridge()
	})
	return g.importErr
}

func (g *databaseGuest) listenImportBridge() error {
	if err := os.MkdirAll(databaseImportSocketDir, 0o700); err != nil {
		return err
	}
	if err := os.Chown(databaseImportSocketDir, g.uid, g.gid); err != nil {
		return err
	}
	path := filepath.Join(databaseImportSocketDir, ".s.PGSQL.5432")
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chown(path, g.uid, g.gid); err != nil {
		_ = ln.Close()
		return err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go relayToImportSource(conn)
		}
	}()
	return nil
}

func relayToImportSource(local net.Conn) {
	defer func() { _ = local.Close() }()
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: dbguest.ImportPort}); err != nil {
		_ = unix.Close(fd)
		return
	}
	host := &runtimeConfigVsockConn{
		File:   os.NewFile(uintptr(fd), "import-vsock"),
		local:  runtimeConfigVsockAddr{unix.VMADDR_CID_ANY, 0},
		remote: runtimeConfigVsockAddr{unix.VMADDR_CID_HOST, dbguest.ImportPort},
	}
	defer func() { _ = host.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(host, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, host); done <- struct{}{} }()
	<-done
}
