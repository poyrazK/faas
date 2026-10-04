// adr: 474
package fcvm

import (
	"errors"
	"strconv"
	"strings"
)

func parseResourceMountInfo(data []byte, path string) (uint64, bool, error) {
	ids, err := parseResourceMountCandidates(data, path)
	if err != nil {
		return 0, false, err
	}
	if len(ids) > 1 {
		return 0, false, errors.New("ambiguous resource mount identity")
	}
	if len(ids) == 0 {
		return 0, false, nil
	}
	return ids[0], true, nil
}

func parseResourceMountCandidates(data []byte, path string) ([]uint64, error) {
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	var mountIDs []uint64
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 10 || !strings.Contains(line, " - ") {
			return nil, errors.New("invalid resource mountinfo")
		}
		if unescape.Replace(fields[4]) != path {
			continue
		}
		id, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || id == 0 {
			return nil, errors.New("invalid resource mount identity")
		}
		for _, existing := range mountIDs {
			if existing == id {
				return nil, errors.New("duplicate resource mount id")
			}
		}
		mountIDs = append(mountIDs, id)
	}
	return mountIDs, nil
}

func selectResourceMountID(candidates []uint64, observed uint64) (uint64, error) {
	if observed == 0 {
		return 0, errors.New("observed resource mount id is zero")
	}
	for _, id := range candidates {
		if id == observed {
			return id, nil
		}
	}
	return 0, errors.New("observed resource mount id is not at the requested path")
}
