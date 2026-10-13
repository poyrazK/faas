package dbvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// cmdlineHasJailID reports whether a NUL-separated /proc cmdline carries
// "--id <jailID>", as jailer and the Firecracker it execs do.
func cmdlineHasJailID(cmdline []byte, jailID string) bool {
	args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--id" && args[i+1] == jailID {
			return true
		}
	}
	return false
}

// Runtime errors callers map to RPC status codes.
var (
	ErrInvalidID      = errors.New("dbvm: invalid database id")
	ErrRunning        = errors.New("dbvm: database vm is running")
	ErrNotRunning     = errors.New("dbvm: database vm is not running")
	ErrUnknownMajor   = errors.New("dbvm: no base image for postgres major")
	ErrVolumeExists   = errors.New("dbvm: volume already exists")
	ErrVolumeNotFound = errors.New("dbvm: volume not found")
)

// In-chroot names. Firecracker resolves them relative to the jail root.
const (
	jailKernel   = "vmlinux"
	jailBase     = "base.ext4"
	jailVolume   = "volume.img"
	jailIDPrefix = "db-"
	// maxUnixPath is the longest unix socket path Linux accepts.
	maxUnixPath = 107
)

var databaseIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Leaser hands out unique jail identities; *fcvm.Allocator satisfies it, so
// database and app VMs on one host never share a jail uid.
type Leaser interface {
	Acquire(instance string) (fcvm.Lease, error)
	Release(instance string) error
}

// RuntimeConfig wires the database VM runtime into vmmd.
type RuntimeConfig struct {
	// JailBase is jailer's --chroot-base-dir for database VMs only.
	JailBase string
	// VolumeRoot holds one persistent volume file per database.
	VolumeRoot string
	KernelPath string
	// Bases maps a PostgreSQL major to its read-only base image. More can
	// arrive later through SetBase (vmmd builds them in the background).
	Bases map[int]string
	// ParentCgroup is the cgroup v2 parent, relative to the mount.
	ParentCgroup   string
	JailerBin      string
	FirecrackerBin string
	Leases         Leaser
	BootTimeout    time.Duration
	StopTimeout    time.Duration
	// ArchiveSocket is dbbackupd's unix socket. When set, each VM archives
	// its WAL: the runtime relays the guest's archive streams there, after
	// a header naming the database it booted (ADR-1027 durability).
	ArchiveSocket string
}

// BootRequest starts one database VM.
type BootRequest struct {
	ID                string
	Major             int
	VcpuCount         int
	MemSizeMiB        int
	VolumeRateLimiter *fcvm.RateLimiter
	// RestoreSource names the database whose archive this one recovers
	// from; the relay stamps it so restore_command reads that archive.
	RestoreSource string
	// RestoreTarget boots the (empty) volume as a point-in-time restore of
	// RestoreSource to that instant.
	RestoreTarget time.Time
	// RestoreWindowSeconds reaches dbbackupd in the relay header and sets
	// how long the database's archive is kept.
	RestoreWindowSeconds int64
	// RestoreUntilEnd lets the restore end at the end of the archive.
	RestoreUntilEnd bool
}

// Runtime runs database VMs through jailer (ADR-1027). Only vmmd uses it.
type Runtime struct {
	cfg    RuntimeConfig
	fcReal string
	mu     sync.Mutex
	vms    map[string]*databaseVM
	// transfers are the stopped databases whose volumes are being
	// exported or imported (transfer.go).
	transfers map[string]bool
}

type databaseVM struct {
	jailID string
	lease  fcvm.Lease
	dir    string // <JailBase>/<firecracker>/<jailID>
	root   string // dir + "/root"
	cmd    *exec.Cmd
	exited chan struct{}
	binds  []string
	volume string
	// archive is the relay listener for guest archive streams.
	archive net.Listener
}

