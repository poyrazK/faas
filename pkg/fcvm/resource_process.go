// adr: 399
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

var errResourceProcessAbsent = errors.New("recorded resource process absent")

func readResourceProcessIdentity(root string, pid int) (resourceProcessIdentity, error) {
	p := resourceProcessIdentity{PID: pid}
	boot, err := os.ReadFile(filepath.Join(root, "sys/kernel/random/boot_id"))
	if err != nil {
		return p, err
	}
	p.BootID = strings.TrimSpace(string(boot))
	if !looksLikeInstanceID(p.BootID) {
		return p, errors.New("invalid kernel boot ID")
	}
	stat, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return p, errResourceProcessAbsent
	}
	if err != nil {
		return p, err
	}
	text := string(stat)
	left, right := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	if left < 1 || right <= left || strings.TrimSpace(text[:left]) != strconv.Itoa(pid) {
		return p, errors.New("invalid process stat identity")
	}
	fields := strings.Fields(text[right+1:])
	if len(fields) < 20 {
		return p, errors.New("incomplete process stat")
	}
	p.StartTicks, err = strconv.ParseUint(fields[19], 10, 64) // Linux stat field 22; comm can contain spaces/parentheses.
	if err != nil || p.StartTicks == 0 {
		return p, errors.New("invalid process start ticks")
	}
	return p, nil
}

func (v *JailerVMM) SetResourceJournal(j *ResourceJournal) {
	v.mu.Lock()
	v.resourceJournal = j
	v.mu.Unlock()
}

func (v *JailerVMM) prepareJournalLaunch(l Lease) error {
	v.mu.Lock()
	j := v.resourceJournal
	v.mu.Unlock()
	if j == nil {
		return nil
	}
	r, ok, err := j.lookup(l.Instance)
	if err != nil {
		return err
	}
	if ok {
		if !resourceLeaseMatches(r.Lease, l) {
			return errors.New("resource journal: launch lease mismatch")
		}
		if r.Process == nil {
			return nil
		}
		current, err := readResourceProcessIdentity("/proc", r.Process.PID)
		if errors.Is(err, errResourceProcessAbsent) {
			return nil
		}
		if err != nil {
			return err
		}
		if current == *r.Process {
			return errors.New("resource journal: prior process incarnation still exists")
		}
		return nil
	}
	return errors.New("resource journal: launch requires committed lease intent")
}

func (inv *restartInventory) reconcileJournal(ctx context.Context, j *ResourceJournal, procRoot string) (records, matches int, err error) {
	if j == nil {
		return 0, 0, nil
	}
	items, err := j.snapshot()
	if err != nil {
		return 0, 0, err
	}
	for _, r := range items {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		inv.slots[r.Lease.Slot] = struct{}{}
		inv.instances[r.Lease.Instance] = struct{}{}
		if r.Process == nil {
			continue
		}
		matched, err := journalProcessMatches(procRoot, r)
		if err != nil {
			return 0, 0, fmt.Errorf("resource journal process check: %w", err)
		}
		if matched {
			matches++
		}
	}
	return len(items), matches, nil
}

// A matching process is provenance, not permission to reattach, replay or kill.
func journalProcessMatches(root string, r resourceJournalRecord) (bool, error) {
	current, err := readResourceProcessIdentity(root, r.Process.PID)
	if errors.Is(err, errResourceProcessAbsent) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current != *r.Process {
		return false, nil
	}
	path := filepath.Join(root, strconv.Itoa(current.PID))
	argv, err := os.ReadFile(filepath.Join(path, "cmdline"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	args := strings.Split(string(argv), "\x00")
	id, jailer := restartProcessID(args)
	if id != r.Lease.Instance {
		return false, nil
	}
	if jailer {
		return restartJailerSlot(args) == r.Lease.Slot, nil
	}
	status, err := os.ReadFile(filepath.Join(path, "status"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return restartProcessSlot(string(status)) == r.Lease.Slot, nil
}
