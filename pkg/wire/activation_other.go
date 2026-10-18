//go:build !unix

package wire

import "errors"

func inspectInheritedFD(int) (string, string, error) {
	return "", "", errors.New("socket activation requires a unix platform")
}
