//go:build linux

package fcvm

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/netns"
)

func nativeNetworkRemoved(nc netns.Config) error {
	if _, err := os.Lstat(filepath.Join("/run/netns", nc.Netns)); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return errors.New("native recovery: network namespace survived cleanup")
	}
	links, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, link := range links {
		if link.Name == nc.VethHost || link.Name == nc.VethPeer || nc.PrivateVethHost != "" && link.Name == nc.PrivateVethHost || nc.PrivateVethPeer != "" && link.Name == nc.PrivateVethPeer {
			return fmt.Errorf("native recovery: host interface %s survived cleanup", link.Name)
		}
	}
	return nil
}
