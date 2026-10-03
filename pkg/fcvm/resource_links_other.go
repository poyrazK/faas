//go:build !linux

// adr: 402
package fcvm

import "errors"

func resourceNetworkContext() (*resourceMountIdentity, error) {
	return nil, errors.New("veth context requires Linux")
}

func resourceNetworkLinkAt(string, int) (*resourceLinkIdentity, error) {
	return nil, errors.New("veth observation requires Linux")
}

func resourceNetworkLinkDelete(int) error {
	return errors.New("veth deletion requires Linux")
}
