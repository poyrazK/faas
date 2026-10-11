//go:build linux

package runtimequalification

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

const nativeHostPATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
const nativeHostLock = "/var/lock/faas-builder-acceptance.lock"

func protectedNativePath(name string, directory bool) (string, error) {
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", err
	}
	// A protected leaf under a writable parent can still be replaced. Verify
	// every resolved ancestor too; operator staging belongs under /srv or /etc.
	for current := resolved; ; current = filepath.Dir(current) {
		var stat unix.Stat_t
		if err := unix.Lstat(current, &stat); err != nil {
			return "", err
		}
		wantType := uint32(unix.S_IFDIR)
		if current == resolved && !directory {
			wantType = unix.S_IFREG
		}
		if stat.Uid != 0 || stat.Mode&0o022 != 0 || stat.Mode&unix.S_IFMT != wantType {
			return "", fmt.Errorf("native path and ancestors must be root-owned and not writable by others: %w", ErrEvidence)
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return resolved, nil
}

func nativeTool(name string) (string, error) {
	for _, dir := range filepath.SplitList(nativeHostPATH) {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return protectedNativePath(candidate, false)
	}
	return "", fmt.Errorf("required native tool %s unavailable: %w", name, ErrEvidence)
}

// This initial read-only gate runs before opening operator infrastructure/key
// material. OpenNativeSession measures the identities again under the host lock.
func CheckNativeCollectorHost(ctx context.Context) error {
	_, err := measureNativeIdentity(ctx)
	return err
}

func measureNativeIdentity(ctx context.Context) (NativeIdentity, error) {
	ctx, stop := context.WithTimeout(ctx, api.RuntimeQualificationCleanupTimeout)
	defer stop()
	var id NativeIdentity
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return id, fmt.Errorf("collector requires native Linux amd64 as root: %w", ErrEvidence)
	}
	if _, err := protectedNativePath("/etc/faas/builder-acceptance-host", false); err != nil {
		return id, fmt.Errorf("designated native host marker: %w", err)
	}
	controllers, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		return id, fmt.Errorf("cgroups v2 unavailable: %w", err)
	}
	for _, required := range []string{"cpu", "memory", "pids"} {
		if !strings.Contains(" "+strings.TrimSpace(string(controllers))+" ", " "+required+" ") {
			return id, fmt.Errorf("native cgroup controller missing: %w", ErrEvidence)
		}
	}
	userns, err := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone")
	if err != nil || strings.TrimSpace(string(userns)) != "0" {
		return id, fmt.Errorf("unprivileged user namespaces must be disabled: %w", ErrEvidence)
	}
	kvmInfo, err := os.Stat("/dev/kvm")
	if err != nil || kvmInfo.Mode()&os.ModeCharDevice == 0 {
		return id, fmt.Errorf("native KVM character device absent: %w", ErrEvidence)
	}
	fd, err := unix.Open("/dev/kvm", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return id, fmt.Errorf("native KVM inaccessible: %w", err)
	}
	if err := unix.Close(fd); err != nil {
		return id, err
	}
	detector, err := nativeTool("systemd-detect-virt")
	if err != nil {
		return id, err
	}
	probe, err := nativeCommand(ctx, detector, nil, "", []string{"PATH=" + nativeHostPATH})
	if err != nil {
		return id, err
	}
	if (probe.ExitCode != 0 && probe.ExitCode != 1) || strings.TrimSpace(string(probe.Stdout)) != "none" {
		return id, fmt.Errorf("native host is virtualized or detection failed: %w", ErrEvidence)
	}
	for name, destination := range map[string]*string{"/etc/machine-id": &id.HostID, "/proc/sys/kernel/random/boot_id": &id.KernelBootID} {
		raw, err := os.ReadFile(name)
		if err != nil {
			return id, err
		}
		parsed, err := uuid.Parse(strings.TrimSpace(string(raw)))
		if err != nil || parsed == uuid.Nil {
			return id, fmt.Errorf("native identity invalid: %w", ErrEvidence)
		}
		*destination = parsed.String()
	}
	return id, nil
}

