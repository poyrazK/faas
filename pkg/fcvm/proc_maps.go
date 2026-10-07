package fcvm

import (
	"math"
	"strconv"
	"strings"
)

type mapsVMA struct {
	start, end   int64
	offset       int64
	major, minor uint32
	inode        uint64
	permissions  string
}

// parseMapsLine parses "start-end perms offset major:minor inode [path]".
// Anonymous mappings have no file identity and are deliberately omitted.
func parseMapsLine(line string) (mapsVMA, bool) {
	f := strings.Fields(line)
	if len(f) < 5 {
		return mapsVMA{}, false
	}
	addrs := strings.SplitN(f[0], "-", 2)
	dev := strings.SplitN(f[3], ":", 2)
	if len(addrs) != 2 || len(dev) != 2 {
		return mapsVMA{}, false
	}
	v := mapsVMA{permissions: f[1]}
	var ok bool
	if v.start, ok = parseHexAddr(addrs[0]); !ok {
		return mapsVMA{}, false
	}
	if v.end, ok = parseHexAddr(addrs[1]); !ok || v.end <= v.start {
		return mapsVMA{}, false
	}
	var err error
	if v.offset, err = strconv.ParseInt(f[2], 16, 64); err != nil || v.offset < 0 {
		return mapsVMA{}, false
	}
	major, err := strconv.ParseUint(dev[0], 16, 32)
	if err != nil {
		return mapsVMA{}, false
	}
	minor, err := strconv.ParseUint(dev[1], 16, 32)
	if err != nil {
		return mapsVMA{}, false
	}
	v.major, v.minor = uint32(major), uint32(minor)
	if v.inode, err = strconv.ParseUint(f[4], 10, 64); err != nil || v.inode == 0 {
		return mapsVMA{}, false
	}
	return v, true
}

// User-space addresses are below 2^63; reject larger values instead of wrapping.
func parseHexAddr(s string) (int64, bool) {
	u, err := strconv.ParseUint(s, 16, 64)
	if err != nil || u > math.MaxInt64 {
		return 0, false
	}
	return int64(u), true
}
