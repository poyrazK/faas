package vmmdgrpc

// Unprivileged bridges (connection-survival ADR, slice 1a).
//
// Every bridge parses bytes the guest controls. Before this slice each one
// ran as root with vmmd's capability bounding set. With
// FAAS_BRIDGE_UNPRIVILEGED=1, vmmd starts each instance's bridges as that
// instance's own bridge uid (fcvm.BridgeUIDForJailUID): nsenter enters the
// network namespace as root, then drops every uid, gid and supplementary
// group before it execs the bridge, which therefore starts with no
// capability. The bridge re-checks this itself (pkg/bridgeproc) and exits if
// the drop did not happen. vmmd's NoNewPrivileges is inherited, so the bridge
// cannot regain privilege through a setuid binary.
//
// The HTTP bridge binds a unix socket. As a bridge uid it cannot write
// /run/faas/stream, so its socket lives in a directory of its own under
// bridgeRunRoot, owned by that bridge uid. bridgeRunRoot is vmmd's systemd
// RuntimeDirectory under /run: unlike /run/faas, which group faas can write,
// no other daemon can rename or replace it, so vmmd's mkdir, chown and
// RemoveAll there cannot be redirected. vmmd accepts a connection on a bridge
// socket only from a peer running as the expected bridge uid: the directory
// belongs to the bridge, so its contents are not trusted.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/bridgeproc"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

const (
	bridgeUnprivilegedEnv   = "FAAS_BRIDGE_UNPRIVILEGED"
	bridgeUnprivilegedValue = "1"
	// bridgeEnvPATH is the only PATH a bridge sees. Bridges exec nothing; it
	// is set so a bridge never inherits vmmd's.
	bridgeEnvPATH = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	// bridgeSocketName is the HTTP bridge's socket inside its directory.
	bridgeSocketName = "http.sock"
)

// bridgeRunRoot holds one directory per unprivileged bridge socket. Mode 0711
// lets a bridge uid reach its own directory without listing the others.
// systemd creates it (RuntimeDirectory=faas-ibridge in faas-vmmd.service).
// A variable so tests can use a temporary directory.
var bridgeRunRoot = "/run/faas-ibridge"

// chownBridgeDir hands a bridge's socket directory to its uid. A variable so
// tests, which cannot chown, can replace it.
var chownBridgeDir = os.Lchown

// bridgeIdentity is the credential an instance's bridges run with. The zero
// value is the legacy root bridge.
type bridgeIdentity struct {
	uid, gid int
}

func (b bridgeIdentity) unprivileged() bool { return b.uid != 0 }

// bridgeCredentialSource is implemented by fcvm.Manager. It is not part of
// VmmdAPI so test fakes that never start a real bridge need not implement it.
type bridgeCredentialSource interface {
	BridgeCredential(instance string) (uid, gid int, ok bool)
}

var _ bridgeCredentialSource = (*fcvm.Manager)(nil)

func bridgeUnprivilegedEnabled() bool {
	return os.Getenv(bridgeUnprivilegedEnv) == bridgeUnprivilegedValue
}

// bridgeIdentityFor returns the identity for a live instance's bridges.
func (s *Server) bridgeIdentityFor(instance string) (bridgeIdentity, error) {
	if !bridgeUnprivilegedEnabled() {
		return bridgeIdentity{}, nil
	}
	src, ok := s.vmm.(bridgeCredentialSource)
	if !ok {
		return bridgeIdentity{}, status.Error(codes.FailedPrecondition, "unprivileged bridges need a VMM that reports bridge credentials")
	}
	uid, gid, ok := src.BridgeCredential(instance)
	if !ok {
		return bridgeIdentity{}, status.Errorf(codes.NotFound, "instance %q not live", instance)
	}
	id, err := newBridgeIdentity(uid, gid)
	if err != nil {
		return bridgeIdentity{}, status.Errorf(codes.Internal, "bridge credential for %q: %v", instance, err)
	}
	return id, nil
}

// forwardBridgeIdentity is bridgeIdentityFor for a forward whose route
// publicForwardContext resolved. A public-target bridge (ADR-1749) runs in
// the host network namespace and dials through the node's interface, which
// needs privilege, so it keeps the legacy root identity: its socket path,
// environment and peer check stay consistent with that.
func (s *Server) forwardBridgeIdentity(ctx context.Context, instance string) (bridgeIdentity, error) {
	if publicTarget(ctx) != nil {
		return bridgeIdentity{}, nil
	}
	return s.bridgeIdentityFor(instance)
}

// bridgeIdentityForJailUID returns the identity for an instance that is not
// live yet: the bridge prewarm runs while the instance is still waking.
func bridgeIdentityForJailUID(jailUID int) (bridgeIdentity, error) {
	if !bridgeUnprivilegedEnabled() {
		return bridgeIdentity{}, nil
	}
	uid, ok := fcvm.BridgeUIDForJailUID(jailUID)
	if !ok {
		return bridgeIdentity{}, fmt.Errorf("jail uid %d is outside the jail range", jailUID)
	}
	return newBridgeIdentity(uid, uid)
}

