//go:build linux && !amd64

package jailsetup

import "errors"

func acknowledgeHostHelperSessionPolicy(_ int) error {
	return errors.New("native helper session policy requires Linux x86_64")
}
