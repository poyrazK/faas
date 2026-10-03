// adr: 404
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

// Only a fully checkpointed, never-claimed spare can prove that all of its
// resources were volatile objects of one earlier kernel boot. Missing paths
// alone cannot rule out a same-boot setup child, renamed link or held namespace.
func restartPreparedBoot(r resourceJournalRecord) string {
	if !r.preparedSpare() || r.Prepared.Target != "" {
		return ""
	}
	ns, link, err := preparedRecordAssets(r)
	if err != nil || ns.Namespace.BootID != link.Namespace.BootID {
		return ""
	}
	return ns.Namespace.BootID
}

func (inv *restartInventory) reclaimRestartPrepared(ctx context.Context, j *ResourceJournal, opts restartInventoryOptions) (int, error) {
	if j == nil {
		return 0, nil
	}
	items, err := j.snapshot()
	if err != nil {
		return 0, err
	}
	var currentBoot string
	var uidSlots map[int]struct{}
	reclaimed := 0
	for _, r := range items {
		if err := ctx.Err(); err != nil {
			return reclaimed, err
		}
		priorBoot := restartPreparedBoot(r)
		if priorBoot == "" {
			continue
		}
		if currentBoot == "" {
			currentBoot, err = resourceKernelBootID(opts.procRoot)
			if err != nil {
				return reclaimed, err
			}
		}
		if priorBoot == currentBoot {
			continue
		}
		if uidSlots == nil {
			uidSlots, err = restartUIDSlots(ctx, opts.procRoot)
			if err != nil {
				return reclaimed, err
			}
		}
		if _, held := uidSlots[r.Lease.Slot]; held {
			continue
		}
		absent, err := inv.restartPreparedAbsent(r.Lease, opts)
		if err != nil {
			return reclaimed, err
		}
		if !absent {
			continue
		}
		if err := ctx.Err(); err != nil {
			return reclaimed, err
		}
		retired, err := j.forgetRestartPrepared(r.Lease, currentBoot)
		if err != nil {
			return reclaimed, err
		}
		if retired {
			reclaimed++
		}
	}
	return reclaimed, nil
}

// Keep even foreign or dangling bindings; absence checks authorize only journal
// retirement. This path never deletes a link, namespace, jail or process.
func (inv *restartInventory) restartPreparedAbsent(l Lease, opts restartInventoryOptions) (bool, error) {
	if _, held := inv.slots[l.Slot]; held {
		return false, nil
	}
	if _, held := inv.instances[l.Instance]; held {
		return false, nil
	}
	privateHost, privatePeer := privateVethNames(l.Slot)
	paths := []string{
		filepath.Join(opts.netRoot, l.VethHost), filepath.Join(opts.netRoot, l.VethPeer),
		filepath.Join(opts.netRoot, privateHost), filepath.Join(opts.netRoot, privatePeer),
		filepath.Join(opts.netnsRoot, l.Netns), filepath.Join(opts.jailRoot, l.Instance),
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			return false, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return true, nil
}

func (j *ResourceJournal) forgetRestartPrepared(l Lease, currentBoot string) (bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return false, errResourceJournalClosed
	}
	r, ok := j.records[l.Instance]
	if !ok || !resourceLeaseMatches(r.Lease, l) {
		return false, errors.New("restart spare reservation changed")
	}
	priorBoot := restartPreparedBoot(r)
	if !looksLikeInstanceID(currentBoot) || priorBoot == "" || priorBoot == currentBoot {
		return false, errors.New("restart spare lacks prior-boot provenance")
	}
	if err := j.forgetRecord(r); err != nil {
		return false, err
	}
	return true, nil
}

// Check all four UID fields of every process, including unrelated executables
// and partially dropped credentials. Unknown inventory fails before retirement.
func restartUIDSlots(ctx context.Context, root string) (map[int]struct{}, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	slots := make(map[int]struct{})
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "status"))
		if errors.Is(err, os.ErrNotExist) {
			continue // Process exited during inventory.
		}
		if err != nil {
			return nil, err
		}
		if err := restartStatusUIDSlots(string(data), slots); err != nil {
			return nil, fmt.Errorf("process %d UID inventory: %w", pid, err)
		}
	}
	return slots, nil
}

func restartStatusUIDSlots(status string, slots map[int]struct{}) error {
	for _, line := range strings.Split(status, "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return errors.New("incomplete process UIDs")
		}
		for _, field := range fields[1:] {
			uid, err := strconv.ParseUint(field, 10, 32)
			if err != nil {
				return errors.New("invalid process UID")
			}
			if uid >= JailUIDBase && uid < JailUIDBase+MaxSlots {
				slots[int(uid)-JailUIDBase] = struct{}{}
			}
		}
		return nil
	}
	return errors.New("missing process UIDs")
}
