//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Database mode (ADR-1027): PostgreSQL is the VM's only workload. The guest
// has no network interface; the host reaches PostgreSQL's local socket
// through the vsock data port and drives the lifecycle over the control
// port. Park is a clean PostgreSQL shutdown followed by poweroff; wake is a
// cold boot. Database VMs are never memory-snapshotted.

const (
	databaseControlTimeout  = 2 * time.Minute
	databaseReadyPoll       = 5 * time.Millisecond
	databaseFastStopWait    = 60 * time.Second
	databaseQuitStopWait    = 10 * time.Second
	databaseAdminSQLTimeout = 60 * time.Second
)

// poweroffDatabase ends the VM with a guest reset rather than a power-off:
// Firecracker emulates no power-off, so a halted guest leaves the VMM
// running, while the reset (reboot=k on the kernel line) makes Firecracker
// exit. The host treats that exit as the park completing.
var poweroffDatabase = func() error {
	unix.Sync()
	return unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART)
}

type databaseGuest struct {
	log    *slog.Logger
	major  int
	memKiB int64
	uid    int
	gid    int

	mu         sync.Mutex
	state      dbguest.State
	errCode    string
	postmaster *os.Process
	exited     chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
}

func runDatabaseGuest(log *slog.Logger, major int) error {
	if log == nil {
		log = slog.Default()
	}
	g := &databaseGuest{log: log, major: major, exited: make(chan struct{}), done: make(chan struct{})}
	g.prepare()
	control, err := listenGuestVsockBacklog(dbguest.ControlPort, 8)
	if err != nil {
		log.Error("database control listener unavailable", "err", err)
		return errors.Join(err, poweroffDatabase())
	}
	data, err := listenGuestVsockBacklog(dbguest.DataPort, 128)
	if err != nil {
		_ = control.Close()
		log.Error("database data listener unavailable", "err", err)
		return errors.Join(err, poweroffDatabase())
	}
	go g.serveControl(control)
	go g.serveData(data)
	if g.currentState() == dbguest.StateStarting {
		g.startPostgres()
	}
	<-g.done
	_ = control.Close()
	_ = data.Close()
	return poweroffDatabase()
}

// prepare mounts the runtime filesystems and the data volume and decides
// whether the volume holds a cluster. Failures leave the guest serving
// status so the host can read the stable error code before stopping it.
func (g *databaseGuest) prepare() {
	if err := mountDatabaseRuntime(); err != nil {
		g.fail(dbguest.ErrCodeVolumeFailed, "mount runtime filesystems", err)
		return
	}
	passwd, err := os.ReadFile("/etc/passwd")
	uid, gid, ok := lookupPasswdUser(passwd, databaseOSUser)
	if err != nil || !ok {
		g.fail(dbguest.ErrCodeStartFailed, "find database user", err)
		return
	}
	g.uid, g.gid = uid, gid
	meminfo, err := os.ReadFile("/proc/meminfo")
	if err == nil {
		g.memKiB, err = parseMemTotalKiB(meminfo)
	}
	if err != nil {
		g.fail(dbguest.ErrCodeStartFailed, "read guest memory", err)
		return
	}
	if err := prepareDatabaseSocketDir(uid, gid); err != nil {
		g.fail(dbguest.ErrCodeStartFailed, "prepare socket directory", err)
		return
	}
	if err := unix.Mount(databaseVolumeDevice, databaseMountPoint, "ext4", unix.MS_NOATIME|unix.MS_NODEV|unix.MS_NOSUID, ""); err != nil {
		g.fail(dbguest.ErrCodeVolumeFailed, "mount data volume", err)
		return
	}
	g.decideClusterState()
}

func (g *databaseGuest) decideClusterState() {
	version, err := os.ReadFile(filepath.Join(databasePGData, "PG_VERSION"))
	if errors.Is(err, os.ErrNotExist) {
		g.setState(dbguest.StateNeedsBootstrap)
		return
	}
	major, parseErr := clusterMajor(version)
	if err != nil || parseErr != nil {
		g.fail(dbguest.ErrCodeVolumeFailed, "read cluster version", errors.Join(err, parseErr))
		return
	}
	if major != g.major {
		g.fail(dbguest.ErrCodeMajorMismatch, fmt.Sprintf("cluster major %d, binaries %d", major, g.major), nil)
		return
	}
	g.setState(dbguest.StateStarting)
}

