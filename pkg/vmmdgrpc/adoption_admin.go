package vmmdgrpc

// vmmd's admin socket (vmmd restart adoption ADR §4): a unix socket that
// serves AdoptionAdmin to processes running as vmmd's own uid (root in
// production), for the release's adoption-check and adoption-report
// commands. The socket file is mode 0600, and every connection's peer
// credentials are checked too, which also closes the window between bind
// and chmod.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// AdoptionAdminSocket is where vmmd serves AdoptionAdmin. It is a file in
// vmmd's RuntimeDirectory, not a directory, so leakcheck's bridge-directory
// check does not count it.
var AdoptionAdminSocket = filepath.Join(bridgeRunRoot, "vmmd-admin.sock")

// ListenAdoptionAdmin binds the admin socket at path, replacing a stale one.
func ListenAdoptionAdmin(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o711); err != nil {
		return nil, fmt.Errorf("admin socket dir: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale admin socket: %w", err)
	}
	lis, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = lis.Close()
		return nil, fmt.Errorf("admin socket mode: %w", err)
	}
	return &ownerOnlyListener{Listener: lis, uid: os.Geteuid()}, nil
}

// ownerOnlyListener drops connections from any uid but vmmd's own.
type ownerOnlyListener struct {
	net.Listener
	uid int
}

func (l *ownerOnlyListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if runtime.GOOS != "linux" {
			return c, nil // Peer credentials are Linux-only; vmmd runs there.
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			_ = c.Close()
			continue
		}
		if uid, err := unixPeerUID(uc); err != nil || uid != l.uid {
			_ = c.Close()
			continue
		}
		return c, nil
	}
}