// NewRuntime validates cfg and resolves the Firecracker binary, whose real
// basename names jailer's chroot directory.
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	if cfg.JailBase == "" || cfg.VolumeRoot == "" || cfg.KernelPath == "" || cfg.ParentCgroup == "" || cfg.Leases == nil {
		return nil, errors.New("dbvm: runtime config is incomplete")
	}
	if cfg.JailBase == fcvm.JailChrootBase {
		return nil, errors.New("dbvm: database VMs need their own jail base")
	}
	if cfg.JailerBin == "" {
		cfg.JailerBin = "jailer"
	}
	if cfg.FirecrackerBin == "" {
		cfg.FirecrackerBin = fcvm.FirecrackerBin
	}
	if cfg.BootTimeout <= 0 {
		cfg.BootTimeout = 30 * time.Second
	}
	if cfg.StopTimeout <= 0 {
		// Covers guest-init's archive drain (15 s) plus PostgreSQL's fast
		// shutdown (60 s) before the VM is killed.
		cfg.StopTimeout = 90 * time.Second
	}
	fc, err := exec.LookPath(cfg.FirecrackerBin)
	if err != nil {
		return nil, fmt.Errorf("dbvm: locate firecracker: %w", err)
	}
	if real, err := filepath.EvalSymlinks(fc); err == nil {
		fc = real
	}
	if cfg.ArchiveSocket != "" {
		// The relay listens where Firecracker delivers the guest's archive
		// connections, deep in the jail; a unix socket path has 107 bytes.
		probe := &databaseVM{root: filepath.Join(cfg.JailBase, filepath.Base(fc), jailIDPrefix+strings.Repeat("0", 36), "root")}
		r := &Runtime{}
		if path := r.archiveUDSPath(probe); len(path) > maxUnixPath {
			return nil, fmt.Errorf("dbvm: jail base %s is too long for the archive relay socket (%d > %d bytes)", cfg.JailBase, len(path), maxUnixPath)
		}
	}
	bases := make(map[int]string, len(cfg.Bases))
	for major, path := range cfg.Bases {
		bases[major] = path
	}
	cfg.Bases = bases
	return &Runtime{cfg: cfg, fcReal: fc, vms: make(map[string]*databaseVM)}, nil
}

// SetBase makes path the base image for new boots of major. Running VMs
// keep the base they booted from: it is bind-mounted into their jail.
func (r *Runtime) SetBase(major int, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg.Bases[major] = path
}

func (r *Runtime) base(major int) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	path, ok := r.cfg.Bases[major]
	return path, ok
}

func validID(id string) error {
	if !databaseIDPattern.MatchString(id) {
		return ErrInvalidID
	}
	return nil
}

// VolumePath is where the database's persistent volume lives.
func (r *Runtime) VolumePath(id string) string {
	return filepath.Join(r.cfg.VolumeRoot, id+".img")
}

func (r *Runtime) jailDir(jailID string) string {
	return filepath.Join(r.cfg.JailBase, filepath.Base(r.fcReal), jailID)
}

// CreateVolume creates and formats the database's volume.
func (r *Runtime) CreateVolume(ctx context.Context, id string, sizeBytes int64) error {
	if err := validID(id); err != nil {
		return err
	}
	if err := os.MkdirAll(r.cfg.VolumeRoot, 0o700); err != nil {
		return fmt.Errorf("dbvm: volume root: %w", err)
	}
	if _, err := os.Stat(r.VolumePath(id)); err == nil {
		return ErrVolumeExists
	}
	return CreateVolume(ctx, r.VolumePath(id), sizeBytes)
}

// DeleteVolume removes a stopped database's volume. Deleting a missing
// volume succeeds so retries converge.
func (r *Runtime) DeleteVolume(_ context.Context, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	r.mu.Lock()
	_, running := r.vms[id]
	r.mu.Unlock()
	if running {
		return ErrRunning
	}
	if err := os.Remove(r.VolumePath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("dbvm: delete volume: %w", err)
	}
	return nil
}

// Volume is one database volume on this node.
type Volume struct {
	ID      string
	Running bool
}

// ListVolumes returns the node's database volumes, sorted by id.
func (r *Runtime) ListVolumes() ([]Volume, error) {
	entries, err := os.ReadDir(r.cfg.VolumeRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dbvm: list volumes: %w", err)
	}
	var out []Volume
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".img")
		if !ok || e.IsDir() || validID(id) != nil {
			continue
		}
		out = append(out, Volume{ID: id, Running: r.Running(id)})
	}
	return out, nil
}

