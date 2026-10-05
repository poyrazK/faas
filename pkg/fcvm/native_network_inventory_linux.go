//go:build linux

package fcvm

import (
	"errors"
	"net"
	"os"
)

func nativeNetworkInventory(leases []Lease) error {
	entries, err := os.ReadDir("/run/netns")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var namespaces, interfaces []string
	for _, entry := range entries {
		namespaces = append(namespaces, entry.Name())
	}
	links, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, link := range links {
		interfaces = append(interfaces, link.Name)
	}
	return checkNativeNetworkInventory(leases, namespaces, interfaces)
}
