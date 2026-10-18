// Socket activation for every daemon listener (sd_listen_fds(3)).
//
// A systemd socket unit owns the listening socket, so the kernel socket stays
// open while the service restarts. Connections that arrive between the old
// process closing its copy and the replacement accepting wait in the backlog
// instead of being refused, and a unix socket path is never unlinked. The old
// process drains only the connections it already accepted.
//
// apid and gatewayd-public keep their single-descriptor helpers (ADR-068).
// Daemons with several listeners claim inherited sockets by address here, so
// a socket unit can carry any subset of them and the daemon binds the rest
// itself. A daemon that adopts this calls RequireInheritedListenersClaimed
// once its listeners are bound: a socket unit that passes an address the
// daemon never serves would otherwise accept and hold connections forever.
// See docs/adr/2026101801-zero-downtime-daemon-restarts.md.

package wire

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// activationFirstFD is SD_LISTEN_FDS_START.
const activationFirstFD = 3

// inheritedSocket is one listening socket systemd passed to this process.
type inheritedSocket struct {
	fd      int
	name    string // LISTEN_FDNAMES entry; "unknown" when systemd sent none
	network string // "unix" or "tcp"
	addr    string // socket path, or the TCP address getsockname reported
	claimed bool
}

func (s *inheritedSocket) String() string {
	return fmt.Sprintf("%s %s (fd %d, name %q)", s.network, s.addr, s.fd, s.name)
}

type activationSet struct {
	mu      sync.Mutex
	sockets []*inheritedSocket
	err     error
}

var (
	activationOnce sync.Once
	activation     *activationSet
)

func processActivation() *activationSet {
	activationOnce.Do(func() {
		activation = loadActivation(os.LookupEnv, os.Getpid(), inspectInheritedFD)
	})
	return activation
}

// loadActivation reads the activation environment. It does not unset it:
// apid and gatewayd-public consume the same variables with their own helpers,
// and a child process ignores them because LISTEN_PID names this process.
func loadActivation(lookup func(string) (string, bool), pid int, inspect func(fd int) (network, addr string, err error)) *activationSet {
	set := &activationSet{}
	pidValue, hasPID := lookup("LISTEN_PID")
	fdsValue, hasFDs := lookup("LISTEN_FDS")
	if !hasPID && !hasFDs {
		return set
	}
	if !hasPID || !hasFDs {
		set.err = errors.New("wire: incomplete systemd socket activation environment")
		return set
	}
	if strings.TrimSpace(pidValue) != strconv.Itoa(pid) {
		// The descriptors belong to an ancestor (sd_listen_fds(3)).
		return set
	}
	count, err := strconv.Atoi(strings.TrimSpace(fdsValue))
	if err != nil || count < 0 {
		set.err = fmt.Errorf("wire: invalid LISTEN_FDS=%q", fdsValue)
		return set
	}
	var names []string
	if value, ok := lookup("LISTEN_FDNAMES"); ok && value != "" {
		names = strings.Split(value, ":")
	}
	fds := make([]int, count)
	for i := range fds {
		fds[i] = activationFirstFD + i
	}
	return inheritedSet(fds, names, inspect)
}

// inheritedSet describes the given inherited descriptors.
func inheritedSet(fds []int, names []string, inspect func(fd int) (network, addr string, err error)) *activationSet {
	set := &activationSet{}
	for i, fd := range fds {
		network, addr, err := inspect(fd)
		if err != nil {
			set.err = fmt.Errorf("wire: inspect inherited fd %d: %w", fd, err)
			return set
		}
		name := "unknown"
		if i < len(names) && names[i] != "" {
			name = names[i]
		}
		set.sockets = append(set.sockets, &inheritedSocket{fd: fd, name: name, network: network, addr: addr})
	}
	return set
}

