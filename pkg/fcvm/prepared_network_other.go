//go:build !linux

package fcvm

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/netns"
)

func preparedNetworkRemoved(netns.Config) bool { return true }

func pinPreparedNetworkBridge(context.Context, Runner) error {
	return fmt.Errorf("prepared networks require Linux")
}

func ReapPreparedNetworks(context.Context, Runner) error { return nil }

func movePreparedNetns(_, _ string) error {
	return fmt.Errorf("prepared networks require Linux")
}
