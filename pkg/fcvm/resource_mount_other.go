//go:build !linux

// adr: 400
package fcvm

import "errors"

func resourceMountNamespace() (resourceMountIdentity, error) {
	return resourceMountIdentity{}, errors.New("mount provenance requires Linux")
}

func resourceMountAt(string) (*resourceMountIdentity, error) {
	return nil, errors.New("mount provenance requires Linux")
}