// ReadNativeSigningSeed never follows a final symlink or accepts a shared,
// permissive key file. The binary 32-byte seed is converted only in parent memory.
func ReadNativeSigningSeed(name string, expected ed25519.PublicKey) (ed25519.PrivateKey, error) {
	if os.Geteuid() != 0 || runtime.GOARCH != "amd64" || !filepath.IsAbs(name) {
		return nil, ErrEvidence
	}
	if _, err := protectedNativePath(filepath.Dir(name), true); err != nil {
		return nil, err
	}
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open protected native signing seed: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Nlink != 1 || stat.Size != ed25519.SeedSize {
		return nil, errors.Join(ErrEvidence, file.Close())
	}
	raw, readErr := ReadBounded(file, ed25519.SeedSize)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		clear(raw)
		return nil, err
	}
	if len(raw) != ed25519.SeedSize {
		clear(raw)
		return nil, fmt.Errorf("native signing seed changed while reading: %w", ErrEvidence)
	}
	key := ed25519.NewKeyFromSeed(raw)
	clear(raw)
	if len(expected) != ed25519.PublicKeySize || !bytes.Equal(key.Public().(ed25519.PublicKey), expected) {
		clear(key)
		return nil, fmt.Errorf("protected signer differs from public trust pin: %w", ErrEvidence)
	}
	return key, nil
}

type linuxNativeSession struct {
	config                                   NativeConfig
	fixture                                  Fixture
	identity                                 NativeIdentity
	lock                                     *os.File
	directory, source, goBinary, firecracker string
	tools                                    map[string]string
	activeServices                           []string
	quiesced, clean, closed                  bool
}

func (s *linuxNativeSession) Identity() NativeIdentity { return s.identity }

func OpenNativeSession(ctx context.Context, c NativeConfig, f Fixture) (session NativeSession, err error) {
	s := &linuxNativeSession{config: c, fixture: f, tools: map[string]string{}}
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.RuntimeQualificationCleanupTimeout)
			defer cancel()
			err = errors.Join(err, s.Close(cleanup))
		}
	}()
	s.identity, err = measureNativeIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if s.identity.HostID != f.HostID {
		return nil, fmt.Errorf("designated host differs from selected attempt: %w", ErrEvidence)
	}
	if err = s.acquire(ctx); err != nil {
		return nil, err
	}
	// Bound the complete source/tool/drain phase, including systemctl and
	// baseline leakcheck, separately from the lock wait and later build phase.
	ctx, stop := context.WithTimeout(ctx, api.RuntimeQualificationBuildTimeout)
	defer stop()
	s.identity, err = measureNativeIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if s.identity.HostID != f.HostID {
		return nil, ErrEvidence
	}
	if _, err = protectedNativePath(filepath.Dir(c.OutputDirectory), true); err != nil {
		return nil, err
	}
	for _, name := range []string{"git", "bash", "firecracker", "jailer", "systemctl", "ip", "iptables", "nft", "tc", "gcc", "debugfs", "python3", "readlink"} {
		s.tools[name], err = nativeTool(name)
		if err != nil {
			return nil, err
		}
	}
	s.goBinary, err = protectedNativePath(c.GoBinary, false)
	if err != nil {
		return nil, err
	}
	s.firecracker = s.tools["firecracker"]
	if err = s.checkHostAssets(ctx); err != nil {
		return nil, err
	}
	parent, err := protectedNativePath("/srv/fc/acceptance", true)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "runtime-qualification-") {
			return nil, fmt.Errorf("prior native run staging retained; operator recovery required before another attempt: %w", ErrEvidence)
		}
	}
	s.directory = filepath.Join(parent, "runtime-qualification-"+f.RunID)
	if err = os.Mkdir(s.directory, 0o700); err != nil {
		s.directory = ""
		return nil, fmt.Errorf("reserve native run staging: %w", err)
	}
	if err = syncEvidenceDirectory(parent); err != nil {
		return nil, fmt.Errorf("retain native recovery directory: %w", err)
	}
	s.source = filepath.Join(s.directory, "source")
	if err = os.Mkdir(s.source, 0o700); err != nil {
		return nil, err
	}
	if err = s.snapshotSource(ctx); err != nil {
		return nil, err
	}
	for _, dir := range []string{"tmp", "home", "go-build", "go-mod", "bin"} {
		if err = os.Mkdir(filepath.Join(s.directory, dir), 0o700); err != nil {
			return nil, err
		}
	}
	if err = s.quiesce(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *linuxNativeSession) acquire(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(nativeHostLock), 0o755); err != nil {
		return err
	}
	fd, err := unix.Open(nativeHostLock, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	s.lock = os.NewFile(uintptr(fd), nativeHostLock)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o022 != 0 {
		return ErrEvidence
	}
	wait, cancel := context.WithTimeout(ctx, api.RuntimeQualificationLockTimeout)
	defer cancel()
	ticker := time.NewTicker(api.RuntimeQualificationLockPollInterval)
	defer ticker.Stop()
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return err
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("native acceptance lock busy: %w", wait.Err())
		case <-ticker.C:
		}
	}
}

