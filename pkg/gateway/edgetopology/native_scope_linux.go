//go:build linux

package edgetopology

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

type linuxNativeScope struct {
	proc      *os.Root
	cgroups   *os.Root
	pidfds    []int
	processes map[string]*os.Root
	groups    map[string]bool
	systemctl string
}

func newNativeScopeSession(ctx context.Context, review NativeScopeReview) (nativeScopeSession, error) {
	s, err := newLinuxNativeHost(ctx)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = s.Close()
		}
	}()
	for _, service := range review.Services {
		s.groups[service.Cgroup] = true
		fd, err := unix.PidfdOpen(service.PID, 0)
		if err != nil {
			return nil, nativeReadError("retained process handle")
		}
		s.pidfds = append(s.pidfds, fd)
		pid := strconv.Itoa(service.PID)
		root, err := os.OpenRoot("/proc/" + pid)
		if err != nil {
			return nil, nativeReadError("retained process procfs directory")
		}
		s.processes[pid] = root
	}
	if s.Alive() != nil {
		return nil, nativeReadError("retained process liveness")
	}
	failed = false
	return s, nil
}

func newLinuxNativeHost(ctx context.Context) (*linuxNativeScope, error) {
	if ctx.Err() != nil {
		return nil, nativeReadError("context")
	}
	for _, item := range []struct {
		name string
		kind int64
	}{{"/proc", unix.PROC_SUPER_MAGIC}, {"/sys/fs/cgroup", unix.CGROUP2_SUPER_MAGIC}} {
		var stat unix.Statfs_t
		if unix.Statfs(item.name, &stat) != nil || int64(stat.Type) != item.kind {
			return nil, nativeReadError("native procfs/cgroups v2")
		}
	}
	tool, err := nativeSystemctl()
	if err != nil {
		return nil, err
	}
	s := &linuxNativeScope{systemctl: tool, processes: make(map[string]*os.Root), groups: make(map[string]bool)}
	failed := true
	defer func() {
		if failed {
			_ = s.Close()
		}
	}()
	if s.proc, err = os.OpenRoot("/proc"); err != nil {
		return nil, nativeReadError("procfs root")
	}
	if s.cgroups, err = os.OpenRoot("/sys/fs/cgroup"); err != nil {
		return nil, nativeReadError("cgroup root")
	}
	failed = false
	return s, nil
}

func (s *linuxNativeScope) Capture(ctx context.Context, review NativeScopeReview) (NativeScopeObservation, error) {
	return captureNativeScope(ctx, s, review)
}

func (s *linuxNativeScope) Close() error {
	for _, fd := range s.pidfds {
		_ = unix.Close(fd)
	}
	s.pidfds = nil
	for _, root := range s.processes {
		_ = root.Close()
	}
	s.processes = nil
	s.groups = nil
	if s.proc != nil {
		_ = s.proc.Close()
		s.proc = nil
	}
	if s.cgroups != nil {
		_ = s.cgroups.Close()
		s.cgroups = nil
	}
	return nil
}

func (s *linuxNativeScope) Alive() error {
	if len(s.pidfds) == 0 {
		return ErrNativeUnverified
	}
	for _, fd := range s.pidfds {
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if _, err := unix.Poll(poll, 0); err != nil || poll[0].Revents != 0 {
			return nativeReadError("retained process liveness")
		}
	}
	return nil
}

func (s *linuxNativeScope) open(name string) (*os.File, error) {
	if pid, relative, ok := strings.Cut(name, "/"); ok {
		if root, exists := s.processes[pid]; exists {
			if relative != "stat" && relative != "status" && relative != "cgroup" && relative != "fd" {
				return nil, nativeReadError("reviewed process metadata leaf")
			}
			//nolint:forbidigo // Literal metadata leaf under retained reviewed process directory; contained os.Root opener.
			return root.Open(relative)
		}
	}
	if name != "sys/kernel/random/boot_id" && name != "self/net/tcp" && name != "self/net/tcp6" {
		return nil, nativeReadError("fixed host procfs metadata path")
	}
	//nolint:forbidigo // Fixed allowlisted host metadata under contained procfs root; no customer path.
	return s.proc.Open(name)
}

