package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
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
	readyTimeout time.Duration // WAKING/cold-boot readiness budget (spec §6)

	mu   sync.Mutex
	proc map[string]*exec.Cmd // instance -> running jailer process
}

// NewJailerVMM constructs a JailerVMM. readyTimeout of 0 defaults to 30s (the
// COLD_BOOTING ceiling, spec §6.1).
func NewJailerVMM(chrootBase string, readyTimeout time.Duration) *JailerVMM {
	if readyTimeout <= 0 {
		readyTimeout = 30 * time.Second
	}
	return &JailerVMM{
		chrootBase:   chrootBase,
		readyTimeout: readyTimeout,
		proc:         make(map[string]*exec.Cmd),
	}
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
	return filepath.Join(v.chrootBase, FirecrackerBin, instance, "root")
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

	jailed, err := v.provision(root, cfg)
	if err != nil {
		return fmt.Errorf("vmm: provision chroot: %w", err)
	}
	cfgBytes, err := json.Marshal(jailed)
	if err != nil {
		return fmt.Errorf("vmm: marshal config: %w", err)
	}
	if err = os.WriteFile(filepath.Join(root, VMConfigName), cfgBytes, 0o640); err != nil {
		return fmt.Errorf("vmm: write config: %w", err)
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

	memName, err := linkInto(root, spec.MemPath)
	if err != nil {
		return fmt.Errorf("vmm: stage mem file: %w", err)
	}
	stateName, err := linkInto(root, spec.VMStatePath)
	if err != nil {
		return fmt.Errorf("vmm: stage vmstate: %w", err)
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
	// Chroot lives in tmpfs (spec §Gotchas); removing it frees the RAM it holds.
	if err := os.RemoveAll(filepath.Join(v.chrootBase, FirecrackerBin, l.Instance)); err != nil {
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
	argv := append(JailerCommand(JailerSpec{Instance: l.Instance, UID: l.UID, GID: l.GID, Netns: l.Netns}), extraFCArgs...)
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

// provision hardlinks the kernel and rootfs images into the chroot and returns a
// copy of cfg with paths rewritten to the chroot-relative basenames.
func (v *JailerVMM) provision(root string, cfg VMConfig) (VMConfig, error) {
	out := cfg
	kname, err := linkInto(root, cfg.BootSource.KernelImagePath)
	if err != nil {
		return out, err
	}
	out.BootSource.KernelImagePath = kname
	out.Drives = make([]Drive, len(cfg.Drives))
	for i, d := range cfg.Drives {
		name, err := linkInto(root, d.PathOnHost)
		if err != nil {
			return out, err
		}
		d.PathOnHost = name
		out.Drives[i] = d
	}
	return out, nil
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
func (v *JailerVMM) fcClient(instance string) *http.Client {
	sock := v.socketPath(instance)
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}
}

func (v *JailerVMM) apiPut(ctx context.Context, instance, path string, body any) error {
	return v.apiCall(ctx, http.MethodPut, instance, path, body)
}

func (v *JailerVMM) apiPatch(ctx context.Context, instance, path string, body any) error {
	return v.apiCall(ctx, http.MethodPatch, instance, path, body)
}

func (v *JailerVMM) apiCall(ctx context.Context, method, instance, path string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.fcClient(instance).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("firecracker %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(msg))
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
