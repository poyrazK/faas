//go:build metal

package leakcheck

import (
	"errors"
	"fmt"
	"strings"
)

func nativeImageMountsFrom(data []byte) []error {
	var leaks []error
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return []error{errors.New("native image mount inventory is incomplete")}
		}
		if strings.Contains(fields[4], "/.native-processes/image-sources/points/") {
			leaks = append(leaks, fmt.Errorf("native image anchor mount %s", fields[4]))
		}
		if strings.HasSuffix(fields[4], "/faas-host-tun") || strings.Contains(fields[4], "/faas-host-tun/") || strings.HasSuffix(fields[4], "/faas-host-tun\\040(deleted)") {
			leaks = append(leaks, fmt.Errorf("native TUN device mount %s", fields[4]))
		}
	}
	return leaks
}