func (s *linuxNativeScope) Read(ctx context.Context, name string, limit int) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, nativeReadError("context")
	}
	var file *os.File
	var err error
	if name == "machine-id" {
		//nolint:forbidigo // Fixed read-only host identity path; exact reviewed content is required.
		file, err = os.Open("/etc/machine-id")
	} else {
		file, err = s.open(name)
	}
	if err != nil {
		return nil, nativeReadError("native metadata")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, nativeReadError("native metadata type")
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(raw) > limit || ctx.Err() != nil {
		return nil, nativeReadError("bounded native metadata")
	}
	return raw, nil
}

func (s *linuxNativeScope) Link(name string) (string, error) {
	value, err := os.Readlink("/proc/" + name)
	if err != nil || len(value) > api.RuntimeUpgradeNativeMetadataMaxBytes {
		return "", nativeReadError("procfs link")
	}
	return value, nil
}

func (s *linuxNativeScope) Entries(name string, limit int) ([]string, error) {
	file, err := s.open(name)
	if err != nil {
		return nil, nativeReadError("descriptor directory")
	}
	defer func() { _ = file.Close() }()
	entries, err := file.Readdirnames(limit + 1)
	if (err != nil && !errors.Is(err, io.EOF)) || len(entries) > limit {
		return nil, nativeReadError("bounded descriptor directory")
	}
	return entries, nil
}

