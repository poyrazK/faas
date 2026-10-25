// Package guestdisk reads and edits ext4 images that a guest may have
// written, without the host kernel ever parsing them (ADR-20261025090000).
//
// A tenant runs as root inside its microVM and can write any block of its
// writable drive. Mounting that image on the host, even read-only, hands
// attacker-chosen ext4 metadata to the host kernel's filesystem code, where a
// parser bug is a VM escape. This package instead runs e2fsprogs (e2fsck to
// replay the journal, debugfs to read and write files) in a sandboxed child
// process: the image owner's unprivileged uid, fresh mount, network, IPC and
// UTS namespaces, no_new_privs, resource limits and, where the kernel has it,
// a Landlock ruleset that confines the tool to the image and its scratch
// directory. A parser bug then costs at most what the guest's own Firecracker
// process could already do.
//
// debugfs reports failures only on stderr and always exits 0, and it prints
// guest-controlled bytes (file contents, symlink targets, names). Every
// script therefore brackets each command with a marker whose name carries a
// random nonce the guest cannot know, the session plans each change from an
// inspection of every path component (debugfs mkdir on an existing directory
// leaks an inode), and every write is read back before the session reports
// success.
package guestdisk

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Config locates e2fsprogs and the sandbox helper.
type Config struct {
	// Helper is the vmmd-guestdisk sandbox binary. Empty runs e2fsprogs
	// directly, without a sandbox: tests and non-Linux development only.
	Helper string
	// Debugfs and E2fsck are the e2fsprogs binaries. Empty looks each up on
	// PATH, then in /usr/sbin and /sbin.
	Debugfs string
	E2fsck  string
	// ScratchRoot holds one private directory per session for file contents
	// moving into or out of an image. A root caller's sandbox masks /tmp,
	// /var/tmp, /dev/shm and /run, so then neither it nor an image may live
	// under them.
	ScratchRoot string
	// AllowOwner decides whether a session may run as an image's owner. When
	// the caller is root a nil AllowOwner refuses every owner: the sandbox
	// must never run as a uid the caller has not vouched for.
	AllowOwner func(uid, gid int) bool
	// RequireLandlock fails a sandboxed run when the kernel cannot apply the
	// filesystem ruleset, instead of relying on the remaining isolation.
	RequireLandlock bool
}

// Editor runs sessions against guest-written images.
type Editor struct {
	cfg     Config
	debugfs string
	e2fsck  string
}

// New resolves the e2fsprogs binaries.
func New(cfg Config) (*Editor, error) {
	debugfs, err := lookTool(cfg.Debugfs, "debugfs")
	if err != nil {
		return nil, err
	}
	e2fsck, err := lookTool(cfg.E2fsck, "e2fsck")
	if err != nil {
		return nil, err
	}
	if cfg.ScratchRoot == "" {
		return nil, errors.New("guestdisk: ScratchRoot is required")
	}
	if !filepath.IsAbs(cfg.ScratchRoot) {
		return nil, fmt.Errorf("guestdisk: ScratchRoot %q is not absolute", cfg.ScratchRoot)
	}
	return &Editor{cfg: cfg, debugfs: debugfs, e2fsck: e2fsck}, nil
}

