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
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgextension"
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
	known := g.imp != nil && g.imp.status.State != dbguest.ImportFailed
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
	if g.imp == nil || g.imp.status.State == dbguest.ImportFailed {
		g.imp = &importJob{status: dbguest.ImportStatus{State: dbguest.ImportDone, OperationID: id}}
	}
}

func (g *databaseGuest) finishImport(code string, sourceMajor int, err error) {
	if err != nil {
		g.log.Warn("database import failed", "code", code)
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
	g.imp.status.IncompatibleExtensions = nil
	if err != nil {
		applyImportFailure(&g.imp.status, code, err)
	}
}

func applyImportFailure(status *dbguest.ImportStatus, code string, err error) {
	status.State, status.Error = dbguest.ImportFailed, code
	var missing *missingExtensionsError
	if errors.As(err, &missing) {
		names := make([]string, 0, len(missing.extensions))
		for _, extension := range missing.extensions {
			names = append(names, extension.Name)
		}
		status.MissingExtensions = dbguest.ReportableExtensions(names)
		status.IncompatibleExtensions = missing.extensions
	}
	var small *targetTooSmallError
	if errors.As(err, &small) {
		status.RequiredVolumeBytes = int64(min(small.requiredVolume, 1<<62)) //nolint:gosec // bounded
	}
}

// checkImport uses the same checks as runImport and holds no import job.
func (g *databaseGuest) checkImport(src dbguest.ImportSource) dbguest.Response {
	resp := g.response()
	if resp.State != dbguest.StateReady {
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	if err := g.serveImportBridge(); err != nil {
		resp.Import = &dbguest.ImportStatus{State: dbguest.ImportFailed, Error: dbguest.ErrCodeImportFailed}
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	code, plan, err := g.importChecks(ctx, src)
	if err == nil {
		_, code, err = g.checkImportSpace(ctx, src)
	}
	result := dbguest.ImportStatus{State: dbguest.ImportChecked, SourceMajor: plan.major}
	if err != nil {
		applyImportFailure(&result, code, err)
	}
	resp.Import = &result
	return resp
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
type missingExtensionsError struct{ extensions []pgextension.Installed }

func (e *missingExtensionsError) Error() string {
	return "required extension versions are unavailable to the owner"
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
	code, plan, err := g.importChecks(ctx, src)
	if err == nil {
		code, err = g.importWithinSpace(ctx, src, plan.vector)
	}
	if err == nil {
		// Fresh statistics: a restore leaves the planner without any.
		if _, analyzeErr := g.queryIn(ctx, src.TargetDatabase, "ANALYZE"); analyzeErr != nil {
			g.log.Warn("database import: analyze failed after copy committed")
		}
	}
	g.finishImport(code, plan.major, err)
}

// importWithinSpace copies src unless the target volume cannot hold it,
// and stops the copy if free space falls to the floor anyway.
func (g *databaseGuest) importWithinSpace(ctx context.Context, src dbguest.ImportSource, vector bool) (string, error) {
	floor, code, err := g.checkImportSpace(ctx, src)
	if err != nil {
		return code, err
	}
	copyCtx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	go watchImportSpace(copyCtx, stop, floor, time.Second, func() (uint64, error) {
		_, free, err := importVolume()
		return free, err
	})
	err = g.importCommitted(ctx, src, g.importCopy(copyCtx, src, vector))
	if errors.Is(err, errImportTargetNotEmpty) {
		return dbguest.ErrCodeTargetNotEmpty, err
	}
	if err != nil && errors.Is(context.Cause(copyCtx), errImportVolumeFull) {
		return dbguest.ErrCodeTargetTooSmall, errors.Join(errImportVolumeFull, err)
	}
	return dbguest.ErrCodeImportFailed, err
}

// checkImportSpace estimates the free space needed for the dump, restore and WAL.
func (g *databaseGuest) checkImportSpace(ctx context.Context, src dbguest.ImportSource) (uint64, string, error) {
	total, free, err := importVolume()
	if err != nil {
		return 0, dbguest.ErrCodeImportFailed, fmt.Errorf("target volume: %w", err)
	}
	out, err := g.sourceQuery(ctx, src, importSourceSizeQuery)
	if err != nil {
		return 0, dbguest.ErrCodeSourceDown, err
	}
	size, err := strconv.ParseUint(out, 10, 64)
	if err != nil {
		return 0, dbguest.ErrCodeImportFailed, err
	}
	floor := importSpaceFloor(total)
	if need := importSpaceNeeded(size, floor); free < need {
		return 0, dbguest.ErrCodeTargetTooSmall, &targetTooSmallError{source: size, free: free, requiredVolume: total - free + need}
	}
	return floor, "", nil
}

// importCommitted decides from the target's recorded lineage whether the
// restore committed, whatever the tools reported: a restore committed
// under src.OperationID is done even if a tool failed after its COMMIT, and
// one that did not commit failed even if no tool said so.
func (g *databaseGuest) importCommitted(ctx context.Context, src dbguest.ImportSource, copyErr error) error {
	verify, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	id, err := g.recordedImport(verify, src.TargetDatabase)
	switch {
	case err != nil:
		return errors.Join(copyErr, fmt.Errorf("read recorded lineage: %w", err))
	case id == src.OperationID:
		if copyErr != nil {
			g.log.Warn("database import: committed despite a tool error")
		}
		return nil
	case copyErr != nil:
		return copyErr
	default:
		return errors.New("restore did not commit")
	}
}

type importPlan struct {
	major  int
	vector bool
}

// importChecks validates a source without changing the target database.
func (g *databaseGuest) importChecks(ctx context.Context, src dbguest.ImportSource) (string, importPlan, error) {
	var plan importPlan
	out, err := g.sourceQuery(ctx, src, importSourceMajorQuery)
	if err != nil {
		return dbguest.ErrCodeSourceDown, plan, err
	}
	major, err := strconv.Atoi(out)
	if err != nil {
		return dbguest.ErrCodeImportFailed, plan, fmt.Errorf("source major %q", out)
	}
	plan.major = major
	if major > g.major {
		return dbguest.ErrCodeSourceNewer, plan, fmt.Errorf("source PostgreSQL %d, binaries %d", major, g.major)
	}
	extensions, err := g.sourceQuery(ctx, src, pgextension.InstalledSQL)
	if err != nil {
		return dbguest.ErrCodeSourceDown, plan, err
	}
	sourceVersions, err := pgextension.DecodeInstalled(extensions)
	if err != nil {
		return dbguest.ErrCodeExtension, plan, err
	}
	if code, err := g.checkImportExtensions(ctx, src.TargetDatabase, sourceVersions, src.SkipExtensions); err != nil {
		return code, plan, err
	}
	if !slices.Contains(src.SkipExtensions, pgextension.VectorName) {
		for _, extension := range sourceVersions {
			if extension.Name != pgextension.VectorName {
				continue
			}
			schema, err := g.sourceQuery(ctx, src, importVectorSchemaQuery)
			if err != nil {
				return dbguest.ErrCodeSourceDown, plan, err
			}
			if schema != "public" || !g.vectorLibraryAvailable() {
				return dbguest.ErrCodeExtension, plan, &missingExtensionsError{extensions: []pgextension.Installed{extension}}
			}
			plan.vector = true
		}
	}
	if err := g.checkImportTargetEmpty(ctx, src.TargetDatabase, plan.vector); err != nil {
		if errors.Is(err, errImportTargetNotEmpty) {
			return dbguest.ErrCodeTargetNotEmpty, plan, err
		}
		return dbguest.ErrCodeImportFailed, plan, err
	}
	return "", plan, nil
}

// checkImportExtensions is shared by reachable and archived sources.
func (g *databaseGuest) checkImportExtensions(ctx context.Context, target string, sourceVersions []pgextension.Installed, skip []string) (string, error) {
	query, err := pgextension.ImportCompatibleSQL(sourceVersions, skip)
	if err != nil {
		return dbguest.ErrCodeExtension, err
	}
	compatible, err := g.queryIn(ctx, target, query)
	if err != nil {
		return dbguest.ErrCodeImportFailed, err
	}
	targetVersions, err := pgextension.DecodeInstalled(compatible)
	if err != nil {
		return dbguest.ErrCodeImportFailed, err
	}
	if missing := pgextension.ImportBlockers(sourceVersions, targetVersions, skip); len(missing) > 0 {
		return dbguest.ErrCodeExtension, &missingExtensionsError{extensions: missing}
	}
	return "", nil
}

var errImportTargetNotEmpty = errors.New("import target contains user objects")

func (g *databaseGuest) checkImportTargetEmpty(ctx context.Context, target string, vector bool) error {
	used, err := g.queryIn(ctx, target, importTargetUsedSQL(vector))
	if err == nil && used != "0" {
		return errImportTargetNotEmpty
	}
	return err
}

// importCopy dumps the source and restores the filtered dump.
func (g *databaseGuest) importCopy(ctx context.Context, src dbguest.ImportSource, vector bool) error {
	dump := importDumpArgs(g.major, importConnString(src))
	if err := g.importTool(ctx, src, dump); err != nil {
		return fmt.Errorf("pg_dump: %w", err)
	}
	list, err := g.asDatabaseUser(exec.CommandContext(ctx, databaseBinDir(g.major)+"/pg_restore", "--list", databaseImportDump)).Output()
	if err != nil {
		return fmt.Errorf("pg_restore --list: %w", err)
	}
	skip := src.SkipExtensions
	if vector {
		skip = append(slices.Clone(skip), pgextension.VectorName)
	}
	if err := g.writeImportTOC(string(list), skip); err != nil {
		return err
	}
	out, err := g.sourceRecords(ctx, src, importPublicACLQuery, maxPublicACLBytes)
	if err != nil {
		return fmt.Errorf("source privileges: %w", err)
	}
	publicACL, err := publicACLStatements(out)
	if err != nil {
		return err
	}
	// The dump can take long enough for another client to use the target.
	if err := g.checkImportTargetEmpty(ctx, src.TargetDatabase, vector); err != nil {
		return err
	}
	if err := g.prepareImportRole(ctx, src, vector); err != nil {
		return err
	}
	if err := g.importApply(ctx, src, publicACL); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	return nil
}

func (g *databaseGuest) readImportTOC(ctx context.Context) (string, error) {
	const maxTOCBytes = 16 << 20
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, databaseBinDir(g.major)+"/pg_restore", "--list", databaseImportDump))
	var list bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &list, n: maxTOCBytes + 1}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pg_restore --list: %w", err)
	}
	if list.Len() > maxTOCBytes {
		return "", errors.New("archive object inventory is too large")
	}
	return list.String(), nil
}

