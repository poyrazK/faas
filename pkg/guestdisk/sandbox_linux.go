//go:build linux

package guestdisk

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// sandboxSysProcAttr starts the helper in fresh mount, network, IPC and UTS
// namespaces when the caller is root, so the tool reaches no network, no
// abstract socket and no SysV IPC, and the helper's masking mounts stay
// private. An unprivileged caller (tests) cannot create them; the helper then
// applies only the limits an unprivileged process may set on itself.
func sandboxSysProcAttr() *syscall.SysProcAttr {
	attr := &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if os.Geteuid() == 0 {
		attr.Cloneflags = unix.CLONE_NEWNS | unix.CLONE_NEWNET | unix.CLONE_NEWIPC | unix.CLONE_NEWUTS
	}
	return attr
}

// helperSpec is vmmd-guestdisk's command line.
type helperSpec struct {
	uid, gid        int
	image           string
	fsize           int64
	readDirs        []string
	writeDirs       []string
	requireLandlock bool
	tool            string
	args            []string
}

// SandboxMain is vmmd-guestdisk: it confines itself and then executes the
// e2fsprogs tool named after "--". It returns only on failure, with the exit
// code the caller should use.
func SandboxMain(args []string) int {
	spec, err := parseHelperArgs(args)
	if err == nil {
		err = confine(spec)
	}
	if err == nil {
		err = syscall.Exec(spec.tool, append([]string{spec.tool}, spec.args...), os.Environ())
	}
	fmt.Fprintf(os.Stderr, "vmmd-guestdisk: %v\n", err)
	return helperFailureExit
}

func parseHelperArgs(args []string) (helperSpec, error) {
	var spec helperSpec
	spec.uid, spec.gid = -1, -1
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if flag == "--" {
			if i+1 >= len(args) {
				return spec, errors.New("no tool after --")
			}
			spec.tool, spec.args = args[i+1], args[i+2:]
			break
		}
		if flag == "--require-landlock" {
			spec.requireLandlock = true
			continue
		}
		if i+1 >= len(args) {
			return spec, fmt.Errorf("%s needs a value", flag)
		}
		i++
		v := args[i]
		var err error
		switch flag {
		case "--uid":
			spec.uid, err = strconv.Atoi(v)
		case "--gid":
			spec.gid, err = strconv.Atoi(v)
		case "--image":
			spec.image = v
		case "--fsize":
			spec.fsize, err = strconv.ParseInt(v, 10, 64)
		case "--read":
			spec.readDirs = append(spec.readDirs, v)
		case "--write":
			spec.writeDirs = append(spec.writeDirs, v)
		default:
			return spec, fmt.Errorf("unknown argument %q", flag)
		}
		if err != nil {
			return spec, fmt.Errorf("%s: %w", flag, err)
		}
	}
	switch {
	case spec.tool == "" || !filepath.IsAbs(spec.tool):
		return spec, errors.New("an absolute tool path is required after --")
	case spec.image == "" || !filepath.IsAbs(spec.image):
		return spec, errors.New("--image must be absolute")
	case spec.uid < 0 || spec.gid < 0:
		return spec, errors.New("--uid and --gid are required")
	case spec.fsize <= 0:
		return spec, errors.New("--fsize must be positive")
	}
	for _, d := range append(append([]string(nil), spec.readDirs...), spec.writeDirs...) {
		if !filepath.IsAbs(d) {
			return spec, fmt.Errorf("scratch directory %q is not absolute", d)
		}
	}
	return spec, nil
}

func confine(spec helperSpec) error {
	if os.Geteuid() == 0 {
		if spec.uid == 0 || spec.gid == 0 {
			return errors.New("refusing to run a tool as root")
		}
		if err := maskHostPaths(); err != nil {
			return err
		}
		if err := dropTo(spec.uid, spec.gid); err != nil {
			return err
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	if err := setLimits(spec.fsize); err != nil {
		return err
	}
	// An ignored signal stays ignored across execve: a write past the file
	// size limit then fails with EFBIG instead of killing the tool.
	signal.Ignore(syscall.SIGXFSZ)
	return restrictFilesystem(spec)
}

// maskHostPaths covers the host's scratch and socket directories with empty
// read-only tmpfs mounts. It refuses to run in the host's own mount
// namespace, where the mounts would hide /run from every daemon.
func maskHostPaths() error {
	self, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		return fmt.Errorf("read mount namespace: %w", err)
	}
	host, err := os.Readlink("/proc/1/ns/mnt")
	if err != nil {
		return fmt.Errorf("read host mount namespace: %w", err)
	}
	if self == host {
		return errors.New("not in a private mount namespace")
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make mounts private: %w", err)
	}
	for _, dir := range sandboxMaskedDirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		if err := unix.Mount("tmpfs", dir, "tmpfs", unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "size=4k,mode=0755"); err != nil {
			return fmt.Errorf("mask %s: %w", dir, err)
		}
	}
	return nil
}

