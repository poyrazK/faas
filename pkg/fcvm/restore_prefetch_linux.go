//go:build linux

package fcvm

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// pagemap entry bits (Documentation/admin-guide/mm/pagemap.rst).
const (
	pagemapPresent = uint64(1) << 63
	pagemapSwapped = uint64(1) << 62
)

// adviseWillNeed queues readahead for ranges of path. FADV_WILLNEED submits
// the reads and returns without waiting for them.
func adviseWillNeed(path string, ranges []fileRange) error {
	f, err := os.Open(path) //nolint:gosec // vmmd-resolved snapshot cache path, never customer-supplied
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for _, r := range ranges {
		if err := unix.Fadvise(int(f.Fd()), r.Off, r.Len, unix.FADV_WILLNEED); err != nil {
			return fmt.Errorf("fadvise %d+%d: %w", r.Off, r.Len, err)
		}
	}
	return nil
}

// touchedFileRanges returns the byte ranges of path that process pid has
// mapped and touched: every page of a mapping of path's inode that is present
// (or swapped) in the process page table. For Firecracker's MAP_PRIVATE guest
// memory mapping those are exactly the guest pages faulted since restore.
func touchedFileRanges(pid int, path string) ([]fileRange, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return nil, err
	}
	maps, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		return nil, err
	}
	pagemap, err := os.Open(fmt.Sprintf("/proc/%d/pagemap", pid))
	if err != nil {
		return nil, err
	}
	defer func() { _ = pagemap.Close() }()
	page := int64(os.Getpagesize())
	var out []fileRange
	matched := false
	for _, line := range strings.Split(string(maps), "\n") {
		vma, ok := parseMapsLine(line)
		if !ok || vma.inode != st.Ino || vma.major != unix.Major(st.Dev) || vma.minor != unix.Minor(st.Dev) {
			continue
		}
		matched = true
		touched, err := touchedPages(pagemap, vma.start, vma.end, page)
		if err != nil {
			return nil, err
		}
		for _, p := range touched {
			off := vma.offset + p*page
			if n := len(out); n > 0 && out[n-1].Off+out[n-1].Len == off {
				out[n-1].Len += page
				continue
			}
			out = append(out, fileRange{Off: off, Len: page})
		}
	}
	if !matched {
		return nil, fmt.Errorf("no mapping of %s in pid %d", path, pid)
	}
	return out, nil
}

type mapsVMA struct {
	start, end   uint64
	offset       int64
	major, minor uint32
	inode        uint64
}

// parseMapsLine parses "start-end perms offset major:minor inode [path]".
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
	var v mapsVMA
	var err error
	if v.start, err = strconv.ParseUint(addrs[0], 16, 64); err != nil {
		return mapsVMA{}, false
	}
	if v.end, err = strconv.ParseUint(addrs[1], 16, 64); err != nil || v.end <= v.start {
		return mapsVMA{}, false
	}
	if v.offset, err = strconv.ParseInt(f[2], 16, 64); err != nil {
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

// touchedPages returns the indexes, relative to start, of the pages in
// [start,end) whose pagemap entry is present or swapped.
func touchedPages(pagemap io.ReaderAt, start, end uint64, page int64) ([]int64, error) {
	const chunk = 64 << 10 // entries per read (512 KiB)
	total := int64(end-start) / page
	first := int64(start) / page
	buf := make([]byte, chunk*8)
	var out []int64
	for done := int64(0); done < total; {
		n := min(int64(chunk), total-done)
		b := buf[:n*8]
		if _, err := pagemap.ReadAt(b, (first+done)*8); err != nil {
			return nil, fmt.Errorf("read pagemap: %w", err)
		}
		for i := int64(0); i < n; i++ {
			if binary.LittleEndian.Uint64(b[i*8:])&(pagemapPresent|pagemapSwapped) != 0 {
				out = append(out, done+i)
			}
		}
		done += n
	}
	return out, nil
}
