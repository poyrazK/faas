package daemonunit

// systemd's file descriptor store. A daemon hands systemd listening sockets
// that must outlive it (sd_notify FDSTORE=1 with the descriptor attached),
// and its successor gets them back through LISTEN_FDS and LISTEN_FDNAMES.
// systemd keeps the store across `systemctl restart` and crash restarts
// (vmmd restart adoption ADR, V-A2); a stop discards it. The unit needs
// FileDescriptorStoreMax. Without NOTIFY_SOCKET (tests, local runs) storing
// is a no-op and nothing comes back.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// listenFDsStart is the first descriptor systemd passes (SD_LISTEN_FDS_START).
const listenFDsStart = 3

// maxFDNameLen is systemd's FDNAME limit.
const maxFDNameLen = 255

// FDStore stores listeners in systemd's file descriptor store and hands out
// the ones a predecessor stored.
type FDStore struct {
	socket string

	mu     sync.Mutex
	passed map[string]*net.UnixListener
}

// OpenFDStore takes the unix listeners systemd passed this process and
// clears the activation environment so children cannot mistake them for
// theirs. Every passed descriptor is made close-on-exec first: a jailer or
// Firecracker child must never inherit another guest's socket. A passed
// descriptor that is not a unix listener is closed and removed from the
// store.
func OpenFDStore() (*FDStore, error) {
	s := &FDStore{socket: os.Getenv("NOTIFY_SOCKET"), passed: make(map[string]*net.UnixListener)}
	names, err := listenFDNames(os.Getenv, os.Getpid())
	for k := range names {
		syscall.CloseOnExec(listenFDsStart + k)
	}
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_FDNAMES")
	var drop []string
	for k, name := range names {
		f := os.NewFile(uintptr(listenFDsStart+k), name)
		ln, lerr := net.FileListener(f)
		_ = f.Close()
		ul, ok := ln.(*net.UnixListener)
		_, dup := s.passed[name]
		switch {
		case lerr != nil:
			drop = append(drop, name)
		case !ok || dup:
			_ = ln.Close()
			drop = append(drop, name)
		default:
			s.passed[name] = ul
		}
	}
	for _, name := range drop {
		if _, kept := s.passed[name]; !kept {
			_ = s.Remove(name)
		}
	}
	return s, err
}

// listenFDNames parses the activation environment: one name per passed
// descriptor, in descriptor order. Descriptors advertised for another
// process are not this one's.
func listenFDNames(getenv func(string) string, pid int) ([]string, error) {
	rawPID, rawFDs := getenv("LISTEN_PID"), getenv("LISTEN_FDS")
	if rawPID == "" && rawFDs == "" {
		return nil, nil
	}
	if rawPID != strconv.Itoa(pid) {
		return nil, nil
	}
	n, err := strconv.Atoi(rawFDs)
	if err != nil || n < 0 || n > 1<<16 {
		return nil, fmt.Errorf("daemonunit: LISTEN_FDS=%q", rawFDs)
	}
	names := make([]string, n)
	if raw := getenv("LISTEN_FDNAMES"); raw != "" {
		split := strings.Split(raw, ":")
		if len(split) != n {
			return nil, fmt.Errorf("daemonunit: %d LISTEN_FDNAMES for %d descriptors", len(split), n)
		}
		copy(names, split)
	}
	for k := range names {
		if names[k] == "" {
			names[k] = "unknown"
		}
	}
	return names, nil
}

// Take returns, once, the listener a predecessor stored under name. It stays
// in systemd's store until Remove.
func (s *FDStore) Take(name string) *net.UnixListener {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ln := s.passed[name]
	delete(s.passed, name)
	return ln
}

// Untaken closes every passed listener nobody took and removes it from the
// store, returning their names.
func (s *FDStore) Untaken() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	passed := s.passed
	s.passed = make(map[string]*net.UnixListener)
	s.mu.Unlock()
	names := make([]string, 0, len(passed))
	for name, ln := range passed {
		_ = ln.Close()
		_ = s.Remove(name)
		names = append(names, name)
	}
	return names
}

// Store hands systemd a duplicate of ln's descriptor under name.
func (s *FDStore) Store(name string, ln *net.UnixListener) error {
	if err := validFDName(name); err != nil {
		return err
	}
	if s == nil || s.socket == "" {
		return nil
	}
	rc, err := ln.SyscallConn()
	if err != nil {
		return fmt.Errorf("daemonunit: fd store %s: %w", name, err)
	}
	var sendErr error
	if err := rc.Control(func(fd uintptr) {
		sendErr = s.send("FDSTORE=1\nFDNAME="+name, int(fd))
	}); err != nil {
		return fmt.Errorf("daemonunit: fd store %s: %w", name, err)
	}
	return sendErr
}

// Remove drops every descriptor stored under name.
func (s *FDStore) Remove(name string) error {
	if err := validFDName(name); err != nil {
		return err
	}
	if s == nil || s.socket == "" {
		return nil
	}
	return s.send("FDSTOREREMOVE=1\nFDNAME="+name, -1)
}

func (s *FDStore) send(state string, fd int) error {
	sock, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return fmt.Errorf("daemonunit: sd_notify socket: %w", err)
	}
	syscall.CloseOnExec(sock)
	defer func() { _ = syscall.Close(sock) }()
	var oob []byte
	if fd >= 0 {
		oob = syscall.UnixRights(fd)
	}
	// A leading '@' names an abstract socket; the syscall package maps it.
	if err := syscall.Sendmsg(sock, []byte(state), oob, &syscall.SockaddrUnix{Name: s.socket}, 0); err != nil {
		return fmt.Errorf("daemonunit: sd_notify %s: %w", strings.SplitN(state, "\n", 2)[0], err)
	}
	return nil
}

// validFDName applies systemd's FDNAME rules: printable ASCII without ':',
// which separates LISTEN_FDNAMES, at most 255 bytes.
func validFDName(name string) error {
	if name == "" || len(name) > maxFDNameLen {
		return errors.New("daemonunit: fd name length out of range")
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; c <= ' ' || c > '~' || c == ':' {
			return fmt.Errorf("daemonunit: fd name %q has a character systemd rejects", name)
		}
	}
	return nil
}