func (g *databaseGuest) writeImportTOC(list string, skip []string) error {
	if err := os.WriteFile(databaseImportList, []byte(filterImportTOC(list, skip)), 0o600); err != nil {
		return err
	}
	return os.Chown(databaseImportList, g.uid, g.gid)
}

func (g *databaseGuest) prepareImportRole(ctx context.Context, src dbguest.ImportSource, vector bool) error {
	if _, err := g.queryIn(ctx, "postgres", importRoleStatement(src.Owner)); err != nil {
		return fmt.Errorf("import role: %w", err)
	}
	if vector {
		// Install only fixed provider SQL through the superuser connection.
		// pg_restore still connects as the unprivileged import role.
		sql, _ := pgextension.EnableSQL(pgextension.VectorName)
		if _, err := g.queryIn(ctx, src.TargetDatabase, sql); err != nil {
			return fmt.Errorf("install managed vector: %w", err)
		}
	}
	return nil
}

// importApply pipes pg_restore's script through gateCommit into psql.
// Either one failing fails the import; whether the copy committed is the
// target's recorded lineage, which runImport reads afterwards.
func (g *databaseGuest) importApply(ctx context.Context, src dbguest.ImportSource, publicACL []string) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	key := hex.EncodeToString(raw)
	script := importScriptArgs(g.major, key)
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
	gated, gate := io.Pipe()
	gateErr := make(chan error, 1)
	go func() {
		err := gateCommit(gate, out, key, importScriptEpilogue(src.TargetDatabase, src.OperationID, publicACL))
		gateErr <- err
		// psql reads to the end either way; a script cut short ends
		// without its COMMIT, so the transaction rolls back.
		_ = gate.Close()
	}()
	apply := importApplyArgs(g.major, src.TargetDatabase)
	psql := g.asDatabaseUser(exec.CommandContext(ctx, apply[0], apply[1:]...))
	psql.Stdin = io.MultiReader(strings.NewReader(importScriptPrologue(src.Owner)), gated)
	psql.Stdout = io.Discard
	var psqlErr bytes.Buffer
	psql.Stderr = &limitedWriter{w: &psqlErr, n: 4096}
	applyErr := psql.Run()
	// psql read to the end or stopped at an error: either way nothing is
	// left to write to, and the gate and pg_restore stop.
	_ = gated.Close()
	_ = out.Close()
	restoreWait := restore.Wait()
	scriptErr := <-gateErr
	if applyErr != nil {
		return fmt.Errorf("psql: %w: %s", applyErr, strings.TrimSpace(psqlErr.String()))
	}
	if restoreWait != nil {
		return fmt.Errorf("pg_restore: %w: %s", restoreWait, strings.TrimSpace(restoreErr.String()))
	}
	if scriptErr != nil {
		return scriptErr
	}
	return nil
}

