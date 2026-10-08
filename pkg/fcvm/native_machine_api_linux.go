//go:build linux

package fcvm

// adr: 768
import (
	"context"
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

func dialNativeMachine(ctx context.Context, path string, owner nativeLaunchRecord) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := checkNativeMachinePeer(conn, owner); err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return conn, nil
}

func checkNativeMachinePeer(conn net.Conn, owner nativeLaunchRecord) error {
	local, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("native machine: API connection is not a Unix socket")
	}
	raw, err := local.SyscallConn()
	if err != nil {
		return err
	}
	var credentials *unix.Ucred
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, credentialErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if credentialErr != nil {
		return credentialErr
	}
	if credentials == nil || int(credentials.Pid) != owner.PID || int(credentials.Uid) != owner.Lease.UID || int(credentials.Gid) != owner.Lease.GID {
		return errors.New("native machine: API peer differs from original task and jail credentials")
	}
	return nil
}