func lookTool(configured, name string) (string, error) {
	if configured != "" {
		if !filepath.IsAbs(configured) {
			return "", fmt.Errorf("guestdisk: %s path %q is not absolute", name, configured)
		}
		return configured, nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return filepath.Abs(p)
	}
	for _, dir := range []string{"/usr/sbin", "/sbin"} {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("guestdisk: %s not found (install e2fsprogs)", name)
}

// sandboxMaskedDirs are covered by an empty read-only tmpfs inside the
// sandbox's mount namespace when the caller is root.
var sandboxMaskedDirs = []string{"/tmp", "/var/tmp", "/dev/shm", "/run"}

func maskedBy(p string) string {
	for _, masked := range sandboxMaskedDirs {
		if p == masked || strings.HasPrefix(p, masked+"/") {
			return masked
		}
	}
	return ""
}

// Session is one image opened for inspection and editing. It is not safe for
// concurrent use, and nothing else may write the image while it is open: the
// guest is stopped and no other session holds it.
type Session struct {
	ed      *Editor
	image   string
	size    int64
	uid     int
	gid     int
	scratch string
	nonce   string
	seq     int
	// Runs counts sandboxed tool invocations, for timing breakdowns.
	Runs int
}

// Open starts a session on image. The tools run as the image's owner, which
// for a VM drive is the instance's jail uid.
func (e *Editor) Open(image string) (*Session, error) {
	if !filepath.IsAbs(image) {
		return nil, fmt.Errorf("guestdisk: image path %q is not absolute", image)
	}
	info, err := os.Lstat(image)
	if err != nil {
		return nil, fmt.Errorf("guestdisk: stat image: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("guestdisk: image %s is not a regular file", image)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errors.New("guestdisk: image owner unavailable")
	}
	uid, gid := int(st.Uid), int(st.Gid)
	if os.Geteuid() == 0 && e.cfg.Helper != "" {
		for _, p := range []string{e.cfg.ScratchRoot, image} {
			if masked := maskedBy(p); masked != "" {
				return nil, fmt.Errorf("guestdisk: %s is under %s, which the sandbox masks", p, masked)
			}
		}
		if uid == 0 || gid == 0 {
			return nil, fmt.Errorf("guestdisk: image %s is owned by root; refusing to edit it as root", image)
		}
		if e.cfg.AllowOwner == nil || !e.cfg.AllowOwner(uid, gid) {
			return nil, fmt.Errorf("guestdisk: image %s owner %d:%d is not an allowed sandbox identity", image, uid, gid)
		}
	}
	if err := os.MkdirAll(e.cfg.ScratchRoot, 0o711); err != nil {
		return nil, fmt.Errorf("guestdisk: create scratch root: %w", err)
	}
	scratch, err := os.MkdirTemp(e.cfg.ScratchRoot, "s-")
	if err != nil {
		return nil, fmt.Errorf("guestdisk: create scratch: %w", err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(scratch, uid, gid); err != nil {
			_ = os.RemoveAll(scratch)
			return nil, fmt.Errorf("guestdisk: hand scratch to %d:%d: %w", uid, gid, err)
		}
	}
	var n [8]byte
	if _, err := rand.Read(n[:]); err != nil {
		_ = os.RemoveAll(scratch)
		return nil, fmt.Errorf("guestdisk: nonce: %w", err)
	}
	return &Session{ed: e, image: image, size: info.Size(), uid: uid, gid: gid, scratch: scratch, nonce: hex.EncodeToString(n[:])}, nil
}

// Close removes the session's scratch directory. The sandboxed tools have
// exited by now, so nothing else writes there.
func (s *Session) Close() error {
	if s.scratch == "" {
		return nil
	}
	err := os.RemoveAll(s.scratch)
	s.scratch = ""
	return err
}

// Prepare replays the image's journal when the superblock says it needs
// recovery, which is the state a guest leaves a drive it had mounted. debugfs
// writes straight to the filesystem and ignores the journal, so editing
// without the replay would let a later replay overwrite the edits with stale
// blocks (the reason ADR-192 kept the kernel mount). It reports whether a
// replay ran.
func (s *Session) Prepare(ctx context.Context) (bool, error) {
	sb, err := readSuperblock(s.image)
	if err != nil {
		return false, err
	}
	if sb.incompat&incompatJournalDev != 0 {
		return false, errors.New("guestdisk: image is an external journal device")
	}
	if sb.incompat&incompatRecover == 0 {
		return false, nil
	}
	if sb.compat&compatHasJournal == 0 {
		return false, errors.New("guestdisk: image needs journal recovery but has no journal")
	}
	res, err := s.run(ctx, s.ed.e2fsck, []string{"-E", "journal_only", "-p", s.image}, nil, runLimits{output: 64 << 10})
	if err != nil {
		return true, fmt.Errorf("guestdisk: replay journal: %w", err)
	}
	// 0: nothing changed; 1: the replay changed the filesystem.
	if res.exit != 0 && res.exit != 1 {
		return true, fmt.Errorf("guestdisk: replay journal: e2fsck exit %d: %s", res.exit, firstLines(res.stderr, 4))
	}
	sb, err = readSuperblock(s.image)
	if err != nil {
		return true, err
	}
	if sb.incompat&incompatRecover != 0 {
		return true, errors.New("guestdisk: image still needs journal recovery after e2fsck")
	}
	return true, nil
}

// ext4 superblock fields read directly: fixed offsets in the 1024 bytes at
// byte 1024, so parsing them is not the hazard this package avoids.
const (
	superblockOffset   = 1024
	superblockMagic    = 0xEF53
	compatHasJournal   = 0x4
	incompatRecover    = 0x4
	incompatJournalDev = 0x8
)

type superblock struct {
	compat, incompat uint32
}

func readSuperblock(image string) (superblock, error) {
	f, err := os.Open(image)
	if err != nil {
		return superblock{}, fmt.Errorf("guestdisk: open image: %w", err)
	}
	defer func() { _ = f.Close() }()
	var buf [1024]byte
	if _, err := f.ReadAt(buf[:], superblockOffset); err != nil {
		return superblock{}, fmt.Errorf("guestdisk: read superblock: %w", err)
	}
	if binary.LittleEndian.Uint16(buf[0x38:]) != superblockMagic {
		return superblock{}, errors.New("guestdisk: image is not ext2/3/4 (bad superblock magic)")
	}
	return superblock{compat: binary.LittleEndian.Uint32(buf[0x5C:]), incompat: binary.LittleEndian.Uint32(buf[0x60:])}, nil
}

// Paths inside an image are absolute, clean and made of conservative
// components, so they need no quoting in a debugfs script.
var componentRE = regexp.MustCompile(`^[A-Za-z0-9._@+-]+$`)

const (
	maxPathBytes      = 1024
	maxPathComponents = 32
)

// CheckPath validates an image path.
func CheckPath(p string) error {
	if len(p) > maxPathBytes || !strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "/" {
		return fmt.Errorf("guestdisk: invalid image path %q", p)
	}
	parts := strings.Split(p[1:], "/")
	if len(parts) > maxPathComponents {
		return fmt.Errorf("guestdisk: image path %q is too deep", p)
	}
	for _, part := range parts {
		if part == "." || part == ".." || !componentRE.MatchString(part) {
			return fmt.Errorf("guestdisk: invalid component %q in image path %q", part, p)
		}
	}
	return nil
}

// components returns p's ancestors and p, shallowest first.
func components(p string) []string {
	var out []string
	for i := 1; i < len(p); i++ {
		if p[i] == '/' {
			out = append(out, p[:i])
		}
	}
	return append(out, p)
}

func firstLines(b []byte, n int) string {
	lines := strings.SplitN(strings.TrimSpace(string(b)), "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "; ")
}

type runLimits struct {
	// output caps captured stdout; a tool that prints more is killed.
	output int
	// fsize caps any file the tool writes; zero uses the image size.
	fsize int64
	// readDirs and writeDirs are scratch directories the tool may use.
	readDirs, writeDirs []string
}

type runResult struct {
	stdout, stderr []byte
	exit           int
}

// run executes one e2fsprogs tool against the image, sandboxed when a helper
// is configured.
func (s *Session) run(ctx context.Context, tool string, args []string, stdin []byte, lim runLimits) (runResult, error) {
	s.Runs++
	if lim.fsize == 0 {
		lim.fsize = s.size + (1 << 20)
	}
	var cmd *exec.Cmd
	if s.ed.cfg.Helper == "" {
		cmd = exec.CommandContext(ctx, tool, args...)
	} else {
		helperArgs := []string{
			"--uid", fmt.Sprint(s.uid), "--gid", fmt.Sprint(s.gid),
			"--image", s.image, "--fsize", fmt.Sprint(lim.fsize),
		}
		if s.ed.cfg.RequireLandlock {
			helperArgs = append(helperArgs, "--require-landlock")
		}
		for _, d := range lim.readDirs {
			helperArgs = append(helperArgs, "--read", d)
		}
		for _, d := range lim.writeDirs {
			helperArgs = append(helperArgs, "--write", d)
		}
		helperArgs = append(helperArgs, "--", tool)
		cmd = exec.CommandContext(ctx, s.ed.cfg.Helper, append(helperArgs, args...)...)
		cmd.SysProcAttr = sandboxSysProcAttr()
	}
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	cmd.Dir = "/"
	cmd.WaitDelay = 2 * time.Second
	if stdin != nil {
		cmd.Stdin = strings.NewReader(string(stdin))
	}
	stdout := &cappedBuffer{limit: lim.output}
	stderr := &cappedBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	res := runResult{stdout: stdout.buf, stderr: stderr.buf}
	if stdout.overflow {
		return res, fmt.Errorf("%s printed more than %d bytes", filepath.Base(tool), lim.output)
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr) && exitErr.Exited():
		res.exit = exitErr.ExitCode()
	case ctx.Err() != nil:
		return res, fmt.Errorf("%s: %w", filepath.Base(tool), ctx.Err())
	default:
		return res, fmt.Errorf("%s: %w: %s", filepath.Base(tool), err, firstLines(res.stderr, 4))
	}
	if s.ed.cfg.Helper != "" && res.exit == helperFailureExit {
		return res, fmt.Errorf("sandbox: %s", firstLines(res.stderr, 4))
	}
	return res, nil
}

// cappedBuffer keeps at most limit bytes and records an overflow.
type cappedBuffer struct {
	buf      []byte
	limit    int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.overflow {
		return 0, io.ErrShortWrite
	}
	if len(b.buf)+len(p) > b.limit {
		b.overflow = true
		return 0, io.ErrShortWrite
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// helperFailureExit is the exit code vmmd-guestdisk uses when it cannot set
// up the sandbox, so a tool's own exit codes stay distinguishable.
const helperFailureExit = 125
