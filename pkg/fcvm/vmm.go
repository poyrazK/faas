package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// JailerVMM is the production VMM. It provisions a jail chroot, launches
// firecracker under the jailer, and drives park/wake via the Firecracker API over
// the jail's unix socket. It is validated on metal (`make test-metal`); the
// orchestration around it (Manager) is proven cross-platform with fakes.
//
// Chroot model (Appendix B): jailer builds the chroot at
// <base>/firecracker/<id>/root and execs firecracker inside it, so every path the
// VM references must live within that root. We hardlink the shared read-only
// kernel/base rootfs in (cheap) and link the per-app layer / snapshot files, then
// reference them by their in-chroot basenames.
type JailerVMM struct {
	chrootBase   string        // /srv/fc/jail
	fcName       string        // chroot dir name jailer derives from the exec-file basename
	readyTimeout time.Duration // WAKING/cold-boot readiness budget (spec §6)

	mu      sync.Mutex
	proc    map[string]*exec.Cmd // instance -> running jailer process
	clients map[string]*http.Client
}

// NewJailerVMM constructs a JailerVMM. readyTimeout of 0 defaults to 30s (the
// COLD_BOOTING ceiling, spec §6.1).
func NewJailerVMM(chrootBase string, readyTimeout time.Duration) *JailerVMM {
	if readyTimeout <= 0 {
		readyTimeout = 30 * time.Second
	}
	return &JailerVMM{
		chrootBase:   chrootBase,
		fcName:       resolveFCChrootName(),
		readyTimeout: readyTimeout,
		proc:         make(map[string]*exec.Cmd),
		clients:      make(map[string]*http.Client),
	}
}

// resolveFCChrootName returns the directory name jailer will use for the chroot:
// jailer resolves the --exec-file symlink and uses the REAL binary's basename, so
// a `firecracker -> firecracker-v1.7.0` symlink (both the ansible role and the
// Lima loop ship one) makes jailer build .../firecracker-v1.7.0/<id>/root. The
// Manager must place the config/drives in that same dir, so it tracks the same
// resolved basename here. Falls back to the plain name off the metal path.
func resolveFCChrootName() string {
	p, err := exec.LookPath(FirecrackerBin)
	if err != nil {
		return FirecrackerBin
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Base(real)
	}
	return filepath.Base(p)
}

// DetectFirecrackerVersion runs `firecracker --version` and returns the version
// string (e.g. "1.7.0"). Snapshots are pinned to this value (ADR-005); on a
// change every snapshot goes stale and apps re-snapshot via cold boot.
func DetectFirecrackerVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, FirecrackerBin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("vmm: firecracker --version: %w", err)
	}
	// First line looks like "Firecracker v1.7.0".
	line := out
	if i := bytes.IndexByte(out, '\n'); i >= 0 {
		line = out[:i]
	}
	fields := bytes.Fields(line)
	if len(fields) == 0 {
		return "", fmt.Errorf("vmm: unexpected version output %q", out)
	}
	return string(bytes.TrimPrefix(fields[len(fields)-1], []byte("v"))), nil
}

func (v *JailerVMM) chrootRoot(instance string) string {
	return filepath.Join(v.chrootBase, v.fcName, instance, "root")
}

func (v *JailerVMM) socketPath(instance string) string {
	return filepath.Join(v.chrootRoot(instance), APISockName)
}

// Boot provisions the chroot, starts the jailed firecracker with a full config,
// and blocks until the guest is ready. On error it kills whatever it started.
func (v *JailerVMM) Boot(ctx context.Context, l Lease, cfg VMConfig) (err error) {
	root, err := v.mkChroot(l.Instance)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = v.Kill(context.WithoutCancel(ctx), l)
		}
	}()

	jailed, err := v.provision(root, cfg, l.UID, l.GID)
	if err != nil {
		return fmt.Errorf("vmm: provision chroot: %w", err)
	}
	cfgBytes, err := json.Marshal(jailed)
	if err != nil {
		return fmt.Errorf("vmm: marshal config: %w", err)
	}
	cfgPath := filepath.Join(root, VMConfigName)
	if err = os.WriteFile(cfgPath, cfgBytes, 0o640); err != nil {
		return fmt.Errorf("vmm: write config: %w", err)
	}
	// The jailed firecracker reads --config-file as the unprivileged uid; hand it
	// ownership so a 0640 root-owned file isn't unreadable inside the jail.
	if err = chownJail(cfgPath, l.UID, l.GID); err != nil {
		return fmt.Errorf("vmm: chown config: %w", err)
	}
	if err = v.ownChrootRoot(root, l); err != nil {
		return err
	}
	if err = v.startJailer(ctx, l, "--config-file", VMConfigName); err != nil {
		return err
	}
	if err = v.waitReady(ctx, l); err != nil {
		return fmt.Errorf("vmm: readiness: %w", err)
	}
	return nil
}