func prepareDatabaseSocketDir(uid, gid int) error {
	if err := os.MkdirAll(databaseSocketDir, 0o755); err != nil {
		return err
	}
	return os.Chown(databaseSocketDir, uid, gid)
}

func mountDatabaseRuntime() error {
	for _, m := range []struct {
		dst  string
		mode string
	}{{"/run", "mode=0755"}, {"/dev/shm", "mode=1777"}} {
		if err := os.MkdirAll(m.dst, 0o755); err != nil {
			return err
		}
		if err := unix.Mount("tmpfs", m.dst, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, m.mode); err != nil {
			return fmt.Errorf("mount %s: %w", m.dst, err)
		}
	}
	return nil
}

func (g *databaseGuest) serveControl(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		g.handleControl(conn)
	}
}

func (g *databaseGuest) handleControl(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(databaseControlTimeout))
	line, err := bufio.NewReaderSize(io.LimitReader(conn, dbguest.MaxRequestBytes), dbguest.MaxRequestBytes).ReadBytes('\n')
	var req dbguest.Request
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&req)
	}
	if err == nil {
		err = req.Validate()
	}
	resp := g.response()
	switch {
	case err != nil:
		resp.Error = dbguest.ErrCodeInvalidRequest
	case req.Op == dbguest.OpBootstrap:
		resp = g.bootstrap()
	case req.Op == dbguest.OpAdminSQL:
		resp = g.adminSQL(req.SQL)
	case req.Op == dbguest.OpStop:
		g.stop()
		resp = g.response()
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

// bootstrap initializes an empty volume once, then starts PostgreSQL. No
// secret is involved: the superuser authenticates only by peer.
func (g *databaseGuest) bootstrap() dbguest.Response {
	if g.currentState() != dbguest.StateNeedsBootstrap {
		resp := g.response()
		resp.Error = dbguest.ErrCodeAlreadyInitialized
		return resp
	}
	if err := g.initCluster(); err != nil {
		g.fail(dbguest.ErrCodeInitFailed, "initialize cluster", err)
		return g.response()
	}
	g.setState(dbguest.StateStarting)
	g.startPostgres()
	return g.response()
}

func (g *databaseGuest) initCluster() error {
	if err := os.Chown(databaseMountPoint, g.uid, g.gid); err != nil {
		return err
	}
	if err := os.Chmod(databaseMountPoint, 0o700); err != nil {
		return err
	}
	args := databaseInitdbArgs(g.major)
	cmd := g.asDatabaseUser(exec.Command(args[0], args[1:]...))
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	hba := filepath.Join(databasePGData, "pg_hba.conf")
	if err := os.WriteFile(hba, []byte(databaseHBA), 0o600); err != nil {
		return err
	}
	return os.Chown(hba, g.uid, g.gid)
}

// adminSQL runs a control-plane batch as the superuser. The SQL can carry
// role passwords, so neither it nor psql's output is logged; only the
// SQLSTATE of a failure is returned.
func (g *databaseGuest) adminSQL(sql string) dbguest.Response {
	if g.currentState() != dbguest.StateReady {
		resp := g.response()
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseAdminSQLTimeout)
	defer cancel()
	args := databasePsqlArgs(g.major)
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, args[0], args[1:]...))
	cmd.Stdin = strings.NewReader(sql)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	resp := g.response()
	if err := cmd.Run(); err != nil {
		resp.Error = dbguest.ErrCodeAdminSQLFailed
		resp.SQLState = parseSQLState(stderr.Bytes())
	}
	return resp
}

// limitedWriter keeps the first n bytes and drops the rest.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		keep := p
		if len(keep) > l.n {
			keep = keep[:l.n]
		}
		written, err := l.w.Write(keep)
		l.n -= written
		if err != nil {
			return written, err
		}
	}
	return len(p), nil
}

func (g *databaseGuest) asDatabaseUser(cmd *exec.Cmd) *exec.Cmd {
	cmd.Env = []string{"PATH=" + databaseBinDir(g.major) + ":/usr/bin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
	cmd.Dir = databaseMountPoint
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(g.uid), Gid: uint32(g.gid)},
		Setsid:     true,
	}
	return cmd
}

