//go:build metal

package leakcheck

import "strings"

func nativeLoopTokenPresent(name []byte) bool {
	return strings.HasPrefix(strings.TrimRight(string(name), "\x00"), "gregale-loop:")
}
