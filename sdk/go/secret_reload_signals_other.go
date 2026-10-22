//go:build !unix

package faas

import "os"

func registerSecretHangups() (<-chan os.Signal, func(), error) {
	return nil, nil, secretReloadError("SIGHUP is unavailable on this platform")
}
