//go:build linux

package main

import (
	"bytes"
	"context"
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

// importJob is the guest's one import. Its state lives in memory: a VM
// that stops mid-import rolled the restore back, so the target is empty
// again and the import can simply start over.
type importJob struct {
	status dbguest.ImportStatus
}

// startImport begins copying src into this database in the background. A
// running or finished import is reported unchanged; a failed one may be
// started again.
func (g *databaseGuest) startImport(src *dbguest.ImportSource) dbguest.Response {
	resp := g.response()
	if g.currentState() != dbguest.StateReady {
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	g.mu.Lock()
	if g.imp != nil && g.imp.status.State != dbguest.ImportFailed {
		status := g.imp.status
		g.mu.Unlock()
		resp.Import = &status
		return resp
	}
	g.imp = &importJob{status: dbguest.ImportStatus{State: dbguest.ImportRunning, StartedAt: time.Now().UTC()}}
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

// importResponse reports the import, if any.
func (g *databaseGuest) importResponse() dbguest.Response {
	resp := g.response()
	g.mu.Lock()
	defer g.mu.Unlock()
	status := dbguest.ImportStatus{State: dbguest.ImportNone}
	if g.imp != nil {
		status = g.imp.status
	}
	resp.Import = &status
	return resp
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
	g.imp.status.State, g.imp.status.Error = dbguest.ImportDone, ""
	if err != nil {
		g.imp.status.State, g.imp.status.Error = dbguest.ImportFailed, code
	}
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
		code, err = dbguest.ErrCodeImportFailed, g.importCopy(ctx, src)
	}
	if err == nil {
		// Fresh statistics: a restore leaves the planner without any.
		_, err = g.queryIn(ctx, src.TargetDatabase, "ANALYZE")
	}
	g.finishImport(code, major, err)
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
		return dbguest.ErrCodeExtension, major, fmt.Errorf("extensions not available to the owner: %s", strings.Join(missing, ", "))
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
	if err := g.importTool(ctx, src, importRestoreArgs(g.major, src.TargetDatabase, src.Owner)); err != nil {
		return fmt.Errorf("pg_restore: %w", err)
	}
	return nil
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
