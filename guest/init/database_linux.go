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

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Database mode (ADR-1027): PostgreSQL is the VM's only workload. The guest
// has no network interface; the host reaches PostgreSQL's local socket
// through the vsock data port and drives the lifecycle over the control
// port. Park is a clean PostgreSQL shutdown followed by poweroff; wake is a
// cold boot. Database VMs are never memory-snapshotted.

const (
	databaseControlTimeout = 2 * time.Minute
	databaseReadyPoll      = 5 * time.Millisecond
	databaseFastStopWait   = 60 * time.Second
	databaseQuitStopWait   = 10 * time.Second
	// databaseArchiveDrainWait bounds the pre-stop archive drain.
	databaseArchiveDrainWait = 15 * time.Second
	// databaseArchiveFlushWait bounds a restore's archive flush.
	databaseArchiveFlushWait = 60 * time.Second
	// databaseDrillWait bounds a restore drill's mark or check.
	databaseDrillWait = 30 * time.Second
	// databaseBackupTimeout bounds one base backup stream.
	databaseBackupTimeout   = time.Hour
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
	// archive ships WAL to the host (ADR-1027 durability).
	archive bool
	// restoreTarget, when set, makes an empty volume a point-in-time
	// restore of the relay's restore source.
	restoreTarget time.Time
	// restoreUntilEnd lets the restore recover to the end of the archive
	// when the archive ends before the target; targetDropped records that
	// it had to, recoveredTo the last replayed commit.
	restoreUntilEnd bool
	targetDropped   bool
	recoveredTo     time.Time
	// standby boots the volume as a hot standby of the primary until
	// promoted (ADR-1027 production class); promoteOnBoot promotes it as
	// soon as it replays (a failover while parked).
	standby       bool
	promoteOnBoot bool
	// upgradeFrom, when set, boots for an in-place upgrade from that major
	// (database_upgrade.go); upgradePending is a cluster still to upgrade.
	upgradeFrom    int
	upgradePending bool
	uid            int
	gid            int
	// bridgeUID and bridgeGID are the replication bridge's OS user; zero
	// when the base has none, and then no standby is admitted.
	bridgeUID int
	bridgeGID int

	// imp is the guest's import (database_import_linux.go); importOnce
	// serves its bridge socket.
	imp        *importJob
	importOnce sync.Once
	importErr  error

	mu         sync.Mutex
	storage    storageGuard
	state      dbguest.State
	errCode    string
	postmaster *os.Process
	// exited closes when the current postmaster exits.
	exited   chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// databaseBoot is what the kernel command line asks of database mode.
type databaseBoot struct {
	major           int
	archive         bool
	restoreTarget   time.Time
	restoreUntilEnd bool
	spare           bool
	standby         bool
	promote         bool
	upgradeFrom     int
}

func runDatabaseGuest(log *slog.Logger, boot databaseBoot) error {
	if log == nil {
		log = slog.Default()
	}
	g := &databaseGuest{log: log, major: boot.major, archive: boot.archive, restoreTarget: boot.restoreTarget, restoreUntilEnd: boot.restoreUntilEnd,
		standby: boot.standby, promoteOnBoot: boot.promote, upgradeFrom: boot.upgradeFrom, exited: make(chan struct{}), done: make(chan struct{})}
	if boot.spare {
		// The volume drive is a placeholder until OpAdopt.
		if g.prepareRuntime() {
			g.setState(dbguest.StateSpare)
		}
	} else {
		g.prepare()
	}
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
	replication, err := listenGuestVsockBacklog(dbguest.ReplicationPort, 8)
	if err != nil {
		_ = control.Close()
		_ = data.Close()
		log.Error("database replication listener unavailable", "err", err)
		return errors.Join(err, poweroffDatabase())
	}
	go g.serveControl(control)
	go g.serveData(data)
	go g.serveReplication(replication)
	go g.guardStorage()
	state := g.currentState()
	switch {
	case g.standby && (state == dbguest.StateNeedsBootstrap || state == dbguest.StateStarting):
		if err := g.serveStandbyBridge(); err != nil {
			g.fail(dbguest.ErrCodeStartFailed, "serve the standby bridge", err)
			break
		}
		g.setState(dbguest.StateStarting)
		go g.startStandby(state == dbguest.StateNeedsBootstrap)
	case state == dbguest.StateNeedsBootstrap && !g.restoreTarget.IsZero():
		g.setState(dbguest.StateStarting)
		go g.restoreAndStart()
	case state == dbguest.StateStarting && g.upgradePending:
		go g.upgradeAndStart()
	case state == dbguest.StateStarting:
		g.startPostgres()
		if fileExists(databaseUpgradeAnalyze) {
			go g.afterUpgrade()
		}
	}
	<-g.done
	_ = control.Close()
	_ = data.Close()
	_ = replication.Close()
	return poweroffDatabase()
}

// prepare mounts the runtime filesystems and the data volume and decides
// whether the volume holds a cluster. Failures leave the guest serving
// status so the host can read the stable error code before stopping it.
func (g *databaseGuest) prepare() {
	if g.prepareRuntime() {
		g.mountVolume()
	}
}

// prepareRuntime does everything before the volume: runtime mounts, the
// database user, guest memory and the socket directory. It reports success.
func (g *databaseGuest) prepareRuntime() bool {
	if err := mountDatabaseRuntime(); err != nil {
		g.fail(dbguest.ErrCodeVolumeFailed, "mount runtime filesystems", err)
		return false
	}
	passwd, err := os.ReadFile("/etc/passwd")
	uid, gid, ok := lookupPasswdUser(passwd, databaseOSUser)
	if err != nil || !ok {
		g.fail(dbguest.ErrCodeStartFailed, "find database user", err)
		return false
	}
	g.uid, g.gid = uid, gid
	if bridgeUID, bridgeGID, ok := lookupPasswdUser(passwd, databaseReplicationOSUser); ok && bridgeUID != uid {
		g.bridgeUID, g.bridgeGID = bridgeUID, bridgeGID
	} else {
		g.log.Warn("database replication unavailable: no bridge user", "user", databaseReplicationOSUser)
	}
	meminfo, err := os.ReadFile("/proc/meminfo")
	if err == nil {
		g.memKiB, err = parseMemTotalKiB(meminfo)
	}
	if err != nil {
		g.fail(dbguest.ErrCodeStartFailed, "read guest memory", err)
		return false
	}
	if err := prepareDatabaseSocketDir(uid, gid); err != nil {
		g.fail(dbguest.ErrCodeStartFailed, "prepare socket directory", err)
		return false
	}
	return true
}

// mountVolume mounts the data volume and decides whether it holds a
// cluster.
func (g *databaseGuest) mountVolume() {
	if err := unix.Mount(databaseVolumeDevice, databaseMountPoint, "ext4", unix.MS_NOATIME|unix.MS_NODEV|unix.MS_NOSUID, ""); err != nil {
		g.fail(dbguest.ErrCodeVolumeFailed, "mount data volume", err)
		return
	}
	g.decideClusterState()
}

// adopt takes the tenant volume vmmd put behind the spare's volume drive.
// The kernel read the placeholder's first blocks when it probed the drive,
// so those are dropped before mounting. A spare only serves an existing
// cluster: bootstrap and restore boots never use one.
func (g *databaseGuest) adopt() dbguest.Response {
	if g.currentState() != dbguest.StateSpare {
		resp := g.response()
		resp.Error = dbguest.ErrCodeNotSpare
		return resp
	}
	g.setState(dbguest.StateStarting)
	if err := flushBlockDevice(databaseVolumeDevice); err != nil {
		g.fail(dbguest.ErrCodeVolumeFailed, "drop the placeholder's cached blocks", err)
		return g.response()
	}
	g.mountVolume()
	switch g.currentState() {
	case dbguest.StateStarting:
		g.startPostgres()
	case dbguest.StateNeedsBootstrap:
		g.fail(dbguest.ErrCodeVolumeFailed, "adopted volume holds no cluster", nil)
	}
	return g.response()
}

// flushBlockDevice drops a block device's cached blocks (BLKFLSBUF).
func flushBlockDevice(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return unix.IoctlSetInt(int(f.Fd()), unix.BLKFLSBUF, 0)
}

func (g *databaseGuest) decideClusterState() {
	if g.standby {
		// A standby whose copy of the primary never finished copies again.
		if _, err := os.Stat(databaseSeedMarker); errors.Is(err, os.ErrNotExist) {
			g.setState(dbguest.StateNeedsBootstrap)
			return
		}
	}
	if !g.restoreTarget.IsZero() {
		// A restore whose extraction never finished starts over.
		if _, err := os.Stat(databaseRestoreMarker); errors.Is(err, os.ErrNotExist) {
			g.setState(dbguest.StateNeedsBootstrap)
			return
		}
	}
	if g.upgradeFrom != 0 && fileExists(databaseUpgradeMarker) {
		// pg_upgrade succeeded on an earlier boot; switching clusters
		// remains (database_upgrade.go).
		g.upgradePending = true
		g.setState(dbguest.StateStarting)
		return
	}
	version, err := os.ReadFile(filepath.Join(databasePGData, "PG_VERSION"))
	if errors.Is(err, os.ErrNotExist) {
		if g.upgradeFrom != 0 {
			g.fail(dbguest.ErrCodeUpgradeRefused, "an upgrade boot found no cluster", nil)
			return
		}
		g.setState(dbguest.StateNeedsBootstrap)
		return
	}
	major, parseErr := clusterMajor(version)
	if err != nil || parseErr != nil {
		g.fail(dbguest.ErrCodeVolumeFailed, "read cluster version", errors.Join(err, parseErr))
		return
	}
	if g.standby && major != g.major {
		// A copy of its primary before the primary's major upgrade:
		// copied again.
		g.setState(dbguest.StateNeedsBootstrap)
		return
	}
	if major != g.major {
		if g.upgradeFrom != 0 && major == g.upgradeFrom {
			g.upgradePending = true
			g.setState(dbguest.StateStarting)
			return
		}
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
	case req.Op == dbguest.OpBaseBackup:
		_ = conn.SetDeadline(time.Now().Add(databaseBackupTimeout))
		resp = g.baseBackup()
	case req.Op == dbguest.OpFlushWAL:
		resp = g.flushArchive(req.After)
	case req.Op == dbguest.OpAdopt:
		resp = g.adopt()
	case req.Op == dbguest.OpPromote:
		resp = g.promote()
	case req.Op == dbguest.OpReplication:
		resp = g.replication(req.Replication)
	case req.Op == dbguest.OpImport:
		resp = g.startImport(req.Import)
	case req.Op == dbguest.OpImportStatus:
		resp = g.importResponse()
	case req.Op == dbguest.OpMarkDrill:
		resp = g.markDrill(req.Drill)
	case req.Op == dbguest.OpCheckDrill:
		resp = g.checkDrill(req.Drill)
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
	return g.writeHBA()
}

// writeHBA writes the authentication files guest-init owns: pg_hba.conf
// and the pg_ident.conf map its replication line names.
func (g *databaseGuest) writeHBA() error {
	for name, content := range map[string]string{"pg_hba.conf": databaseHBA, "pg_ident.conf": databaseIdent} {
		path := filepath.Join(databasePGData, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
		if err := os.Chown(path, g.uid, g.gid); err != nil {
			return err
		}
	}
	return nil
}

// baseBackup streams pg_basebackup's tar to the host archive. Nothing of it
// is logged or returned but the backup id.
func (g *databaseGuest) baseBackup() dbguest.Response {
	resp := g.response()
	switch {
	case !g.archive:
		resp.Error = dbguest.ErrCodeArchiveOff
		return resp
	case g.currentState() != dbguest.StateReady:
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseBackupTimeout)
	defer cancel()
	args := databaseBaseBackupArgs(g.major)
	cmd := g.asDatabaseUser(exec.CommandContext(ctx, args[0], args[1:]...))
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		resp.Error = dbguest.ErrCodeBackupFailed
		return resp
	}
	conn, err := dialHostArchive()
	var id string
	if err == nil {
		id, err = dbarchive.NewClient(conn).PutBackup(stdout)
		_ = conn.Close()
	}
	if err != nil {
		_ = cmd.Process.Kill()
	}
	if waitErr := cmd.Wait(); err == nil {
		err = waitErr
	}
	if err != nil {
		g.log.Warn("database base backup failed", "err", err, "pg_basebackup", stderr.String())
		resp.Error = dbguest.ErrCodeBackupFailed
		return resp
	}
	resp.Backup = id
	return resp
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
	cmd := g.asSuperuser(exec.CommandContext(ctx, args[0], args[1:]...))
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

// asSuperuser is asDatabaseUser for the guest's own superuser psql, which
// the storage guard's read-only default never applies to.
func (g *databaseGuest) asSuperuser(cmd *exec.Cmd) *exec.Cmd {
	cmd = g.asDatabaseUser(cmd)
	cmd.Env = append(cmd.Env, databaseAdminOptions)
	return cmd
}

func (g *databaseGuest) startPostgres() {
	// pg_hba.conf is ours: rewrite it every start so existing volumes pick
	// up template changes (the replication line base backups need).
	if err := g.writeHBA(); err != nil {
		g.fail(dbguest.ErrCodeStartFailed, "write pg_hba.conf", err)
		return
	}
	args := append(databaseServerArgs(g.major, g.memKiB, g.archive), slotWALKeepSetting()...)
	g.mu.Lock()
	target, dropped, standby := g.restoreTarget, g.targetDropped, g.standby
	g.mu.Unlock()
	if standby {
		args = append(args, standbySettings(g.archive)...)
	}
	if !target.IsZero() {
		if dropped {
			target = time.Time{}
		}
		args = append(args, recoverySettings(target)...)
	}
	cmd := g.asDatabaseUser(exec.Command(args[0], args[1:]...))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		g.fail(dbguest.ErrCodeStartFailed, "start postgres", err)
		return
	}
	exited := make(chan struct{})
	g.mu.Lock()
	g.postmaster = cmd.Process
	g.exited = exited
	g.mu.Unlock()
	go g.watchPostmaster(cmd, exited)
	go g.awaitReady(exited)
}

func (g *databaseGuest) watchPostmaster(cmd *exec.Cmd, exited chan struct{}) {
	err := cmd.Wait()
	close(exited)
	if g.currentState() == dbguest.StateStopping {
		return
	}
	if g.retryToEndOfArchive() {
		return
	}
	g.fail(dbguest.ErrCodeStartFailed, "postgres exited", err)
	g.stop()
}

// retryToEndOfArchive restarts a restore that stopped before promoting,
// once and only when allowed, without its target: PostgreSQL ends a
// targeted recovery with FATAL when the archive runs out first, and with
// the source's node lost the archive is all there is.
func (g *databaseGuest) retryToEndOfArchive() bool {
	g.mu.Lock()
	retry := g.restoreUntilEnd && !g.restoreTarget.IsZero() && !g.targetDropped && g.state == dbguest.StateStarting
	if retry {
		g.targetDropped = true
	}
	g.mu.Unlock()
	if !retry {
		return false
	}
	g.log.Warn("database restore: the archive ended before the target; recovering to its end", "target", g.restoreTarget)
	g.startPostgres()
	return true
}

func (g *databaseGuest) awaitReady(exited chan struct{}) {
	pidFile := filepath.Join(databasePGData, "postmaster.pid")
	for {
		select {
		case <-exited:
			return
		case <-time.After(databaseReadyPoll):
		}
		data, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		// A hot standby reports ready once it takes read-only
		// connections; it stays a standby until promoted.
		if status := postmasterStatus(data); g.standby && (status == "ready" || status == "standby") {
			g.mu.Lock()
			if g.state == dbguest.StateStarting {
				g.state = dbguest.StateStandby
			}
			g.mu.Unlock()
			guestStage("database-standby")
			if g.promoteOnBoot {
				if resp := g.promote(); resp.Error != "" {
					g.fail(dbguest.ErrCodePromoteFailed, "promote at boot", nil)
				}
			}
			return
		}
		if postmasterReady(data) {
			// A restoring server accepts read-only connections while it
			// recovers; it is ready only once promoted.
			if !g.restoreTarget.IsZero() {
				if !g.awaitPromotion(exited) {
					select {
					case <-exited: // the postmaster's watcher decides
					default:
						g.fail(dbguest.ErrCodeRestoreFailed, "recovery did not promote", nil)
					}
					return
				}
				g.noteRecoveredTo()
			}
			// A primary starts asynchronous: no commit may wait for a
			// standby the host has not attached yet.
			if err := g.resetSynchronousOnBoot(); err != nil {
				g.fail(dbguest.ErrCodeStartFailed, "reset synchronous replication", err)
				return
			}
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
		postmaster, exited := g.postmaster, g.exited
		g.mu.Unlock()
		go g.shutdown(postmaster, exited)
	})
}

func (g *databaseGuest) shutdown(postmaster *os.Process, exited chan struct{}) {
	defer close(g.done)
	g.mu.Lock()
	standby := g.standby
	g.mu.Unlock()
	// A standby archives nothing until promoted.
	if postmaster != nil && g.archive && !standby {
		g.drainArchive()
	}
	if postmaster != nil {
		_ = postmaster.Signal(syscall.SIGINT)
		select {
		case <-exited:
		case <-time.After(databaseFastStopWait):
			g.log.Warn("database fast shutdown stalled; escalating")
			_ = postmaster.Signal(syscall.SIGQUIT)
			select {
			case <-exited:
			case <-time.After(databaseQuitStopWait):
			}
		}
	}
	unix.Sync()
	if err := unix.Unmount(databaseMountPoint, 0); err != nil && !errors.Is(err, unix.EINVAL) {
		g.log.Warn("database volume unmount failed", "err", err)
	}
}

// drainArchive switches to a new WAL segment and waits for the archiver to
// ship everything before a clean stop, so parking loses no committed work
// off the node. It gives up after databaseArchiveDrainWait: a stalled
// archive must not block the park, and the volume still holds the WAL.
func (g *databaseGuest) drainArchive() {
	ctx, cancel := context.WithTimeout(context.Background(), databaseArchiveDrainWait)
	defer cancel()
	if err := g.switchAndArchive(ctx); err != nil {
		g.log.Warn("database archive drain failed", "err", err)
	}
}

// switchAndArchive switches to a new WAL segment and waits until the
// archiver has shipped every completed one. It watches archive_status
// rather than segment names: a switch with no WAL since the previous one
// does nothing and reports the segment still in use, which is never
// archived.
func (g *databaseGuest) switchAndArchive(ctx context.Context) error {
	if _, err := g.query(ctx, "SELECT pg_switch_wal()"); err != nil {
		return fmt.Errorf("switch wal: %w", err)
	}
	statusDir := filepath.Join(databasePGData, "pg_wal", "archive_status")
	for {
		pending, err := archivePending(statusDir)
		if err == nil && pending == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%d files still unarchived: %w", pending, errors.Join(ctx.Err(), err))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// flushArchive makes the archive cover after for a point-in-time restore:
// recovery to a time stops only at a commit record past it, so the guest
// waits until its clock passes after, commits a transaction that takes a
// transaction id, and ships the segment holding that commit.
func (g *databaseGuest) flushArchive(after time.Time) dbguest.Response {
	resp := g.response()
	switch {
	case !g.archive:
		resp.Error = dbguest.ErrCodeArchiveOff
		return resp
	case g.currentState() != dbguest.StateReady:
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	case time.Until(after) > dbguest.MaxFlushAhead:
		resp.Error = dbguest.ErrCodeInvalidRequest
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseArchiveFlushWait)
	defer cancel()
	if wait := time.Until(after); wait >= 0 {
		time.Sleep(wait + time.Millisecond)
	}
	_, err := g.query(ctx, "SELECT pg_current_xact_id()")
	if err == nil {
		err = g.switchAndArchive(ctx)
	}
	if err != nil {
		g.log.Warn("database archive flush failed", "err", err)
		resp = g.response()
		resp.Error = dbguest.ErrCodeFlushFailed
		return resp
	}
	return g.response()
}

// markDrill commits a restore drill's mark (database_drill.go) and
// reports the guest clock after the commit.
func (g *databaseGuest) markDrill(drill string) dbguest.Response {
	resp := g.response()
	if g.currentState() != dbguest.StateReady {
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseDrillWait)
	defer cancel()
	out, err := g.query(ctx, drillMarkSQL(drill))
	if err == nil {
		resp.MarkedAt, err = parseDrillClock(out)
	}
	if err != nil {
		g.log.Warn("database drill mark failed", "err", err)
		resp.Error = dbguest.ErrCodeDrillMarkFailed
	}
	return resp
}

// checkDrill reports whether the database holds drill's mark.
func (g *databaseGuest) checkDrill(drill string) dbguest.Response {
	resp := g.response()
	if g.currentState() != dbguest.StateReady {
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseDrillWait)
	defer cancel()
	found := ""
	exists, err := g.query(ctx, drillTableQuery)
	if err == nil && exists == "t" {
		found, err = g.query(ctx, drillMarkQuery)
	}
	switch {
	case err != nil:
		g.log.Warn("database drill check failed", "err", err)
		resp.Error = dbguest.ErrCodeDrillMarkFailed
	case found != drill:
		resp.Error = dbguest.ErrCodeDrillMarkMissing
	}
	return resp
}

// query runs one read-only statement as the superuser and returns its
// single value.
func (g *databaseGuest) query(ctx context.Context, sql string) (string, error) {
	args := databasePsqlArgs(g.major)
	args = append(args[:len(args)-2], "-tA", "-c", sql)
	cmd := g.asSuperuser(exec.CommandContext(ctx, args[0], args[1:]...))
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
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
	resp := dbguest.Response{State: g.state, Major: g.major, Error: g.errCode, RecoveredTo: g.recoveredTo}
	g.mu.Unlock()
	if resp.State == dbguest.StateReady {
		resp.Storage = g.storageStatus()
	}
	return resp
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