// take returns the inherited socket serving network/address and marks it
// claimed. A second claim of the same socket is an error: two servers on one
// accept queue would split its connections.
func (s *activationSet) take(network, address string) (*inheritedSocket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	for _, sock := range s.sockets {
		if !inheritedMatches(sock, network, address) {
			continue
		}
		if sock.claimed {
			return nil, fmt.Errorf("wire: inherited %s already claimed", sock)
		}
		sock.claimed = true
		return sock, nil
	}
	return nil, nil
}

func inheritedMatches(sock *inheritedSocket, network, address string) bool {
	switch network {
	case "unix":
		return sock.network == "unix" && filepath.Clean(sock.addr) == filepath.Clean(address)
	case "tcp", "tcp4", "tcp6":
		if sock.network != "tcp" {
			return false
		}
		got, err := net.ResolveTCPAddr("tcp", sock.addr)
		if err != nil {
			return false
		}
		want, err := net.ResolveTCPAddr(network, address)
		if err != nil || got.Port != want.Port {
			return false
		}
		// The socket unit may be narrower than a wildcard configuration but
		// never wider than a concrete one (the apid activation rule).
		wantWildcard := len(want.IP) == 0 || want.IP.IsUnspecified()
		return wantWildcard || got.IP.Equal(want.IP)
	}
	return false
}

// InheritedListener returns the listening socket a systemd socket unit passed
// for network ("unix" or "tcp") and address, or ok=false when it passed none.
// The caller owns the listener. Closing it neither closes systemd's copy nor
// unlinks a unix socket path, so the replacement process inherits the same
// socket. A unix socket that grants access to other users is refused: socket
// permissions are the authentication boundary (ADR-015).
func InheritedListener(network, address string) (net.Listener, bool, error) {
	return processActivation().listener(network, address)
}

func (s *activationSet) listener(network, address string) (net.Listener, bool, error) {
	sock, err := s.take(network, address)
	if err != nil || sock == nil {
		return nil, false, err
	}
	if sock.network == "unix" {
		info, err := os.Stat(sock.addr)
		if err != nil {
			return nil, false, fmt.Errorf("wire: inherited unix socket %s: %w", sock.addr, err)
		}
		if info.Mode().Perm()&0o007 != 0 {
			return nil, false, fmt.Errorf("wire: inherited unix socket %s has mode %o; the socket unit must set SocketMode=0660", sock.addr, info.Mode().Perm())
		}
	}
	file := os.NewFile(uintptr(sock.fd), sock.name)
	if file == nil {
		return nil, false, fmt.Errorf("wire: inherited fd %d is unavailable", sock.fd)
	}
	l, err := net.FileListener(file)
	_ = file.Close()
	if err != nil {
		return nil, false, fmt.Errorf("wire: consume inherited %s: %w", sock, err)
	}
	return l, true, nil
}

// UnclaimedInheritedListeners describes inherited sockets no listener has
// claimed yet, in descriptor order.
func UnclaimedInheritedListeners() []string {
	return processActivation().unclaimed()
}

func (s *activationSet) unclaimed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, sock := range s.sockets {
		if !sock.claimed {
			out = append(out, sock.String())
		}
	}
	sort.Strings(out)
	return out
}

// RequireInheritedListenersClaimed fails when a socket unit passed a socket
// this daemon did not bind. Call it after every listener is open. Without it
// a mismatched ListenStream= would hold callers' connections in a backlog no
// process ever accepts from.
func RequireInheritedListenersClaimed(daemon string) error {
	return processActivation().requireClaimed(daemon)
}

func (s *activationSet) requireClaimed(daemon string) error {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if left := s.unclaimed(); len(left) > 0 {
		return fmt.Errorf("%s: socket unit passed listeners this daemon does not serve: %s", daemon, strings.Join(left, ", "))
	}
	return nil
}

// ListenInherited is net.Listen preferring an inherited socket.
func ListenInherited(network, address string) (net.Listener, error) {
	if l, ok, err := InheritedListener(network, address); err != nil {
		return nil, err
	} else if ok {
		return l, nil
	}
	return net.Listen(network, address)
}
