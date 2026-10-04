//go:build !linux

// adr: 475
package fcvm

import "errors"

func resourcePlacementContext() (*resourceMountIdentity, error) { return nil, nil }

func resourceNetworkNamespaceAt(string) (*resourceAsset, error) {
	return nil, errors.New("network namespace provenance requires Linux")
}

func resourceMountTreeClear(string) error { return nil }