func (s *linuxNativeScope) Executable(ctx context.Context, name string) (NativeFileIdentity, error) {
	pid, leaf, ok := strings.Cut(name, "/")
	_, retained := s.processes[pid]
	if _, canonical := nativeDecimal(pid); !ok || leaf != "exe" || !canonical || !retained {
		return NativeFileIdentity{}, nativeReadError("reviewed executable path")
	}
	// This intentional kernel exe link points outside procfs. Retained pidfds
	// span the WHOLE collection; exit/reuse is rejected before and after reads.
	//nolint:forbidigo,gosec // Canonical retained PID plus literal exe leaf; intentional kernel link, no caller filesystem path.
	file, err := os.Open("/proc/" + name)
	if err != nil {
		return NativeFileIdentity{}, nativeReadError("process executable")
	}
	defer func() { _ = file.Close() }()
	var before, after unix.Stat_t
	if unix.Fstat(int(file.Fd()), &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Mode&0o111 == 0 || before.Size < 1 || before.Size > api.RuntimeUpgradeNativeExecutableMaxBytes {
		return NativeFileIdentity{}, nativeReadError("bounded regular executable")
	}
	hash := sha256.New()
	buffer := make([]byte, api.RuntimeUpgradeNativeMetadataMaxBytes)
	var count int64
	for {
		if ctx.Err() != nil {
			return NativeFileIdentity{}, nativeReadError("context")
		}
		n, err := file.Read(buffer)
		count += int64(n)
		if count > api.RuntimeUpgradeNativeExecutableMaxBytes {
			return NativeFileIdentity{}, nativeReadError("executable byte bound")
		}
		_, _ = hash.Write(buffer[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return NativeFileIdentity{}, nativeReadError("executable read")
		}
	}
	if unix.Fstat(int(file.Fd()), &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Uid != after.Uid || before.Gid != after.Gid || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim || count != before.Size || ctx.Err() != nil {
		return NativeFileIdentity{}, nativeReadError("stable executable identity")
	}
	return NativeFileIdentity{Device: uint64(before.Dev), Inode: before.Ino, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (s *linuxNativeScope) Cgroup(name string) (NativeFileIdentity, error) {
	if !s.groups[name] {
		return NativeFileIdentity{}, nativeReadError("reviewed cgroup path")
	}
	//nolint:forbidigo // Frozen exact reviewed cgroup path under contained cgroups v2 root; no symlink escape.
	file, err := s.cgroups.Open(strings.TrimPrefix(name, "/"))
	if err != nil {
		return NativeFileIdentity{}, nativeReadError("cgroup directory")
	}
	defer func() { _ = file.Close() }()
	var stat unix.Stat_t
	if unix.Fstat(int(file.Fd()), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return NativeFileIdentity{}, nativeReadError("cgroup directory type")
	}
	return NativeFileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func (s *linuxNativeScope) Unit(ctx context.Context, unit string) ([]byte, error) {
	if !nativeUnit(unit) {
		return nil, nativeReadError("canonical service unit")
	}
	return showNativeUnit(ctx, s.systemctl, unit, "Id,LoadState,ActiveState,SubState,MainPID,ControlGroup,InvocationID,NeedDaemonReload")
}

// Callers validate the unit and supply fixed property lists only.
func showNativeUnit(ctx context.Context, tool, unit, properties string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeNativeUnitTimeout)
	defer cancel()
	//nolint:gosec // Constructor pins root-owned systemctl/protected ancestors; fixed read-only show arguments and canonical unit, no shell.
	command := exec.CommandContext(ctx, tool, "--system", "--no-pager", "--no-ask-password", "show", "--all", "--property="+properties, "--", unit)
	command.Env = []string{"LANG=C", "LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_LOG_LEVEL=err"}
	command.WaitDelay = api.RuntimeUpgradeNativeCommandWaitDelay
	output := nativeBoundedOutput{limit: api.RuntimeUpgradeNativeUnitMaxBytes}
	command.Stdout, command.Stderr = &output, io.Discard
	if command.Run() != nil || ctx.Err() != nil {
		return nil, nativeReadError("bounded local systemd properties")
	}
	return output.Bytes(), nil
}

func (s *linuxNativeScope) Addresses(ctx context.Context) ([]NativeHostAddress, error) {
	interfaces, err := net.Interfaces()
	if err != nil || len(interfaces) > api.RuntimeUpgradeNativeOriginLimit {
		return nil, nativeReadError("bounded host interfaces")
	}
	var addresses []NativeHostAddress
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 {
			continue
		}
		entries, err := item.Addrs()
		if err != nil {
			return nil, nativeReadError("host interface addresses")
		}
		for _, entry := range entries {
			prefix, err := netip.ParsePrefix(entry.String())
			if err != nil {
				return nil, nativeReadError("native interface prefix")
			}
			ip := prefix.Addr()
			bits := prefix.Bits()
			if ip.Is4In6() {
				if bits < 96 {
					return nil, nativeReadError("mapped interface prefix")
				}
				ip, bits = ip.Unmap(), bits-96
			}
			addresses = append(addresses, NativeHostAddress{IP: ip.String(), Interface: item.Name, Index: item.Index, Prefix: bits})
			if len(addresses) > api.RuntimeUpgradeNativeOriginLimit || ctx.Err() != nil {
				return nil, nativeReadError("bounded host address inventory")
			}
		}
	}
	return addresses, nil
}

func nativeSystemctl() (string, error) {
	tool, err := filepath.EvalSymlinks("/usr/bin/systemctl")
	if err != nil {
		return "", nativeReadError("protected systemctl")
	}
	for current := tool; ; current = filepath.Dir(current) {
		var stat unix.Stat_t
		kind := uint32(unix.S_IFDIR)
		if current == tool {
			kind = unix.S_IFREG
		}
		if unix.Lstat(current, &stat) != nil || stat.Uid != 0 || stat.Mode&0o022 != 0 || stat.Mode&unix.S_IFMT != kind || (current == tool && stat.Mode&0o111 == 0) {
			return "", nativeReadError("protected systemctl and ancestors")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return tool, nil
}