func newBridgeIdentity(uid, gid int) (bridgeIdentity, error) {
	if uid < fcvm.BridgeUIDBase || uid > fcvm.BridgeUIDMax || gid != uid {
		return bridgeIdentity{}, fmt.Errorf("bridge uid %d gid %d is outside the bridge range %d-%d", uid, gid, fcvm.BridgeUIDBase, fcvm.BridgeUIDMax)
	}
	return bridgeIdentity{uid: uid, gid: gid}, nil
}

// nsenterArgs is the nsenter invocation up to and including "--". target
// selects the network namespace: "--net=<path>", or "--target <pid> --net".
// For an unprivileged identity nsenter drops to it after entering.
func (b bridgeIdentity) nsenterArgs(target ...string) []string {
	args := append([]string{"nsenter"}, target...)
	if b.unprivileged() {
		args = append(args, "--setuid", strconv.Itoa(b.uid), "--setgid", strconv.Itoa(b.gid))
	}
	return append(args, "--")
}

// env is a bridge's whole environment. It never includes vmmd's own, which
// carries database and storage credentials. An unprivileged bridge is told
// which uid to verify and which parent to die with: its uid change cleared
// the parent-death signal. A root bridge keeps the one set at fork, and is
// not given a parent pid because `ip netns exec` may run it from a child.
func (b bridgeIdentity) env(extra ...string) []string {
	env := []string{bridgeEnvPATH}
	if b.unprivileged() {
		env = append(env,
			bridgeproc.ExpectUIDEnv+"="+strconv.Itoa(b.uid),
			bridgeproc.ParentPIDEnv+"="+strconv.Itoa(os.Getpid()))
	}
	return append(env, extra...)
}

// streamBridgeSocket returns the HTTP bridge socket path. name is the
// instance for the persistent bridge, or a per-request name.
func (b bridgeIdentity) streamBridgeSocket(name string) string {
	if !b.unprivileged() {
		return "/var/run/faas/stream/" + name + ".sock"
	}
	return filepath.Join(bridgeRunRoot, name, bridgeSocketName)
}

// prepareSocketDir creates an empty socket directory owned by the bridge uid.
// A legacy root bridge binds directly in /run/faas/stream and needs nothing.
func (b bridgeIdentity) prepareSocketDir(socket string) error {
	if !b.unprivileged() {
		return nil
	}
	if err := ensureBridgeRunRoot(); err != nil {
		return err
	}
	dir := filepath.Dir(socket)
	// The directory belonged to a bridge uid; RemoveAll does not follow the
	// symlinks a bridge could have left in it.
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove stale bridge socket directory: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return fmt.Errorf("create bridge socket directory: %w", err)
	}
	if err := chownBridgeDir(dir, b.uid, b.gid); err != nil {
		_ = os.Remove(dir)
		return fmt.Errorf("hand bridge socket directory to uid %d: %w", b.uid, err)
	}
	return nil
}

// removeSocket deletes the bridge socket, and its directory when it has one.
func (b bridgeIdentity) removeSocket(socket string) {
	if b.unprivileged() {
		_ = os.RemoveAll(filepath.Dir(socket))
		return
	}
	_ = os.Remove(socket)
}

// ensureBridgeRunRoot checks the root before vmmd creates, chowns or removes
// anything in it: a directory, not a symlink, owned by vmmd's own uid.
func ensureBridgeRunRoot() error {
	if err := os.MkdirAll(bridgeRunRoot, 0o711); err != nil {
		return fmt.Errorf("create bridge run root: %w", err)
	}
	info, err := os.Lstat(bridgeRunRoot)
	if err != nil {
		return fmt.Errorf("inspect bridge run root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("bridge run root %s is not a directory", bridgeRunRoot)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("bridge run root %s is not owned by uid %d", bridgeRunRoot, os.Geteuid())
	}
	if info.Mode().Perm() != 0o711 {
		if err := os.Chmod(bridgeRunRoot, 0o711); err != nil {
			return fmt.Errorf("set bridge run root mode: %w", err)
		}
	}
	return nil
}

// verifyBridgePeer rejects a bridge socket connection whose peer is not the
// expected bridge uid. A legacy root bridge's socket lives in a root-owned
// directory, so its peer needs no check.
func (b bridgeIdentity) verifyBridgePeer(conn net.Conn) error {
	if !b.unprivileged() {
		return nil
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("bridge connection is not a unix socket")
	}
	uid, err := unixPeerUID(unixConn)
	if err != nil {
		return fmt.Errorf("read bridge peer credentials: %w", err)
	}
	if uid != b.uid {
		return fmt.Errorf("bridge socket peer uid %d, want %d", uid, b.uid)
	}
	return nil
}
