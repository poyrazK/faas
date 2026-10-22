//go:build unix

package faas

import (
	"os"
	"os/signal"
	"syscall"
)

func registerSecretHangups() (<-chan os.Signal, func(), error) {
	hangups := make(chan os.Signal, 1)
	signal.Notify(hangups, syscall.SIGHUP)
	return hangups, func() { signal.Stop(hangups) }, nil
}
