//go:build !linux

package fcvm

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/netns"
)

func nativeNetworkRemoved(netns.Config) error {
	return errors.New("native recovery: network cleanup verification requires Linux")
}
