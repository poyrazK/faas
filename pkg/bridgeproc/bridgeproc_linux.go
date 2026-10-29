//go:build linux

package bridgeproc

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func harden(getenv func(string) string) error {
	if err := dropTo(getenv); err != nil {
		return err
	}
	// vmmd's unit already sets NoNewPrivileges, which every child inherits.
	// Setting it again costs nothing and holds when a bridge is run by hand.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	if raw := getenv(ExpectUIDEnv); raw != "" {
		uid, err := parseExpectedUID(raw)
		if err != nil {
			return err
		}
		status, err := os.ReadFile("/proc/self/status")
		if err != nil {
			return fmt.Errorf("read own credentials: %w", err)
		}
		if err := checkStatus(string(status), uid); err != nil {
			return fmt.Errorf("bridge is not running unprivileged: %w", err)
		}
	}
	if raw := getenv(ParentPIDEnv); raw != "" {
		parent, err := strconv.Atoi(raw)
		if err != nil || parent <= 1 {
			return fmt.Errorf("%s=%q: want the parent's pid", ParentPIDEnv, raw)
		}
		if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(unix.SIGTERM), 0, 0, 0); err != nil {
			return fmt.Errorf("set parent-death signal: %w", err)
		}
		// The parent may have died between the uid change and the prctl.
		if os.Getppid() != parent {
			return errors.New("parent exited before the bridge started")
		}
	}
	return nil
}

// dropTo moves a bridge started as root to its uid: no supplementary
// group, then every gid, then every uid. The stdlib calls apply to all
// threads. Leaving uid 0 clears every capability; the ExpectUIDEnv check
// that follows confirms it.
func dropTo(getenv func(string) string) error {
	raw := getenv(DropToUIDEnv)
	if raw == "" {
		return nil
	}
	uid, err := parseExpectedUID(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", DropToUIDEnv, err)
	}
	if getenv(ExpectUIDEnv) != raw {
		return fmt.Errorf("%s=%q must equal %s", DropToUIDEnv, raw, ExpectUIDEnv)
	}
	if os.Geteuid() != 0 {
		return nil // already dropped, or started unprivileged: the check decides
	}
	if err := syscall.Setgroups(nil); err != nil {
		return fmt.Errorf("drop supplementary groups: %w", err)
	}
	if err := syscall.Setresgid(uid, uid, uid); err != nil {
		return fmt.Errorf("drop to gid %d: %w", uid, err)
	}
	if err := syscall.Setresuid(uid, uid, uid); err != nil {
		return fmt.Errorf("drop to uid %d: %w", uid, err)
	}
	return nil
}
