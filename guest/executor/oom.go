package executor

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

func guestOOMKills(reader io.Reader) (uint64, bool) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "oom_kill" {
			value, err := strconv.ParseUint(fields[1], 10, 64)
			return value, err == nil
		}
	}
	return 0, false
}