func (s *linuxNativeSession) environment(cgo string) []string {
	return []string{"PATH=" + nativeHostPATH, "HOME=" + filepath.Join(s.directory, "home"), "TMPDIR=" + filepath.Join(s.directory, "tmp"), "GOCACHE=" + filepath.Join(s.directory, "go-build"), "GOMODCACHE=" + filepath.Join(s.directory, "go-mod"), "GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "GOFLAGS=", "CGO_ENABLED=" + cgo, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_REPLACE_OBJECTS=1"}
}

func (s *linuxNativeSession) checkHostAssets(ctx context.Context) error {
	for name, want := range map[string]string{s.goBinary: s.config.GoSHA256, s.firecracker: s.fixture.FirecrackerSHA256, s.config.KernelPath: s.fixture.KernelSHA256} {
		if _, err := protectedNativePath(name, false); err != nil {
			return err
		}
		got, err := hashNativeFile(ctx, name)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("pinned native host asset changed: %w", ErrEvidence)
		}
	}
	version, err := nativeCommand(ctx, s.firecracker, []string{"--version"}, "", []string{"PATH=" + nativeHostPATH})
	first := strings.TrimSpace(strings.SplitN(string(version.Stdout), "\n", 2)[0])
	if err != nil || version.ExitCode != 0 || first != "Firecracker v"+s.config.FirecrackerVersion {
		return errors.Join(err, fmt.Errorf("installed firecracker version differs from pin: %w", ErrEvidence))
	}
	return nil
}

func (s *linuxNativeSession) quiesce(ctx context.Context) error {
	// Run the pinned source's leakcheck before stopping services; no live tenant
	// workload is allowed. Repeat after stops to catch a drain/start race.
	result, err := s.runLeakcheck(ctx)
	if err != nil || result.ExitCode != 0 {
		return errors.Join(err, fmt.Errorf("acceptance host is not drained: %w", ErrEvidence))
	}
	if err := verifyLeakcheck(result.Stdout); err != nil {
		return err
	}
	services := []string{"faas-schedd", "faas-builderd", "faas-imaged", "faas-gatewayd-public", "faas-gatewayd-internal", "faas-vmmd"}
	for _, service := range services {
		status, err := nativeCommand(ctx, s.tools["systemctl"], []string{"is-active", "--quiet", service}, s.source, s.environment("0"))
		if err != nil {
			return err
		}
		switch status.ExitCode {
		case 3, 4:
			continue
		case 0:
		default:
			return fmt.Errorf("cannot inspect acceptance service state: %w", ErrEvidence)
		}
		s.activeServices = append(s.activeServices, service)
		// Retain original active units before a stop, including partial failures.
		if err := s.recordActiveService(service); err != nil {
			return err
		}
		s.quiesced = true
		stop, err := nativeCommand(ctx, s.tools["systemctl"], []string{"stop", service}, s.source, s.environment("0"))
		if err != nil || stop.ExitCode != 0 {
			return errors.Join(err, fmt.Errorf("cannot quiesce acceptance service: %w", ErrEvidence))
		}
	}
	s.quiesced = true
	result, err = s.runLeakcheck(ctx)
	if err != nil || result.ExitCode != 0 {
		return errors.Join(err, fmt.Errorf("acceptance host raced with drain: %w", ErrEvidence))
	}
	if err := verifyLeakcheck(result.Stdout); err != nil {
		return err
	}
	forwarding, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil || strings.TrimSpace(string(forwarding)) != "1" {
		return ErrEvidence
	}
	bridge, err := nativeCommand(ctx, s.tools["ip"], []string{"link", "show", "br-tenants"}, s.source, s.environment("0"))
	if err != nil || bridge.ExitCode != 0 {
		return errors.Join(err, fmt.Errorf("tenant bridge unavailable: %w", ErrEvidence))
	}
	return nil
}