// dropTo leaves root for uid/gid: no supplementary group, then every gid,
// then every uid. The stdlib calls apply to all threads; leaving uid 0
// clears every capability.
func dropTo(uid, gid int) error {
	if err := syscall.Setgroups(nil); err != nil {
		return fmt.Errorf("drop supplementary groups: %w", err)
	}
	if err := syscall.Setresgid(gid, gid, gid); err != nil {
		return fmt.Errorf("drop to gid %d: %w", gid, err)
	}
	if err := syscall.Setresuid(uid, uid, uid); err != nil {
		return fmt.Errorf("drop to uid %d: %w", uid, err)
	}
	if os.Getuid() != uid || os.Geteuid() != uid || os.Getgid() != gid || os.Getegid() != gid {
		return errors.New("credentials did not change")
	}
	return nil
}

// sandboxLimits bound what a tool subverted by a crafted image can consume.
// The CPU limit is a backstop; callers also bound each run with a context.
const (
	sandboxCPUSeconds  = 60
	sandboxAddressSize = 2 << 30
	sandboxOpenFiles   = 64
)

func setLimits(fsize int64) error {
	limits := []struct {
		resource int
		value    uint64
	}{
		{unix.RLIMIT_CORE, 0},
		{unix.RLIMIT_CPU, sandboxCPUSeconds},
		{unix.RLIMIT_AS, sandboxAddressSize},
		{unix.RLIMIT_FSIZE, uint64(fsize)},
		{unix.RLIMIT_NOFILE, sandboxOpenFiles},
	}
	for _, l := range limits {
		if err := unix.Setrlimit(l.resource, &unix.Rlimit{Cur: l.value, Max: l.value}); err != nil {
			return fmt.Errorf("setrlimit %d: %w", l.resource, err)
		}
	}
	return nil
}

const (
	landlockFSv1 = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM
	landlockFileRights = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
)

// restrictFilesystem confines the tool to reading the system's programs and
// libraries, reading and writing the image, and the session's scratch
// directories. On ABI 4+ it also refuses TCP, and on ABI 6+ abstract unix
// sockets and signals outside the sandbox.
func restrictFilesystem(spec helperSpec) error {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		if spec.requireLandlock {
			return fmt.Errorf("landlock unavailable: %w", errno)
		}
		return nil
	}
	handled := uint64(landlockFSv1)
	if abi >= 2 {
		handled |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		handled |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		handled |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	size := unsafe.Sizeof(attr.Access_fs)
	if abi >= 4 {
		attr.Access_net = unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP
		size = unsafe.Offsetof(attr.Scoped)
	}
	if abi >= 6 {
		attr.Scoped = unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET | unix.LANDLOCK_SCOPE_SIGNAL
		size = unsafe.Sizeof(attr)
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), size, 0)
	if errno != 0 {
		return fmt.Errorf("landlock create ruleset: %w", errno)
	}
	ruleset := int(fd)
	defer func() { _ = unix.Close(ruleset) }()

	read := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	write := read | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_TRUNCATE
	rules := []struct {
		path     string
		access   uint64
		optional bool
	}{
		{"/usr", read | unix.LANDLOCK_ACCESS_FS_EXECUTE, false},
		{"/etc", read, false},
		{"/proc", read, false},
		{"/lib", read | unix.LANDLOCK_ACCESS_FS_EXECUTE, true},
		{"/lib64", read | unix.LANDLOCK_ACCESS_FS_EXECUTE, true},
		{"/lib32", read | unix.LANDLOCK_ACCESS_FS_EXECUTE, true},
		{"/libx32", read | unix.LANDLOCK_ACCESS_FS_EXECUTE, true},
		{"/bin", read | unix.LANDLOCK_ACCESS_FS_EXECUTE, true},
		{"/sbin", read | unix.LANDLOCK_ACCESS_FS_EXECUTE, true},
		{"/dev/null", unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE, false},
		{"/dev/urandom", unix.LANDLOCK_ACCESS_FS_READ_FILE, true},
		{spec.image, unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE, false},
		{spec.tool, unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_EXECUTE, false},
	}
	for _, d := range spec.readDirs {
		rules = append(rules, struct {
			path     string
			access   uint64
			optional bool
		}{d, read, false})
	}
	for _, d := range spec.writeDirs {
		rules = append(rules, struct {
			path     string
			access   uint64
			optional bool
		}{d, write, false})
	}
	for _, r := range rules {
		if err := addLandlockRule(ruleset, r.path, r.access&handled); err != nil {
			if r.optional && errors.Is(err, unix.ENOENT) {
				continue
			}
			return fmt.Errorf("landlock rule for %s: %w", r.path, err)
		}
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("landlock restrict self: %w", errno)
	}
	return nil
}

func addLandlockRule(ruleset int, path string, access uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		access &= landlockFileRights
	}
	attr := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&attr)), 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

// LandlockABI reports the kernel's Landlock ABI version, or 0 without it.
func LandlockABI() int {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(abi)
}