// sourceRecords runs one query on the source and returns its rows,
// NUL-separated, refusing more than limit bytes.
func (g *databaseGuest) sourceRecords(ctx context.Context, src dbguest.ImportSource, sql string, limit int64) ([]byte, error) {
	return g.sourceRecordsDSN(ctx, src, importConnString(src), sql, limit)
}

func (g *databaseGuest) sourceRecordsDSN(ctx context.Context, src dbguest.ImportSource, dsn, sql string, limit int64) ([]byte, error) {
	args := []string{databaseBinDir(g.major) + "/psql", "-X", "-q", "-tA", "-0", "-v", "ON_ERROR_STOP=1", "-d", dsn, "-c", sql}
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, args[0], args[1:]...))
	cmd.Env = append(cmd.Env, "PGPASSWORD="+src.Password)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	if int64(len(out)) > limit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("source returned more than %d bytes", limit)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, readErr
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
	out, err := g.sourceRecords(ctx, src, sql, 64<<10)
	return strings.TrimSpace(strings.ReplaceAll(string(out), "\x00", "\n")), err
}

// queryIn runs SQL as the superuser in database and returns the trimmed
// value from psql.
func (g *databaseGuest) queryIn(ctx context.Context, database, sql string) (string, error) {
	args := databasePsqlArgs(g.major)
	args = append(args[:len(args)-2], "-tA", "-c", sql)
	for i, arg := range args {
		if arg == "-d" {
			args[i+1] = database
		}
	}
	cmd := g.asSuperuser(exec.CommandContext(ctx, args[0], args[1:]...))
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// serveImportBridge serves the socket the import tools connect to, once:
// each connection goes to the host's import relay.
func (g *databaseGuest) serveImportBridge() error {
	g.importOnce.Do(func() {
		g.importErr = g.listenSourceBridge(databaseImportSocketDir, dbguest.ImportPort)
	})
	return g.importErr
}

func (g *databaseGuest) listenSourceBridge(directory string, port uint32) error {
	return g.listenGuardedSourceBridge(directory, port, nil)
}

func (g *databaseGuest) listenGuardedSourceBridge(directory string, port uint32, admit func() bool) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chown(directory, g.uid, g.gid); err != nil {
		return err
	}
	path := filepath.Join(directory, ".s.PGSQL.5432")
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
			go func() {
				if admit != nil && !admit() {
					_ = conn.Close()
					return
				}
				relayToImportSource(conn, port)
			}()
		}
	}()
	return nil
}

func relayToImportSource(local net.Conn, port uint32) {
	defer func() { _ = local.Close() }()
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: port}); err != nil {
		_ = unix.Close(fd)
		return
	}
	host := &runtimeConfigVsockConn{
		File:   os.NewFile(uintptr(fd), "import-vsock"),
		local:  runtimeConfigVsockAddr{unix.VMADDR_CID_ANY, 0},
		remote: runtimeConfigVsockAddr{unix.VMADDR_CID_HOST, port},
	}
	defer func() { _ = host.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(host, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, host); done <- struct{}{} }()
	<-done
}