// GrowVolume enlarges a stopped database's volume (see GrowVolume).
func (r *Runtime) GrowVolume(ctx context.Context, id string, sizeBytes int64) error {
	if err := validID(id); err != nil {
		return err
	}
	if r.Running(id) {
		return ErrRunning
	}
	path := r.VolumePath(id)
	if _, err := os.Stat(path); err != nil {
		return ErrVolumeNotFound
	}
	return GrowVolume(ctx, path, sizeBytes)
}

// Running reports whether the database VM is up.
func (r *Runtime) Running(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	vm, ok := r.vms[id]
	if !ok {
		return false
	}
	select {
	case <-vm.exited:
		return false
	default:
		return true
	}
}

// Start boots the database VM and returns once guest-init answers on the
// control port. The caller bootstraps or waits for readiness through
// Control.
func (r *Runtime) Start(ctx context.Context, req BootRequest) (err error) {
	if err := validID(req.ID); err != nil {
		return err
	}
	if (req.RestoreSource == "") != req.RestoreTarget.IsZero() || req.RestoreSource == req.ID {
		return errors.New("dbvm: a restore names another source database and a target")
	}
	if req.RestoreSource != "" {
		if err := validID(req.RestoreSource); err != nil {
			return err
		}
	}
	base, ok := r.base(req.Major)
	if !ok {
		return fmt.Errorf("%w %d", ErrUnknownMajor, req.Major)
	}
	volume := r.VolumePath(req.ID)
	if _, err := os.Stat(volume); err != nil {
		return ErrVolumeNotFound
	}
	vm, err := r.reserve(req.ID)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			r.teardown(req.ID, vm)
		}
	}()
	vm.volume = volume
	if err = r.prepareJail(vm, base); err != nil {
		return err
	}
	if err = r.writeConfig(vm, req); err != nil {
		return err
	}
	if err = r.startArchiveRelay(vm, req.ID, req); err != nil {
		return err
	}
	if err = r.launch(vm, req); err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, r.cfg.BootTimeout)
	defer cancel()
	alive, stop := context.WithCancel(waitCtx)
	defer stop()
	go func() {
		select {
		case <-vm.exited:
			stop()
		case <-alive.Done():
		}
	}()
	if _, err = WaitState(alive, r.udsPath(vm), dbguest.StateNeedsBootstrap, dbguest.StateStarting, dbguest.StateReady); err != nil {
		return fmt.Errorf("dbvm: boot %s: %w", req.ID, err)
	}
	return nil
}

func (r *Runtime) reserve(id string) (*databaseVM, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vms[id]; ok {
		return nil, ErrRunning
	}
	if r.transfers[id] {
		return nil, ErrTransferring
	}
	jailID := jailIDPrefix + id
	lease, err := r.cfg.Leases.Acquire(jailID)
	if err != nil {
		return nil, fmt.Errorf("dbvm: jail identity: %w", err)
	}
	dir := r.jailDir(jailID)
	vm := &databaseVM{jailID: jailID, lease: lease, dir: dir, root: filepath.Join(dir, "root"), exited: make(chan struct{})}
	r.vms[id] = vm
	return vm, nil
}

func (r *Runtime) udsPath(vm *databaseVM) string {
	return filepath.Join(vm.root, fcvm.VsockUDSSocketName)
}

func (r *Runtime) writeConfig(vm *databaseVM, req BootRequest) error {
	cfg, err := BuildConfig(Spec{
		KernelPath: jailKernel, BasePath: jailBase, VolumePath: jailVolume, Major: req.Major,
		VcpuCount: req.VcpuCount, MemSizeMiB: req.MemSizeMiB,
		GuestCID: fcvm.GuestVsockCID(vm.lease.Slot), VsockUDS: fcvm.VsockUDSSocketName,
		VolumeRateLimiter: req.VolumeRateLimiter, Archive: r.cfg.ArchiveSocket != "",
		RestoreTarget: req.RestoreTarget, RestoreUntilEnd: req.RestoreUntilEnd,
	})
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	path := filepath.Join(vm.root, fcvm.VMConfigName)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("dbvm: write vm config: %w", err)
	}
	return os.Chown(path, vm.lease.UID, vm.lease.GID)
}

