// Unix-socket helpers shared by every daemon that listens on a path under
// /run/faas/. The auth model is mode 0660 + group `faas` (ADR-015); the
// v1.0 daemon-listening function chowns the socket to that group so that
// every other daemon in the same group can dial it. Gate-A multi-host
// replaces this with mTLS — see ADR-015 re-evaluation triggers.

package wire

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// SkipGroupLookupEnv is the env knob tests set to tolerate a missing
// `faas` group on a CI runner or dev Mac. The wire package falls back to
// gid=0 instead of failing the listener boot. Production deployments never
// set this — the ansible role creates the `faas` group as part of host
// bootstrap.
const SkipGroupLookupEnv = "FAAS_SKIP_SOCKET_GROUP"

// DefaultSocketGroup is the unix-socket group every shared socket is owned by
// in v1.0. Members of the group can dial the daemon. Spec §11: nothing
// privileged runs outside this group; cgroup-scope fencing enforces the
// per-tenant isolation that this group membership does NOT.
const DefaultSocketGroup = "faas"

// DefaultSocketMode is the permission applied to a fresh shared unix socket.
// The owner is the daemon process; the group is DefaultSocketGroup; world
// has no access. Callers can override via ListenOrRecreate.
const DefaultSocketMode os.FileMode = 0o660

// ListenOrRecreate removes any stale socket at path, makes the parent
// directory (mode 0750), and binds a new listener owned by uid/gid with the
// requested mode. The returned listener is ready to be passed to grpc.Server.
//
// Stale sockets (e.g. left over from a crash) are removed silently — a
// missing-but-bounded cross-daemon race is preferable to a startup failure
// when systemd restarts the service. Path must be an absolute path under
// /run/faas/.
func ListenOrRecreate(path string, uid, gid int, mode os.FileMode) (net.Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("wire: socket path must be absolute; got %q", path)
	}
	if mode == 0 || mode > 0o777 {
		return nil, fmt.Errorf("wire: invalid mode %o", mode)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("wire: mkdir %q: %w", dir, err)
	}

	// Remove any stale socket left from an unclean shutdown. ENOENT is fine;
	// anything else (e.g. EBUSY = process still listening) is propagated.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("wire: remove stale %q: %w", path, err)
	}

	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("wire: listen %q: %w", path, err)
	}

	// Chown first (root-only) then chmod (everyone). Both can fail when the
	// caller lacks CAP_CHOWN; emit the failure wrapped clearly so the
	// daemon can decide whether to abort.
	if uid != 0 || gid != 0 {
		if err := os.Chown(path, uid, gid); err != nil {
			_ = l.Close()
			return nil, fmt.Errorf("wire: chown %q to %d:%d: %w", path, uid, gid, err)
		}
	}
	if err := os.Chmod(path, mode); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("wire: chmod %q to %o: %w", path, mode, err)
	}

	return l, nil
}

// ListenOrRecreateByName is the convenience entrypoint: looks up the daemon's
// uid and the `faas` group gid, then calls ListenOrRecreate. Returns an error
// (and a nil listener) if either lookup fails — call sites that want to
// tolerate missing groups can use ListenOrRecreate with explicit integer ids.
//
// Tests that drive daemons through the cmd/e2e harness on a CI runner or
// dev box without the ansible bootstrap set SkipGroupLookupEnv; in that
// case the daemon falls back to gid=0 and skips the chown, so the unix
// socket still binds (production deployments always have the `faas`
// group, so this fallback is test-only).
func ListenOrRecreateByName(path, daemonUser string) (net.Listener, error) {
	uid, err := lookupUserUID(daemonUser)
	if err != nil {
		return nil, fmt.Errorf("wire: lookup uid for %q: %w", daemonUser, err)
	}
	gid, err := lookupGroupGID(DefaultSocketGroup)
	if err != nil {
		if os.Getenv(SkipGroupLookupEnv) != "" {
			// Test-only fallback: bind the socket owned by the daemon
			// uid with mode 0660 (no group chown). The harness sets
			// SkipGroupLookupEnv because the `faas` group doesn't
			// exist on dev / CI boxes that haven't run the ansible
			// role. Production deploys never set this — the group is
			// created at bootstrap.
			l, lerr := listenSkipChown(path, DefaultSocketMode)
			if lerr != nil {
				return nil, fmt.Errorf("wire: listen (skip group) %q: %w", path, lerr)
			}
			return l, nil
		}
		return nil, fmt.Errorf("wire: lookup gid for %q: %w", DefaultSocketGroup, err)
	}
	return ListenOrRecreate(path, uid, gid, DefaultSocketMode)
}

// listenSkipChown binds a unix socket and chmods it without chowning (the
// gid lookup failed and SkipGroupLookupEnv was set). Mirrors the body of
// ListenOrRecreate minus the chown step; the test harness sets the env
// precisely because the chown would fail on a host without the `faas` group.
func listenSkipChown(path string, mode os.FileMode) (net.Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("wire: socket path must be absolute; got %q", path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("wire: mkdir %q: %w", dir, err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("wire: remove stale %q: %w", path, err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("wire: listen %q: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("wire: chmod %q to %o: %w", path, mode, err)
	}
	return l, nil
}

func lookupUserUID(name string) (int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}

// LookupGroupGID resolves a unix group name to its numeric gid; returns
// (gid, false) if the group does not exist. Centralised so tests don't
// reimplement getent lookups.
func LookupGroupGID(name string) (int, bool) {
	gid, err := lookupGroupGID(name)
	if err != nil {
		return 0, false
	}
	return gid, true
}

func lookupGroupGID(name string) (int, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(g.Gid)
}

// ParseUIDGID parses "uid:gid" — used by tests and configs that store the
// pair as a single string. Convenience, not a stable wire format.
func ParseUIDGID(s string) (uid, gid int, err error) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			uid, err = strconv.Atoi(s[:i])
			if err != nil {
				return 0, 0, fmt.Errorf("wire: bad uid in %q: %w", s, err)
			}
			gid, err = strconv.Atoi(s[i+1:])
			if err != nil {
				return 0, 0, fmt.Errorf("wire: bad gid in %q: %w", s, err)
			}
			return uid, gid, nil
		}
	}
	return 0, 0, fmt.Errorf("wire: expected \"uid:gid\", got %q", s)
}
