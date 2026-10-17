// Inherited listening sockets (sd_listen_fds(3) and daemon hand-offs).
//
// A systemd socket unit owns the listening socket, so the kernel socket stays
// open while the service restarts. Connections that arrive between the old
// process closing its copy and the replacement accepting wait in the backlog
// instead of being refused, and a unix socket path is never unlinked. The old
// process drains only the connections it already accepted.
//
// A hand-off (handoff.go) passes every listening socket a daemon opened to its
// successor the same way, so both generations accept from one queue while the
// old one finishes its long requests.
//
// apid and gatewayd-public keep their single-descriptor helpers (ADR-068) for
// systemd's socket. Daemons claim inherited sockets by address here, so a
// socket unit can carry any subset of them and the daemon binds the rest
// itself. A daemon that adopts this calls RequireInheritedListenersClaimed
// once its listeners are bound: a socket unit that passes an address the
// daemon never serves would otherwise accept and hold connections forever.
// See docs/adr/1580-zero-downtime-daemon-restarts.md.

package wire

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// activationFirstFD is SD_LISTEN_FDS_START.
const activationFirstFD = 3

const (
	sourceSystemd = "systemd"
	sourceHandoff = "handoff"
)

// inheritedSocket is one listening socket passed to this process.
type inheritedSocket struct {
	fd      int
	name    string // LISTEN_FDNAMES entry; "unknown" when none was sent
	network string // "unix", "tcp" or "udp"
	addr    string // socket path, or the address getsockname reported
	source  string // sourceSystemd or sourceHandoff
	claimed bool
	closed  bool
}

func (s *inheritedSocket) String() string {
	return fmt.Sprintf("%s %s (fd %d, name %q)", s.network, s.addr, s.fd, s.name)
}

type activationSet struct {
	mu      sync.Mutex
	sockets []*inheritedSocket
	err     error
	// shared are the listeners and packet conns this process serves, which
	// a hand-off passes to the successor.
	shared []any
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
// and a child process ignores them because LISTEN_PID names this process. A
// hand-off successor has no LISTEN_* variables; its predecessor describes the
// passed sockets in handoffFDsEnv instead (the successor's PID is unknown
// before exec, so LISTEN_PID cannot name it).
func loadActivation(lookup func(string) (string, bool), pid int, inspect func(fd int) (network, addr string, err error)) *activationSet {
	set := &activationSet{}
	pidValue, hasPID := lookup("LISTEN_PID")
	fdsValue, hasFDs := lookup("LISTEN_FDS")
	if !hasPID && !hasFDs {
		return loadHandoff(lookup, inspect)
	}
	if !hasPID || !hasFDs {
		set.err = errors.New("wire: incomplete systemd socket activation environment")
		return set
	}
	if strings.TrimSpace(pidValue) != strconv.Itoa(pid) {
		// The descriptors belong to an ancestor (sd_listen_fds(3)).
		return loadHandoff(lookup, inspect)
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
	return inheritedSet(firstFDs(count), names, sourceSystemd, inspect)
}

func loadHandoff(lookup func(string) (string, bool), inspect func(fd int) (network, addr string, err error)) *activationSet {
	value, ok := lookup(handoffFDsEnv)
	if !ok {
		return &activationSet{}
	}
	count, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || count < 0 {
		return &activationSet{err: fmt.Errorf("wire: invalid %s=%q", handoffFDsEnv, value)}
	}
	return inheritedSet(firstFDs(count), nil, sourceHandoff, inspect)
}

func firstFDs(count int) []int {
	fds := make([]int, count)
	for i := range fds {
		fds[i] = activationFirstFD + i
	}
	return fds
}

// inheritedSet describes the given inherited descriptors.
func inheritedSet(fds []int, names []string, source string, inspect func(fd int) (network, addr string, err error)) *activationSet {
	set := &activationSet{}
	for i, fd := range fds {
		network, addr, err := inspect(fd)
		if err != nil {
			set.err = fmt.Errorf("wire: inspect inherited fd %d: %w", fd, err)
			return set
		}
		name := source
		if i < len(names) && names[i] != "" {
			name = names[i]
		} else if source == sourceSystemd {
			name = "unknown"
		}
		set.sockets = append(set.sockets, &inheritedSocket{fd: fd, name: name, network: network, addr: addr, source: source})
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
		if sock.closed || !inheritedMatches(sock, network, address) {
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
		return sock.network == "tcp" && sameIPPort(sock.addr, network, address)
	case "udp", "udp4", "udp6":
		return sock.network == "udp" && sameIPPort(sock.addr, network, address)
	}
	return false
}

func sameIPPort(have, network, address string) bool {
	host, port, err := net.SplitHostPort(have)
	if err != nil {
		return false
	}
	wantHost, wantPort, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	gotPort, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	resolved, err := net.LookupPort(strings.TrimRight(network, "46"), wantPort)
	if err != nil || resolved != gotPort {
		return false
	}
	// The socket unit may be narrower than a wildcard configuration but
	// never wider than a concrete one (the apid activation rule).
	if wantHost == "" {
		return true
	}
	want := net.ParseIP(wantHost)
	if want == nil {
		addrs, err := net.LookupIP(wantHost)
		if err != nil || len(addrs) == 0 {
			return false
		}
		want = addrs[0]
	}
	return want.IsUnspecified() || net.ParseIP(host).Equal(want)
}

// InheritedListener returns the listening socket a systemd socket unit or a
// predecessor passed for network ("unix" or "tcp") and address, or ok=false
// when none was passed. The caller owns the listener. Closing it neither
// closes the other copies nor unlinks a unix socket path, so a replacement
// inherits the same socket. A unix socket that grants access to other users
// is refused: socket permissions are the authentication boundary (ADR-015).
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
	s.share(l)
	return l, true, nil
}

// InheritedPacketConn is InheritedListener for a datagram socket ("udp").
func InheritedPacketConn(network, address string) (net.PacketConn, bool, error) {
	set := processActivation()
	sock, err := set.take(network, address)
	if err != nil || sock == nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(sock.fd), sock.name)
	if file == nil {
		return nil, false, fmt.Errorf("wire: inherited fd %d is unavailable", sock.fd)
	}
	pc, err := net.FilePacketConn(file)
	_ = file.Close()
	if err != nil {
		return nil, false, fmt.Errorf("wire: consume inherited %s: %w", sock, err)
	}
	set.share(pc)
	return pc, true, nil
}

// share records a listener or packet conn for a hand-off.
func (s *activationSet) share(x any) {
	s.mu.Lock()
	s.shared = append(s.shared, x)
	s.mu.Unlock()
}

// Share records a listener or packet conn opened outside this package, so a
// hand-off passes it to the successor.
func Share(x any) {
	processActivation().share(x)
}

// UnclaimedInheritedListeners describes inherited sockets no listener has
// claimed yet, in descriptor order.
func UnclaimedInheritedListeners() []string {
	return processActivation().unclaimed("")
}

func (s *activationSet) unclaimed(source string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, sock := range s.sockets {
		if !sock.claimed && !sock.closed && (source == "" || sock.source == source) {
			out = append(out, sock.String())
		}
	}
	sort.Strings(out)
	return out
}

// RequireInheritedListenersClaimed fails when a socket unit passed a socket
// this daemon did not bind. Call it after every listener is open. Without it
// a mismatched ListenStream= would hold callers' connections in a backlog no
// process ever accepts from. Sockets a predecessor passed are exempt: a new
// release may stop serving an address, and ReleaseUnclaimedHandoff closes
// them.
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
	if left := s.unclaimed(sourceSystemd); len(left) > 0 {
		return fmt.Errorf("%s: socket unit passed listeners this daemon does not serve: %s", daemon, strings.Join(left, ", "))
	}
	return nil
}