func (r *Runtime) launch(vm *databaseVM, req BootRequest) error {
	if err := fcvm.EnsureCgroupParentControllers(r.cfg.ParentCgroup); err != nil {
		return err
	}
	argv := append(fcvm.JailerCommand(fcvm.JailerSpec{
		Instance: vm.jailID, UID: vm.lease.UID, GID: vm.lease.GID, ExecFile: r.fcReal,
		Networkless: true, ChrootBase: r.cfg.JailBase, ParentCgroup: r.cfg.ParentCgroup,
		MemoryMaxBytes: int64(api.BillableRAMMB(req.MemSizeMiB)) << 20,
	}), "--config-file", fcvm.VMConfigName)
	console, err := os.OpenFile(filepath.Join(vm.dir, "console.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("dbvm: console log: %w", err)
	}
	// The VM outlives the RPC that started it, so the command is not bound
	// to the caller's context; Stop and teardown end it.
	cmd := exec.Command(r.cfg.JailerBin, argv[1:]...)
	cmd.Stdout, cmd.Stderr = console, console
	if err := cmd.Start(); err != nil {
		_ = console.Close()
		return fmt.Errorf("dbvm: start jailer: %w", err)
	}
	vm.cmd = cmd
	go func() {
		_ = cmd.Wait()
		_ = console.Close()
		close(vm.exited)
	}()
	return nil
}

// Control sends one control request to a running database VM.
func (r *Runtime) Control(ctx context.Context, id string, req dbguest.Request) (dbguest.Response, error) {
	vm, err := r.running(id)
	if err != nil {
		return dbguest.Response{}, err
	}
	return Control(ctx, r.udsPath(vm), req)
}

// WaitState polls a running database VM until it reports one of want.
func (r *Runtime) WaitState(ctx context.Context, id string, want ...dbguest.State) (dbguest.Response, error) {
	vm, err := r.running(id)
	if err != nil {
		return dbguest.Response{}, err
	}
	return WaitState(ctx, r.udsPath(vm), want...)
}

// Dial opens a client session to the database VM's PostgreSQL socket.
func (r *Runtime) Dial(ctx context.Context, id string) (net.Conn, error) {
	vm, err := r.running(id)
	if err != nil {
		return nil, err
	}
	return Dial(ctx, r.udsPath(vm), dbguest.DataPort)
}

func (r *Runtime) running(id string) (*databaseVM, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	r.mu.Lock()
	vm, ok := r.vms[id]
	r.mu.Unlock()
	if !ok {
		return nil, ErrNotRunning
	}
	return vm, nil
}

// Stop parks the database: clean PostgreSQL shutdown, VM exit, jail
// teardown. A VM that does not exit within StopTimeout is killed; PostgreSQL
// then recovers from its WAL on the next boot.
func (r *Runtime) Stop(ctx context.Context, id string) error {
	vm, err := r.running(id)
	if err != nil {
		return err
	}
	select {
	case <-vm.exited:
	default:
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, _ = Control(stopCtx, r.udsPath(vm), dbguest.Request{Op: dbguest.OpStop})
		cancel()
		select {
		case <-vm.exited:
		case <-time.After(r.cfg.StopTimeout):
			_ = vm.cmd.Process.Kill()
			<-vm.exited
		}
	}
	r.teardown(id, vm)
	return nil
}

// teardown releases everything Start acquired. It is safe on a partially
// started VM.
func (r *Runtime) teardown(id string, vm *databaseVM) {
	if vm.archive != nil {
		_ = vm.archive.Close()
	}
	if vm.cmd != nil && vm.cmd.Process != nil {
		select {
		case <-vm.exited:
		default:
			_ = vm.cmd.Process.Kill()
			<-vm.exited
		}
	}
	unmountJail(vm.binds)
	_ = os.RemoveAll(vm.dir)
	removeJailCgroup(r.cfg.ParentCgroup, vm.jailID)
	if vm.volume != "" {
		_ = os.Chown(vm.volume, 0, 0)
	}
	_ = r.cfg.Leases.Release(vm.jailID)
	r.mu.Lock()
	delete(r.vms, id)
	r.mu.Unlock()
}
