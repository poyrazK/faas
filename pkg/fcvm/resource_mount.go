// adr: 400
package fcvm

import (
	"errors"
	"strconv"
	"strings"
)

func parseResourceMountInfo(data []byte, path string) (uint64, bool, error) {
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	var mountID uint64
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 10 || !strings.Contains(line, " - ") {
			return 0, false, errors.New("invalid resource mountinfo")
		}
		if unescape.Replace(fields[4]) != path {
			continue
		}
		id, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || id == 0 || mountID != 0 {
			return 0, false, errors.New("ambiguous resource mount identity")
		}
		mountID = id
	}
	return mountID, mountID != 0, nil
}