// ReleaseUnclaimedHandoff closes the sockets a predecessor passed that this
// process does not serve, and returns their descriptions.
func ReleaseUnclaimedHandoff() []string {
	set := processActivation()
	set.mu.Lock()
	defer set.mu.Unlock()
	var released []string
	for _, sock := range set.sockets {
		if sock.source == sourceHandoff && !sock.claimed && !sock.closed {
			if file := os.NewFile(uintptr(sock.fd), sock.name); file != nil {
				_ = file.Close()
			}
			sock.closed = true
			released = append(released, sock.String())
		}
	}
	return released
}

// ListenInherited is net.Listen preferring an inherited socket.
func ListenInherited(network, address string) (net.Listener, error) {
	if l, ok, err := InheritedListener(network, address); err != nil {
		return nil, err
	} else if ok {
		return l, nil
	}
	l, err := net.Listen(network, address)
	if err == nil {
		Share(l)
	}
	return l, err
}

// ListenPacketInherited is net.ListenPacket preferring an inherited socket.
func ListenPacketInherited(network, address string) (net.PacketConn, error) {
	if pc, ok, err := InheritedPacketConn(network, address); err != nil {
		return nil, err
	} else if ok {
		return pc, nil
	}
	pc, err := net.ListenPacket(network, address)
	if err == nil {
		Share(pc)
	}
	return pc, err
}

// NotifyReadyWhen is daemonunit.NotifyReadyWhen that, once ready, also
// closes sockets a predecessor passed but this release does not serve, so
// their callers are refused instead of queued forever.
func NotifyReadyWhen(ctx context.Context, ready func() bool) func() {
	var once sync.Once
	return daemonunit.NotifyReadyWhen(ctx, func() bool {
		if !ready() {
			return false
		}
		once.Do(func() { ReleaseUnclaimedHandoff() })
		return true
	})
}

// ListenUDPInherited is net.ListenUDP preferring an inherited socket.
func ListenUDPInherited(network string, laddr *net.UDPAddr) (*net.UDPConn, error) {
	pc, ok, err := InheritedPacketConn(network, laddr.String())
	if err != nil {
		return nil, err
	}
	if ok {
		if conn, isUDP := pc.(*net.UDPConn); isUDP {
			return conn, nil
		}
		_ = pc.Close()
		return nil, fmt.Errorf("wire: inherited %s socket for %s is not UDP", network, laddr)
	}
	conn, err := net.ListenUDP(network, laddr)
	if err == nil {
		Share(conn)
	}
	return conn, err
}