func (g *databaseGuest) startPostgres() {
	args := databaseServerArgs(g.major, g.memKiB)
	cmd := g.asDatabaseUser(exec.Command(args[0], args[1:]...))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		g.fail(dbguest.ErrCodeStartFailed, "start postgres", err)
		return
	}
	g.mu.Lock()
	g.postmaster = cmd.Process
	g.mu.Unlock()
	go g.watchPostmaster(cmd)
	go g.awaitReady()
}

func (g *databaseGuest) watchPostmaster(cmd *exec.Cmd) {
	err := cmd.Wait()
	close(g.exited)
	if g.currentState() == dbguest.StateStopping {
		return
	}
	g.fail(dbguest.ErrCodeStartFailed, "postgres exited", err)
	g.stop()
}

func (g *databaseGuest) awaitReady() {
	pidFile := filepath.Join(databasePGData, "postmaster.pid")
	for {
		select {
		case <-g.exited:
			return
		case <-time.After(databaseReadyPoll):
		}
		if data, err := os.ReadFile(pidFile); err == nil && postmasterReady(data) {
			g.mu.Lock()
			if g.state == dbguest.StateStarting {
				g.state = dbguest.StateReady
			}
			g.mu.Unlock()
			guestStage("database-ready")
			return
		}
	}
}

// stop shuts PostgreSQL down with a fast shutdown, which writes a shutdown
// checkpoint, so the next cold boot needs no crash recovery. It escalates
// to an immediate shutdown only if the fast one stalls.
func (g *databaseGuest) stop() {
	g.stopOnce.Do(func() {
		g.mu.Lock()
		if g.state != dbguest.StateFailed {
			g.state = dbguest.StateStopping
		}
		postmaster := g.postmaster
		g.mu.Unlock()
		go g.shutdown(postmaster)
	})
}

func (g *databaseGuest) shutdown(postmaster *os.Process) {
	defer close(g.done)
	if postmaster != nil {
		_ = postmaster.Signal(syscall.SIGINT)
		select {
		case <-g.exited:
		case <-time.After(databaseFastStopWait):
			g.log.Warn("database fast shutdown stalled; escalating")
			_ = postmaster.Signal(syscall.SIGQUIT)
			select {
			case <-g.exited:
			case <-time.After(databaseQuitStopWait):
			}
		}
	}
	unix.Sync()
	if err := unix.Unmount(databaseMountPoint, 0); err != nil && !errors.Is(err, unix.EINVAL) {
		g.log.Warn("database volume unmount failed", "err", err)
	}
}

// serveData bridges each vsock session to PostgreSQL's local socket.
// Sessions arriving before the database is ready are closed at once; the
// host waits for the ready state on the control port first.
func (g *databaseGuest) serveData(ln net.Listener) {
	socket := filepath.Join(databaseSocketDir, ".s.PGSQL.5432")
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if g.currentState() != dbguest.StateReady {
			_ = conn.Close()
			continue
		}
		go bridgeDatabaseSession(conn, socket)
	}
}

func bridgeDatabaseSession(client net.Conn, socket string) {
	defer func() { _ = client.Close() }()
	server, err := net.Dial("unix", socket)
	if err != nil {
		return
	}
	defer func() { _ = server.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(server, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, server); done <- struct{}{} }()
	<-done
}

func (g *databaseGuest) currentState() dbguest.State {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

func (g *databaseGuest) setState(state dbguest.State) {
	g.mu.Lock()
	g.state = state
	g.mu.Unlock()
}

func (g *databaseGuest) response() dbguest.Response {
	g.mu.Lock()
	defer g.mu.Unlock()
	return dbguest.Response{State: g.state, Major: g.major, Error: g.errCode}
}

// fail records a stable error code. The detail goes to the guest console
// only; it can contain paths but never secrets.
func (g *databaseGuest) fail(code, what string, err error) {
	g.log.Error("database guest failure", "code", code, "step", what, "err", err)
	g.mu.Lock()
	g.state = dbguest.StateFailed
	g.errCode = code
	g.mu.Unlock()
}
