package fcvm

// adr: 595
// The expected inode must back exactly one complete private guest-memory range.

import (
	"cmp"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type RuntimeSnapshotMemoryRange struct {
	Start, End, Offset int64
}

type snapshotMemoryFileIdentity struct {
	major, minor uint32
	inode        uint64
	bytes        int64
}

func verifySnapshotMemoryMaps(body []byte, file snapshotMemoryFileIdentity, page int64) ([]RuntimeSnapshotMemoryRange, error) {
	if file.inode == 0 || file.bytes <= 0 || page <= 0 || file.bytes%page != 0 {
		return nil, runtimeadmission.ErrInvalid
	}
	var ranges []RuntimeSnapshotMemoryRange
	for _, line := range strings.Split(string(body), "\n") {
		v, ok := parseMapsLine(line)
		if !ok || v.inode != file.inode || v.major != file.major || v.minor != file.minor {
			continue
		}
		length := v.end - v.start
		if v.permissions != "rw-p" || v.start%page != 0 || v.end%page != 0 || v.offset%page != 0 || v.offset > file.bytes || length > file.bytes-v.offset {
			return nil, runtimeadmission.ErrInvalid
		}
		ranges = append(ranges, RuntimeSnapshotMemoryRange{Start: v.start, End: v.end, Offset: v.offset})
	}
	if len(ranges) == 0 {
		return nil, runtimeadmission.ErrInvalid
	}
	// Reject aliases as well as overlapping or omitted file bytes. File order
	// can differ from virtual-address order when the guest has a memory hole.
	slices.SortFunc(ranges, func(a, b RuntimeSnapshotMemoryRange) int { return cmp.Compare(a.Start, b.Start) })
	for i := 1; i < len(ranges); i++ {
		if ranges[i].Start < ranges[i-1].End {
			return nil, runtimeadmission.ErrInvalid
		}
	}
	slices.SortFunc(ranges, func(a, b RuntimeSnapshotMemoryRange) int { return cmp.Compare(a.Offset, b.Offset) })
	var covered int64
	for _, r := range ranges {
		if r.Offset != covered {
			return nil, runtimeadmission.ErrInvalid
		}
		covered += r.End - r.Start // Bounded by the checked file size above.
	}
	if covered != file.bytes {
		return nil, runtimeadmission.ErrInvalid
	}
	return ranges, nil
}
