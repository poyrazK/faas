// adr: 933
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// reclaimRestartDead retires instance records whose guest process is provably
// gone and whose kernel resources are verifiably absent (ADR-933). Each record
// still in the journal otherwise quarantines its slot for this vmmd's lifetime,
// and the record outlives every restart: production-us fsn-2 carried 13 such
// records (13 of 32 slots) after rc.251's rollout, plus their materialised
// snapshot files and layer clones on disk.
//
// It runs before reconcileJournal so a retired record's slot is never
// quarantined. Only identity-verified temporary files are removed; links,
// namespaces, jails, mounts and processes are never touched. Any ambiguity
// keeps the record quarantined.
func (inv *restartInventory) reclaimRestartDead(ctx context.Context, j *ResourceJournal, opts restartInventoryOptions) (int, error) {
	if j == nil {
		return 0, nil
	}
	items, err := j.snapshot()
	if err != nil {
		return 0, err
	}
	var holders *restartHolders
	reclaimed := 0
	for _, r := range items {
		if err := ctx.Err(); err != nil {
			return reclaimed, err
		}
		if r.Prepared != nil || r.Process == nil {
			continue
		}
		if dead, err := restartProcessGone(opts.procRoot, *r.Process); err != nil || !dead {
			continue
		}
		if holders == nil {
			if holders, err = scanRestartHolders(ctx, opts); err != nil {
				// Unreadable inventory keeps every record; startup proceeds
				// with the pre-ADR-933 quarantine.
				return reclaimed, nil
			}
		}
		files, ok := inv.restartDeadEligible(r, opts, holders)
		if !ok {
			continue
		}
		removed := true
		for _, f := range files {
			if err := removeResourceFile(f.Path, *f.File); err != nil {
				removed = false
				break
			}
		}
		if !removed {
			continue
		}
		retired, err := j.forgetRestartDead(r)
		if err != nil {
			return reclaimed, fmt.Errorf("retire dead instance record %s: %w", r.Lease.Instance, err)
		}
		if retired {
			reclaimed++
		}
	}
	return reclaimed, nil
}

// restartProcessGone reports whether the recorded guest process no longer
// exists: its PID is absent, or now names a different process (other start
// ticks or kernel boot).
func restartProcessGone(procRoot string, recorded resourceProcessIdentity) (bool, error) {
	current, err := readResourceProcessIdentity(procRoot, recorded.PID)
	if errors.Is(err, errResourceProcessAbsent) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return current != recorded, nil
}

// restartDeadEligible checks a dead record against the same-boot hazards ADR-478
// names (setup children, renamed links, held namespaces) and returns the
// identity-verified temporary files to remove before retirement.
func (inv *restartInventory) restartDeadEligible(r resourceJournalRecord, opts restartInventoryOptions, h *restartHolders) ([]resourceAsset, bool) {
	l := r.Lease
	if _, held := h.uidSlots[l.Slot]; held {
		return nil, false
	}
	if absent, err := inv.restartPreparedAbsent(l, opts); err != nil || !absent {
		return nil, false
	}
	var files []resourceAsset
	for _, a := range r.Assets {
		switch a.Kind {
		case "veth":
			if a.Link == nil || h.links[restartLinkKey{a.Link.Index, strings.ToLower(a.Link.Address)}] {
				return nil, false
			}
		case "netns":
			if a.File == nil || h.netns[a.File.Inode] {
				return nil, false
			}
		case "jail", "bind":
			if present, err := restartPathPresent(a.Path); err != nil || present {
				return nil, false
			}
		case "materialised", "clone":
			if a.File == nil {
				if present, err := restartPathPresent(a.Path); err != nil || present {
					return nil, false
				}
				continue
			}
			info, err := os.Lstat(a.Path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, false
			}
			// A path now naming another file is not ours to remove; the
			// recorded file is already gone.
			if current, err := resourceFileID(info); err == nil && current == *a.File {
				files = append(files, a)
			}
		default:
			return nil, false
		}
	}
	return files, true
}

func restartPathPresent(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, err
	}
}

type restartLinkKey struct {
	index   int
	address string
}

// restartHolders is the host-wide evidence a dead record must not match.
type restartHolders struct {
	uidSlots map[int]struct{}
	links    map[restartLinkKey]bool
	netns    map[uint64]bool
}

// scanRestartHolders reads every process's UIDs and network namespace, every
// nsfs mount and every link's index and address. Any unreadable source fails
// the scan, which keeps all records.
func scanRestartHolders(ctx context.Context, opts restartInventoryOptions) (*restartHolders, error) {
	uidSlots, err := restartUIDSlots(ctx, opts.procRoot)
	if err != nil {
		return nil, err
	}
	h := &restartHolders{uidSlots: uidSlots, links: make(map[restartLinkKey]bool), netns: make(map[uint64]bool)}
	procs, err := os.ReadDir(opts.procRoot)
	if err != nil {
		return nil, err
	}
	for _, p := range procs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		target, err := os.Readlink(filepath.Join(opts.procRoot, p.Name(), "ns", "net"))
		if errors.Is(err, os.ErrNotExist) {
			continue // exited, or a kernel thread without a namespace link
		}
		if err != nil {
			return nil, err
		}
		if inode, ok := nsfsInode(target, "net"); ok {
			h.netns[inode] = true
		}
	}
	mounts, err := os.ReadFile(filepath.Join(opts.procRoot, "self", "mountinfo"))
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(mounts), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if inode, ok := nsfsInode(fields[3], "net"); ok {
			h.netns[inode] = true
		}
	}
	links, err := os.ReadDir(opts.netRoot)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		dir := filepath.Join(opts.netRoot, link.Name())
		rawIndex, err := os.ReadFile(filepath.Join(dir, "ifindex"))
		if errors.Is(err, os.ErrNotExist) {
			continue // removed during the scan
		}
		if err != nil {
			return nil, err
		}
		index, err := strconv.Atoi(strings.TrimSpace(string(rawIndex)))
		if err != nil {
			return nil, fmt.Errorf("link %s ifindex: %w", link.Name(), err)
		}
		address, err := os.ReadFile(filepath.Join(dir, "address"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		h.links[restartLinkKey{index, strings.ToLower(strings.TrimSpace(string(address)))}] = true
	}
	return h, nil
}

// nsfsInode parses "net:[4026532316]" (an ns link target or an nsfs mount root).
func nsfsInode(s, kind string) (uint64, bool) {
	prefix := kind + ":["
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, "]") {
		return 0, false
	}
	inode, err := strconv.ParseUint(s[len(prefix):len(s)-1], 10, 64)
	return inode, err == nil && inode != 0
}

// forgetRestartDead unlinks a dead instance record that is unchanged since the
// eligibility checks and fsyncs the journal directory.
func (j *ResourceJournal) forgetRestartDead(r resourceJournalRecord) (bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return false, errResourceJournalClosed
	}
	current, ok := j.records[r.Lease.Instance]
	if !ok {
		return false, nil
	}
	if !resourceLeaseMatches(current.Lease, r.Lease) || current.Prepared != nil || current.Process == nil || r.Process == nil || *current.Process != *r.Process {
		return false, errors.New("dead instance record changed during retirement")
	}
	if err := j.forgetRecord(current); err != nil {
		return false, err
	}
	return true, nil
}