func (s *linuxNativeSession) runLeakcheck(ctx context.Context) (CommandResult, error) {
	return nativeCommand(ctx, s.tools["bash"], []string{filepath.Join(s.source, "deploy", "scripts", "leakcheck.sh")}, s.source, s.environment("0"))
}
func (s *linuxNativeSession) Leakcheck(ctx context.Context) (CommandResult, error) {
	result, err := s.runLeakcheck(ctx)
	s.clean = err == nil && result.ExitCode == 0 && verifyLeakcheck(result.Stdout) == nil
	return result, err
}

func (s *linuxNativeSession) Recheck(ctx context.Context) error {
	identity, err := measureNativeIdentity(ctx)
	if err != nil {
		return err
	}
	if identity != s.identity {
		return fmt.Errorf("native host rebooted or changed during acceptance: %w", ErrEvidence)
	}
	return s.checkHostAssets(ctx)
}

func (s *linuxNativeSession) Close(ctx context.Context) error {
	if s.closed {
		return nil
	}
	s.closed = true
	var closeErr error
	if s.quiesced && !s.clean {
		_, err := s.Leakcheck(ctx)
		closeErr = errors.Join(closeErr, err)
		if !s.clean {
			closeErr = errors.Join(closeErr, fmt.Errorf("acceptance host quarantined: cleanup failed; services remain stopped and staging retained: %w", ErrEvidence))
		}
	}
	if !s.quiesced || s.clean {
		for i := len(s.activeServices) - 1; i >= 0; i-- {
			start, err := nativeCommand(ctx, s.tools["systemctl"], []string{"start", s.activeServices[i]}, s.source, s.environment("0"))
			if err != nil || start.ExitCode != 0 {
				closeErr = errors.Join(closeErr, err, fmt.Errorf("acceptance service %s restoration failed: %w", s.activeServices[i], ErrEvidence))
			}
		}
		if s.directory != "" && closeErr == nil {
			closeErr = errors.Join(closeErr, os.RemoveAll(s.directory))
		}
	}
	if s.lock != nil {
		closeErr = errors.Join(closeErr, s.lock.Close())
	}
	return closeErr
}

func protectNativeGit(directory string) error {
	_, err := protectedNativePath(directory, true)
	if err != nil {
		return err
	}
	gitDir := filepath.Join(directory, ".git")
	if _, err := os.Lstat(filepath.Join(gitDir, "objects", "info", "alternates")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("native Git alternates are unsupported: %w", ErrEvidence)
	}
	if _, err := os.Lstat(filepath.Join(gitDir, "info", "attributes")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("native Git uncommitted archive attributes are unsupported: %w", ErrEvidence)
	}
	info, err := os.Lstat(gitDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("native source must have its own protected Git object directory: %w", ErrEvidence)
	}
	return filepath.WalkDir(gitDir, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrEvidence
		}
		_, err := protectedNativePath(name, entry.IsDir())
		return err
	})
}

func (s *linuxNativeSession) recordActiveService(service string) error {
	//nolint:gosec // Fixed recovery file in this run's private owner directory.
	file, err := os.OpenFile(filepath.Join(s.directory, "active-services"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(service + "\n")
	return errors.Join(writeErr, file.Sync(), file.Close(), syncEvidenceDirectory(s.directory))
}