// Restore starts a bare jailed firecracker and loads a snapshot into it, resuming
// the guest (spec §4.4, mem_backend File). The netns/tap already exist (the
// Manager set them up); the restored net device references tap0 by name.
func (v *JailerVMM) Restore(ctx context.Context, l Lease, spec RestoreSpec) (err error) {
	root, err := v.mkChroot(l.Instance)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = v.Kill(context.WithoutCancel(ctx), l)
		}
	}()

	// Snapshot files are read-only inputs shared across the N instances a single
	// snapshot may restore (invariant §6.2-5): hardlink them in and widen for read
	// rather than chown, which would rewrite the shared inode owner.
	memName, err := stageReadOnly(root, spec.MemPath)
	if err != nil {
		return fmt.Errorf("vmm: stage mem file: %w", err)
	}
	stateName, err := stageReadOnly(root, spec.VMStatePath)
	if err != nil {
		return fmt.Errorf("vmm: stage vmstate: %w", err)
	}
	// firecracker (as the jailer uid) writes the API socket and, later, snapshot
	// output into the chroot root — it must own that directory.
	if err = v.ownChrootRoot(root, l); err != nil {
		return err
	}

	// Start firecracker with only the API socket, then load + resume.
	if err = v.startJailer(ctx, l); err != nil {
		return err
	}
	body := map[string]any{
		"snapshot_path": stateName,
		"mem_backend":   map[string]any{"backend_type": "File", "backend_path": memName},
		"resume_vm":     true,
	}
	if err = v.apiPut(ctx, l.Instance, "/snapshot/load", body); err != nil {
		return fmt.Errorf("vmm: load snapshot: %w", err)
	}
	if err = v.waitReady(ctx, l); err != nil {
		return fmt.Errorf("vmm: readiness after restore: %w", err)
	}
	return nil
}

// Snapshot pauses the running VM, writes a full snapshot, copies the files out to
// spec's durable paths, and destroys the VM (spec §4.4).
func (v *JailerVMM) Snapshot(ctx context.Context, l Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	root := v.chrootRoot(l.Instance)
	if err := v.apiPatch(ctx, l.Instance, "/vm", map[string]any{"state": "Paused"}); err != nil {
		return SnapshotInfo{}, fmt.Errorf("vmm: pause: %w", err)
	}
	const memName, stateName = "mem", "vmstate"
	create := map[string]any{
		"snapshot_type": "Full",
		"snapshot_path": stateName,
		"mem_file_path": memName,
	}
	if err := v.apiPut(ctx, l.Instance, "/snapshot/create", create); err != nil {
		return SnapshotInfo{}, fmt.Errorf("vmm: create snapshot: %w", err)
	}

	memBytes, err := moveOut(filepath.Join(root, memName), spec.MemPath)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("vmm: export mem: %w", err)
	}
	stateBytes, err := moveOut(filepath.Join(root, stateName), spec.VMStatePath)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("vmm: export vmstate: %w", err)
	}

	// Snapshot semantics: the VM is destroyed after a successful snapshot.
	_ = v.Kill(ctx, l)
	return SnapshotInfo{MemBytes: memBytes, VMStateBytes: stateBytes}, nil
}

