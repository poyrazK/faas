//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Streaming replication (ADR-1027 production class). A production
// database runs a primary and a hot standby on two nodes, each with the
// database's id. The standby's walreceiver connects to a socket its
// guest-init serves; guest-init dials the host's replication port, the
// host relays the stream to the primary's node, and the primary's
// guest-init hands it to PostgreSQL through a bridge process running as
// the replication OS user, which pg_ident admits as the replication role
// and nothing else. No password exists anywhere on the path.

const (
	// databaseSeedTimeout bounds a standby's copy of its primary.
	databaseSeedTimeout = 6 * time.Hour
	// databasePromoteTimeout bounds pg_promote's wait.
	databasePromoteTimeout = 60 * time.Second
	// databaseSettingWait bounds waiting for a reloaded setting to show.
	databaseSettingWait = 10 * time.Second
	// replicationBridgeCommand is guest-init re-executed as the bridge.
	replicationBridgeCommand = "replication-bridge"
	// replicationStreamsPerVM caps concurrent replication streams a
	// primary admits: a walreceiver, or a seed's two connections.
	replicationStreamsPerVM = 4
)

// serveReplication admits relayed standby streams while the database is a
// ready primary.
func (g *databaseGuest) serveReplication(ln net.Listener) {
	slots := make(chan struct{}, replicationStreamsPerVM)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if g.currentState() != dbguest.StateReady || g.bridgeUID == 0 {
			_ = conn.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		go func() {
			defer func() { <-slots }()
			g.bridgeReplication(conn)
		}()
	}
}

// bridgeReplication hands one stream to a bridge process running as the
// replication OS user, with the stream as its stdin and stdout.
func (g *databaseGuest) bridgeReplication(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	stream, ok := conn.(*runtimeConfigVsockConn)
	if !ok {
		return
	}
	cmd := exec.Command("/sbin/init", replicationBridgeCommand) //nolint:gosec // guest-init itself
	cmd.Env = []string{}
	cmd.Dir = "/"
	cmd.Stdin, cmd.Stdout = stream.File, stream.File
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(g.bridgeUID), Gid: uint32(g.bridgeGID)}, //nolint:gosec // from /etc/passwd
		Setsid:     true,
	}
	if err := cmd.Start(); err != nil {
		g.log.Warn("database replication bridge failed to start", "err", err)
		return
	}
	_ = cmd.Wait()
}

// runReplicationBridge is the bridge process: it connects to PostgreSQL as
// this process's OS user and copies the stream on stdin and stdout to and
// from it until either side ends.
func runReplicationBridge() int {
	conn, err := net.Dial("unix", filepath.Join(databaseSocketDir, ".s.PGSQL.5432"))
	if err != nil {
		return 1
	}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		if unixConn, ok := conn.(*net.UnixConn); ok {
			_ = unixConn.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() { _, _ = io.Copy(os.Stdout, conn); done <- struct{}{} }()
	<-done
	return 0
}

// serveStandbyBridge serves the socket a standby's walreceiver and seed
// connect to, relaying each connection to the host's replication port.
func (g *databaseGuest) serveStandbyBridge() error {
	if err := os.MkdirAll(databaseReplicationSocketDir, 0o700); err != nil {
		return err
	}
	if err := os.Chown(databaseReplicationSocketDir, g.uid, g.gid); err != nil {
		return err
	}
	path := filepath.Join(databaseReplicationSocketDir, ".s.PGSQL.5432")
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
			go relayToPrimary(conn)
		}
	}()
	return nil
}

func relayToPrimary(local net.Conn) {
	defer func() { _ = local.Close() }()
	host, err := dialHostReplication()
	if err != nil {
		return
	}
	defer func() { _ = host.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(host, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, host); done <- struct{}{} }()
	<-done
}

// dialHostReplication opens a stream to the host's replication port. It
// sets no timeouts: a replication stream lives as long as the standby.
func dialHostReplication() (net.Conn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: dbguest.ReplicationPort}); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("connect host replication: %w", err)
	}
	return &runtimeConfigVsockConn{
		File:   os.NewFile(uintptr(fd), "replication-vsock"),
		local:  runtimeConfigVsockAddr{unix.VMADDR_CID_ANY, 0},
		remote: runtimeConfigVsockAddr{unix.VMADDR_CID_HOST, dbguest.ReplicationPort},
	}, nil
}

// startStandby seeds an empty or half-seeded volume from the primary,
// then starts PostgreSQL as a hot standby.
func (g *databaseGuest) startStandby(seed bool) {
	if seed {
		if err := g.seedStandby(); err != nil {
			g.fail(dbguest.ErrCodeSeedFailed, "copy the primary", err)
			return
		}
	}
	signal := filepath.Join(databasePGData, "standby.signal")
	if err := os.WriteFile(signal, nil, 0o600); err != nil {
		g.fail(dbguest.ErrCodeStartFailed, "write standby.signal", err)
		return
	}
	if err := os.Chown(signal, g.uid, g.gid); err != nil {
		g.fail(dbguest.ErrCodeStartFailed, "own standby.signal", err)
		return
	}
	g.startPostgres()
}

