//go:build linux

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/netns"
	"golang.org/x/sys/unix"
)

func preparedNetworkRemoved(nc netns.Config) bool {
	_, nsErr := os.Lstat(filepath.Join("/run/netns", nc.Netns))
	if !errors.Is(nsErr, os.ErrNotExist) {
		return false
	}
	// Query the caller's network namespace. A sysfs mount may still describe
	// its parent's network namespace after unshare, including in metal tests.
	links, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, link := range links {
		if link.Name == nc.VethHost {
			return false
		}
	}
	return true
}

// Preserve the current bridge MAC and mark it as explicitly assigned. Linux's
// automatic MAC selection can otherwise change it when a cache worker adds
// a veth, invalidating the gateway MAC already cached by running namespaces.
func pinPreparedNetworkBridge(ctx context.Context, run Runner) error {
	link, err := net.InterfaceByName(netns.TenantBridge)
	if err != nil {
		return fmt.Errorf("prepared network bridge: %w", err)
	}
	if len(link.HardwareAddr) != 6 {
		return fmt.Errorf("prepared network bridge: missing Ethernet address")
	}
	if err := run.Run(ctx, []string{"ip", "link", "set", "dev", netns.TenantBridge, "address", link.HardwareAddr.String()}); err != nil {
		return fmt.Errorf("pin prepared network bridge address: %w", err)
	}
	return nil
}

// ReapPreparedNetworks runs once at daemon startup, before accepting RPCs.
// Only the private, UUID-qualified unused-cache names are eligible. Claimed
// namespaces have ordinary instance names and follow normal VM recovery.
func ReapPreparedNetworks(ctx context.Context, run Runner) error {
	entries, err := os.ReadDir("/run/netns")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "fc-prepared-") {
			continue
		}
		if _, err := uuid.Parse(strings.TrimPrefix(name, "fc-prepared-")); err != nil {
			continue
		}
		if err := run.Run(ctx, []string{"ip", "netns", "del", name}); err != nil {
			removeStaleNetnsMarker(name)
			if _, statErr := os.Lstat(filepath.Join("/run/netns", name)); !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("remove unused prepared namespace %s: %w", name, err)
			}
		}
	}
	return nil
}

// A network namespace is a bind mount. rename(2) fails with EBUSY and MS_MOVE
// is invalid beneath iproute2's shared mount. Bind the new exclusive marker,
// then detach the old alias. The namespace and all policy objects stay intact.
func movePreparedNetns(oldName, newName string) (err error) {
	if !strings.HasPrefix(oldName, "fc-prepared-") || oldName == newName ||
		filepath.Base(oldName) != oldName || filepath.Base(newName) != newName || !strings.HasPrefix(newName, "fc-") {
		return fmt.Errorf("prepared network: invalid namespace names")
	}
	oldPath, newPath := filepath.Join("/run/netns", oldName), filepath.Join("/run/netns", newName)
	f, err := os.OpenFile(newPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("prepared network marker: %w", err)
	}
	_ = f.Close()
	bound := false
	defer func() {
		if err != nil {
			if bound {
				_ = unix.Unmount(newPath, unix.MNT_DETACH)
			}
			_ = os.Remove(newPath)
		}
	}()
	if err = unix.Mount(oldPath, newPath, "", unix.MS_BIND, ""); err != nil {
		return err
	}
	bound = true
	if err = unix.Unmount(oldPath, unix.MNT_DETACH); err != nil {
		return err
	}
	if err = os.Remove(oldPath); err != nil {
		return err
	}
	return nil
}
