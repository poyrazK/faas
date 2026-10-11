// Package guestmemproto defines the guest/host wire contract vmmd uses to ask
// guest-init what its memory holds just before a snapshot capture. The
// breakdown explains a snapshot's non-zero content (page cache vs anonymous
// memory vs kernel), so it has no dependencies on the VMM or guest-init.
package guestmemproto

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// MessageType shares the resume listener with types 1, 3, 4 and 5. The
// request body is empty; the reply is one OK byte, a 4-byte big-endian
// length and the Stats JSON. A guest that predates it answers with a single
// non-zero byte.
const MessageType uint32 = 6

// MaxBodyBytes bounds the Stats JSON the host accepts.
const MaxBodyBytes = 4096

// Stats is a subset of /proc/meminfo, in bytes.
type Stats struct {
	MemTotal     int64 `json:"mem_total"`
	MemFree      int64 `json:"mem_free"`
	Buffers      int64 `json:"buffers"`
	Cached       int64 `json:"cached"`
	Shmem        int64 `json:"shmem"`
	AnonPages    int64 `json:"anon_pages"`
	Slab         int64 `json:"slab"`
	SReclaimable int64 `json:"sreclaimable"`
	KernelStack  int64 `json:"kernel_stack"`
	PageTables   int64 `json:"page_tables"`
}

// Parse reads /proc/meminfo lines ("Cached:   12345 kB"). Unknown and
// malformed lines are skipped so a kernel adding fields never fails capture.
func Parse(r io.Reader) (Stats, error) {
	var s Stats
	fields := map[string]*int64{
		"MemTotal": &s.MemTotal, "MemFree": &s.MemFree, "Buffers": &s.Buffers,
		"Cached": &s.Cached, "Shmem": &s.Shmem, "AnonPages": &s.AnonPages,
		"Slab": &s.Slab, "SReclaimable": &s.SReclaimable,
		"KernelStack": &s.KernelStack, "PageTables": &s.PageTables,
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		dst := fields[name]
		if dst == nil {
			continue
		}
		parts := strings.Fields(rest)
		if len(parts) == 0 {
			continue
		}
		n, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || n < 0 {
			continue
		}
		if len(parts) > 1 && parts[1] == "kB" {
			n *= 1024
		}
		*dst = n
	}
	return s, sc.Err()
}