// seedStandby copies the primary into the volume with pg_basebackup over
// the bridge. A copy interrupted before it finished starts over.
func (g *databaseGuest) seedStandby() error {
	for _, path := range []string{databaseSeedMarker, databasePGData} {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	if err := os.Chown(databaseMountPoint, g.uid, g.gid); err != nil {
		return err
	}
	if err := os.Chmod(databaseMountPoint, 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseSeedTimeout)
	defer cancel()
	args := databaseStandbySeedArgs(g.major)
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, args[0], args[1:]...))
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_basebackup: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := markDurable(databaseSeedMarker); err != nil {
		return err
	}
	g.log.Info("database standby: seeded from the primary")
	return nil
}

// promote makes a standby the primary on a new timeline, then starts it
// asynchronous like any primary boot.
func (g *databaseGuest) promote() dbguest.Response {
	if g.currentState() != dbguest.StateStandby {
		resp := g.response()
		resp.Error = dbguest.ErrCodeNotStandby
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), databasePromoteTimeout+databaseSettingWait+5*time.Second)
	defer cancel()
	out, err := g.query(ctx, fmt.Sprintf("SELECT pg_catalog.pg_promote(true, %d)", int(databasePromoteTimeout/time.Second)))
	if err == nil && out != "t" {
		err = errors.New("promotion did not finish")
	}
	if err == nil {
		err = g.resetSynchronous(ctx)
	}
	if err != nil {
		g.log.Warn("database promotion failed", "err", err)
		resp := g.response()
		resp.Error = dbguest.ErrCodePromoteFailed
		return resp
	}
	g.mu.Lock()
	g.standby = false
	if g.state == dbguest.StateStandby {
		g.state = dbguest.StateReady
	}
	g.mu.Unlock()
	g.log.Info("database standby: promoted")
	return g.response()
}

// replication sets and reports a ready primary's replication mode.
func (g *databaseGuest) replication(mode dbguest.ReplicationMode) dbguest.Response {
	resp := g.response()
	if g.currentState() != dbguest.StateReady {
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseAdminSQLTimeout)
	defer cancel()
	if mode != "" {
		if err := g.exec(ctx, replicationStatements(mode)...); err != nil {
			g.log.Warn("database replication change failed", "mode", mode, "err", err)
			resp.Error = dbguest.ErrCodeReplicationFailed
			return resp
		}
	}
	row, err := g.queryRow(ctx, replicationStatusQuery)
	var status dbguest.ReplicationStatus
	if err == nil {
		status, err = parseReplicationStatus(row)
	}
	if err != nil {
		g.log.Warn("database replication status failed", "err", err)
		resp.Error = dbguest.ErrCodeReplicationFailed
		return resp
	}
	resp.Replication = &status
	return resp
}

// resetSynchronous turns synchronous replication off and waits until new
// sessions see it off, so nothing waits for a standby that is not there.
func (g *databaseGuest) resetSynchronous(ctx context.Context) error {
	if err := g.exec(ctx, resetSynchronousStatements...); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, databaseSettingWait)
	defer cancel()
	for {
		names, err := g.query(ctx, "SELECT pg_catalog.current_setting('synchronous_standby_names')")
		if err == nil && names == "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("synchronous_standby_names still set: %w", errors.Join(ctx.Err(), err))
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// resetSynchronousOnBoot clears a synchronous setting a previous run left
// in postgresql.auto.conf before the primary reports ready. Reading the
// file first spares the common boot a psql round trip.
func (g *databaseGuest) resetSynchronousOnBoot() error {
	auto, err := os.ReadFile(filepath.Join(databasePGData, "postgresql.auto.conf"))
	if err != nil || !bytes.Contains(auto, []byte("synchronous_standby_names")) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseAdminSQLTimeout)
	defer cancel()
	return g.resetSynchronous(ctx)
}

// exec runs statements as the superuser, each on its own, stopping at the
// first error.
func (g *databaseGuest) exec(ctx context.Context, statements ...string) error {
	args := databasePsqlArgs(g.major)
	args = args[:len(args)-2]
	for _, statement := range statements {
		args = append(args, "-c", statement)
	}
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, args[0], args[1:]...))
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: sqlstate %s", err, parseSQLState(stderr.Bytes()))
	}
	return nil
}

// queryRow runs one statement and returns its single row, fields joined
// by |.
func (g *databaseGuest) queryRow(ctx context.Context, sql string) (string, error) {
	args := databasePsqlArgs(g.major)
	args = append(args[:len(args)-2], "-tA", "-F", "|", "-c", sql)
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, args[0], args[1:]...))
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// slotWALKeepSetting bounds the WAL kept for an absent standby by the
// mounted volume's size.
func slotWALKeepSetting() []string {
	var fs unix.Statfs_t
	if err := unix.Statfs(databaseMountPoint, &fs); err != nil {
		return nil
	}
	size := int64(fs.Blocks) * fs.Bsize //nolint:gosec // block counts fit
	return []string{"-c", fmt.Sprintf("max_slot_wal_keep_size=%dMB", slotWALKeepMB(size))}
}
