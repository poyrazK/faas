//go:build linux

package guestdisk

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
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
	rest := args
	for len(rest) > 0 {
		flag := rest[0]
		rest = rest[1:]
		if flag == "--" {
			if len(rest) == 0 {
				return spec, errors.New("no tool after --")
			}
			spec.tool, spec.args = rest[0], rest[1:]
			break
		}
		if flag == "--require-landlock" {
			spec.requireLandlock = true
			continue
		}
		if len(rest) == 0 {
			return spec, fmt.Errorf("%s needs a value", flag)
		}
		v := rest[0]
		rest = rest[1:]
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
		if err := exposeSessionPaths(spec); err != nil {
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

// exposeSessionPaths makes the image and the session's scratch directories
// reachable by uid/gid at their own paths. They live under the jail root,
// which admits only root (/srv/fc/jail is a mode-1700 tmpfs), and the tool
// and the Landlock rules open them after dropTo. In this private mount
// namespace each topmost ancestor the identity cannot search is covered with
// a tmpfs holding only the session's paths, bound back from descriptors
// opened before the cover, so the tool sees nothing else below it.
func exposeSessionPaths(spec helperSpec) error {
	paths := append(append([]string{spec.image}, spec.readDirs...), spec.writeDirs...)
	covers := map[string][]string{}
	seen := map[string]bool{}
	for _, p := range paths {
		// A /proc/self/fd path is a descriptor vmmd handed over (build
		// output), reachable whatever its directory's mode.
		if seen[p] || strings.HasPrefix(p, "/proc/") {
			continue
		}
		seen[p] = true
		cover, err := unsearchableAncestor(p, spec.uid, spec.gid)
		if err != nil {
			return err
		}
		if cover != "" {
			covers[cover] = append(covers[cover], p)
		}
	}
	fds := map[string]int{}
	defer func() {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}()
	for _, ps := range covers {
		for _, p := range ps {
			fd, err := unix.Open(p, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return fmt.Errorf("open %s: %w", p, err)
			}
			fds[p] = fd
		}
	}
	for cover, ps := range covers {
		if err := unix.Mount("tmpfs", cover, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "size=64k,mode=0711"); err != nil {
			return fmt.Errorf("cover %s: %w", cover, err)
		}
		for _, p := range ps {
			if err := bindBack(cover, p, fds[p]); err != nil {
				return err
			}
		}
	}
	return nil
}

// unsearchableAncestor returns the topmost directory above p that uid/gid
// cannot search, or "" when every one admits it. Supplementary groups are
// dropped before the tool runs, so only gid counts.
func unsearchableAncestor(p string, uid, gid int) (string, error) {
	for i := 1; i < len(p); i++ {
		if p[i] != '/' {
			continue
		}
		dir := p[:i]
		var st unix.Stat_t
		if err := unix.Stat(dir, &st); err != nil {
			return "", fmt.Errorf("stat %s: %w", dir, err)
		}
		if !searchableBy(st, uid, gid) {
			return dir, nil
		}
	}
	return "", nil
}

func searchableBy(st unix.Stat_t, uid, gid int) bool {
	switch {
	case int(st.Uid) == uid:
		return st.Mode&0o100 != 0
	case int(st.Gid) == gid:
		return st.Mode&0o010 != 0
	default:
		return st.Mode&0o001 != 0
	}
}

// bindBack recreates p's directories below cover, searchable but not
// listable, and binds the original p (held as fd) over a placeholder there.
func bindBack(cover, p string, fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("stat %s: %w", p, err)
	}
	rel := strings.Split(strings.TrimPrefix(p, cover+"/"), "/")
	dir := cover
	for _, part := range rel[:len(rel)-1] {
		dir = filepath.Join(dir, part)
		if err := os.Mkdir(dir, 0o711); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("recreate %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o711); err != nil {
			return fmt.Errorf("recreate %s: %w", dir, err)
		}
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		if err := os.Mkdir(p, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("placeholder %s: %w", p, err)
		}
	case unix.S_IFREG:
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("placeholder %s: %w", p, err)
		}
		_ = f.Close()
	default:
		return fmt.Errorf("%s is neither a directory nor a regular file", p)
	}
	if err := unix.Mount(fmt.Sprintf("/proc/self/fd/%d", fd), p, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind %s: %w", p, err)
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
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), size, 0) //nolint:gosec // G103: Landlock takes a C struct by pointer; x/sys has no wrapper.
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
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&attr)), 0, 0, 0); errno != 0 { //nolint:gosec // G103: see landlock_create_ruleset above.
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