// Kill stops the jailer process (if any) and removes the chroot. Idempotent.
func (v *JailerVMM) Kill(_ context.Context, l Lease) error {
	v.mu.Lock()
	cmd := v.proc[l.Instance]
	delete(v.proc, l.Instance)
	v.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	v.closeClient(l.Instance)
	// Chroot lives in tmpfs (spec §Gotchas); removing it frees the RAM it holds.
	if err := os.RemoveAll(filepath.Join(v.chrootBase, v.fcName, l.Instance)); err != nil {
		return fmt.Errorf("vmm: remove chroot: %w", err)
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

func (v *JailerVMM) mkChroot(instance string) (string, error) {
	root := v.chrootRoot(instance)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("vmm: mkdir chroot: %w", err)
	}
	return root, nil
}

// startJailer launches jailer→firecracker for the instance with any extra
// firecracker args appended, and records the process.
func (v *JailerVMM) startJailer(ctx context.Context, l Lease, extraFCArgs ...string) error {
	execFile, err := exec.LookPath(FirecrackerBin)
	if err != nil {
		return fmt.Errorf("vmm: locate firecracker binary: %w", err)
	}
	// Pass the resolved real binary so jailer's chroot basename matches v.fcName
	// (jailer follows the symlink); see resolveFCChrootName.
	if real, rErr := filepath.EvalSymlinks(execFile); rErr == nil {
		execFile = real
	}
	argv := append(JailerCommand(JailerSpec{
		Instance: l.Instance, UID: l.UID, GID: l.GID, Netns: l.Netns, ExecFile: execFile,
	}), extraFCArgs...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("vmm: start jailer: %w", err)
	}
	v.mu.Lock()
	v.proc[l.Instance] = cmd
	v.mu.Unlock()
	return nil
}

// provision stages the kernel and rootfs images into the chroot for the jailer
// uid and returns a copy of cfg with paths rewritten to the chroot-relative
// basenames. Read-only images (kernel, drive0 base) are hard-linked in and widened
// for read; the writable drive (drive1, the overlay upper) is copied to a private
// per-instance file owned by the uid — see stageReadOnly / stageWritable.
func (v *JailerVMM) provision(root string, cfg VMConfig, uid, gid int) (VMConfig, error) {
	out := cfg
	kname, err := stageReadOnly(root, cfg.BootSource.KernelImagePath)
	if err != nil {
		return out, err
	}
	out.BootSource.KernelImagePath = kname
	out.Drives = make([]Drive, len(cfg.Drives))
	for i, d := range cfg.Drives {
		var name string
		var err error
		if d.IsReadOnly {
			name, err = stageReadOnly(root, d.PathOnHost)
		} else {
			name, err = stageWritable(root, d.PathOnHost, uid, gid)
		}
		if err != nil {
			return out, err
		}
		d.PathOnHost = name
		out.Drives[i] = d
	}
	return out, nil
}

// ownChrootRoot hands the chroot root directory to the jailer uid so the jailed
// firecracker — which chroots into it and then runs unprivileged — can create the
// API socket there and, on Snapshot, write the mem/vmstate files it later exports.
func (v *JailerVMM) ownChrootRoot(root string, l Lease) error {
	if err := chownJail(root, l.UID, l.GID); err != nil {
		return fmt.Errorf("vmm: chown chroot root: %w", err)
	}
	return nil
}

// waitReady polls the guest's routable identity for a :8080 accept.
func (v *JailerVMM) waitReady(ctx context.Context, l Lease) error {
	deadline := time.Now().Add(v.readyTimeout)
	addr := net.JoinHostPort(l.HostIP.String(), "8080")
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("guest %s not ready after %s", l.Instance, v.readyTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fcClient returns an HTTP client bound to the instance's Firecracker API socket.
// Clients are cached per instance because http.Transport's connection pool is
// the expensive part; rebuilding per request would re-resolve the socket every
// time.
func (v *JailerVMM) fcClient(instance string) *http.Client {
	v.mu.Lock()
	defer v.mu.Unlock()
	if c, ok := v.clients[instance]; ok {
		return c
	}
	sock := v.socketPath(instance)
	c := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}
	v.clients[instance] = c
	return c
}

// closeClient drops any cached http.Client for instance. Called by Kill so a
// subsequent Boot of the same instance name gets a fresh client (and thus a
// fresh transport pool pointed at the new socket).
func (v *JailerVMM) closeClient(instance string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.clients, instance)
}

func (v *JailerVMM) apiPut(ctx context.Context, instance, path string, body any) error {
	return v.apiCall(ctx, http.MethodPut, instance, path, body)
}

func (v *JailerVMM) apiPatch(ctx context.Context, instance, path string, body any) error {
	return v.apiCall(ctx, http.MethodPatch, instance, path, body)
}

func (v *JailerVMM) apiCall(ctx context.Context, method, instance, path string, body any) error {
	return v.apiCallWithClient(ctx, v.fcClient(instance), method, path, body)
}

// apiCallWithClient is the seam that drives a single Firecracker API request.
// Split out from apiCall so tests can inject a client backed by an httptest
// server without needing the unix-socket machinery.
func (v *JailerVMM) apiCallWithClient(ctx context.Context, client *http.Client, method, path string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// startJailer returns as soon as the jailer process is forked — the
	// Firecracker API socket is created by firecracker itself a few ms
	// later. On a slow nested-KVM guest (Lima arm64) the first POST
	// races the socket creation; retry briefly before giving up so the
	// snapshot-restore path isn't held hostage to the boot timing.
	const maxAttempts = 20
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Each attempt needs a fresh body reader because http.Client.Do
		// consumes the body on send.
		req.Body = io.NopCloser(bytes.NewReader(buf))
		req.ContentLength = int64(len(buf))
		resp, err := client.Do(req)
		if err == nil {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode >= 300 {
				msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				return fmt.Errorf("firecracker %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(msg))
			}
			return nil
		}
		lastErr = err
		// Short backoff: 5ms × 20 = 100ms total. The socket appears in
		// single-digit ms on bare metal; nested KVM needs ~50ms.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	return lastErr
}

// stageReadOnly hardlinks a shared read-only source (kernel, drive0 base, or a
// snapshot file) into the chroot and widens its mode so the unprivileged jailer
// uid can read it. These files are shared across instances via hardlink (cheap —
// Appendix B), so we must NOT chown them: that would rewrite the shared inode's
// owner and break every other instance holding the same link. They are non-secret,
// read-only, and visible only inside this instance's chroot, so o+r is safe.
func stageReadOnly(root, src string) (string, error) {
	name, err := linkInto(root, src)
	if err != nil {
		return "", err
	}
	if err := ensureOtherReadable(filepath.Join(root, name)); err != nil {
		return "", err
	}
	return name, nil
}

// stageWritable copies a source image into the chroot as a private, per-instance
// file owned by the jailer uid. drive1 (the overlay upper — guest/init) is opened
// read-write by firecracker, and two instances must never share it (invariant
// §6.2-5), so it is copied — never hard-linked — and chowned to the uid. A hardlink
// would alias the shared source inode and corrupt it under concurrent writers.
func stageWritable(root, src string, uid, gid int) (string, error) {
	name := filepath.Base(src)
	dst := filepath.Join(root, name)
	// A read-only sibling drive may already have hard-linked this basename in (the
	// M0 fixture points drive0 and drive1 at the same image); drop that link first
	// so the copy below can't truncate the shared source through it.
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stage writable %s: %w", src, err)
	}
	if err := copyFile(src, dst); err != nil {
		return "", fmt.Errorf("copy writable %s: %w", src, err)
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		return "", fmt.Errorf("chmod writable %s: %w", dst, err)
	}
	if err := chownJail(dst, uid, gid); err != nil {
		return "", err
	}
	return name, nil
}

// ensureOtherReadable widens path's mode to add group+other read if it isn't there
// already. Used for shared read-only chroot files that the unprivileged jailer uid
// (never the owner, never in a matching group) must be able to open.
func ensureOtherReadable(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := fi.Mode().Perm(); perm&0o004 == 0 {
		if err := os.Chmod(path, perm|0o044); err != nil {
			return fmt.Errorf("widen readable %s: %w", path, err)
		}
	}
	return nil
}

// chownJail gives path to the jailer uid/gid. Chowning to an arbitrary uid needs
// CAP_CHOWN, i.e. root; vmmd is the only root component (spec §11) and owns all
// jail staging. Off the box the unit suite runs unprivileged, where chowning to a
// 20000+ uid would EPERM, so we skip when not root: those tests never launch a
// real jailed firecracker, and the metal suite runs as root (test-metal /
// metal-lima are sudo).
func chownJail(path string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown jail %s -> %d:%d: %w", path, uid, gid, err)
	}
	return nil
}

// linkInto hardlinks src into dir (falling back to copy on cross-device) and
// returns its basename for chroot-relative reference.
func linkInto(dir, src string) (string, error) {
	name := filepath.Base(src)
	dst := filepath.Join(dir, name)
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err != nil {
		if cErr := copyFile(src, dst); cErr != nil {
			return "", fmt.Errorf("link/copy %s: %w", src, cErr)
		}
	}
	return name, nil
}

// moveOut moves src to dst (across filesystems if needed) and returns the size.
func moveOut(src, dst string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return 0, err
	}
	if err := os.Rename(src, dst); err != nil {
		if cErr := copyFile(src, dst); cErr != nil {
			return 0, cErr
		}
		_ = os.Remove(src)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := in.Close(); cErr != nil && err == nil {
			err = cErr
		}
	}()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := out.Close(); cErr != nil && err == nil {
			err = cErr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}
