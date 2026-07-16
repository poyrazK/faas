package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// JailerVMM is the production VMM. It provisions a jail chroot, launches
// firecracker under the jailer, and waits for the guest to accept on :8080
// (readiness, spec §4.8). It is validated on metal (`make test-metal`); the
// orchestration around it (Manager) is proven cross-platform with fakes.
//
// Chroot model (Appendix B): jailer builds the chroot at
// <base>/firecracker/<id>/root and execs firecracker inside it, so every path
// the VM config references must live within that root. We hardlink the shared,
// read-only kernel/base rootfs in (cheap, no copy) and link the per-app layer,
// then rewrite the config to in-chroot relative paths.
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

// chrootRoot is where jailer pivots the VM's filesystem to.
func (v *JailerVMM) chrootRoot(instance string) string {
	return filepath.Join(v.chrootBase, FirecrackerBin, instance, "root")
}

// Boot provisions the chroot, starts the jailed firecracker, and blocks until the
// guest is ready or the timeout fires. On any error it kills whatever it started.
func (v *JailerVMM) Boot(ctx context.Context, l Lease, cfg VMConfig) (err error) {
	root := v.chrootRoot(l.Instance)
	if err = os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("vmm: mkdir chroot: %w", err)
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

	argv := JailerCommand(JailerSpec{Instance: l.Instance, UID: l.UID, GID: l.GID, Netns: l.Netns})
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("vmm: start jailer: %w", err)
	}
	v.mu.Lock()
	v.proc[l.Instance] = cmd
	v.mu.Unlock()

	if err = v.waitReady(ctx, l); err != nil {
		return fmt.Errorf("vmm: readiness: %w", err)
	}
	return nil
}

// provision hardlinks the kernel and rootfs images into the chroot and returns a
// copy of cfg with paths rewritten to be relative to the chroot root.
func (v *JailerVMM) provision(root string, cfg VMConfig) (VMConfig, error) {
	out := cfg

	link := func(src string) (string, error) {
		name := filepath.Base(src)
		dst := filepath.Join(root, name)
		_ = os.Remove(dst)
		if err := os.Link(src, dst); err != nil {
			// Cross-device or unsupported: fall back to a copy.
			if cErr := copyFile(src, dst); cErr != nil {
				return "", fmt.Errorf("link/copy %s: %w", src, cErr)
			}
		}
		return name, nil
	}

	kname, err := link(cfg.BootSource.KernelImagePath)
	if err != nil {
		return out, err
	}
	out.BootSource.KernelImagePath = kname

	out.Drives = make([]Drive, len(cfg.Drives))
	for i, d := range cfg.Drives {
		name, err := link(d.PathOnHost)
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
