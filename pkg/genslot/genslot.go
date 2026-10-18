// Package genslot gives each running generation of a daemon its own
// directory for a durable queue, so a successor started by a hand-off
// (docs/adr/2026101801-zero-downtime-daemon-restarts.md) and the old generation
// still finishing its requests never write the same files.
//
// Slot 0 is the queue's root itself, the layout every release before the
// hand-off used; slot n is root/.generation-n. A generation holds an flock on
// root/.generation-n.lock for its lifetime, so the kernel releases the slot
// when the process exits, however it exits. Records a generation leaves
// behind are adopted by a running generation (AdoptOrphans), and a generation
// outside slot 0 moves its records home on a clean exit (ReturnHome), so a
// release that predates slots finds them where it looks.
package genslot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// MaxGenerations bounds the generations that hold one queue at once: the one
// serving, the one draining, and briefly a third while a hand-off stops a
// predecessor that was still draining.
const MaxGenerations = 4

// AdoptInterval is how often a running generation looks for records an
// exited one left in its slot.
const AdoptInterval = 10 * time.Second

// Slot is one generation's claim on a queue root.
type Slot struct {
	root  string
	index int
	lock  *flock.Flock
}

func lockPath(root string, index int) string {
	return filepath.Join(root, fmt.Sprintf(".generation-%d.lock", index))
}

func slotDir(root string, index int) string {
	if index == 0 {
		return root
	}
	return filepath.Join(root, fmt.Sprintf(".generation-%d", index))
}

// Acquire claims the lowest free slot under root and creates its directory.
func Acquire(root string) (*Slot, error) {
	if root == "" {
		return nil, errors.New("genslot: empty root")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("genslot: create %s: %w", root, err)
	}
	for index := range MaxGenerations {
		lock := flock.New(lockPath(root, index), flock.SetPermissions(0o600))
		held, err := lock.TryLock()
		if err != nil {
			return nil, fmt.Errorf("genslot: lock slot %d of %s: %w", index, root, err)
		}
		if !held {
			continue
		}
		if err := os.MkdirAll(slotDir(root, index), 0o750); err != nil {
			_ = lock.Unlock()
			return nil, fmt.Errorf("genslot: create slot %d of %s: %w", index, root, err)
		}
		return &Slot{root: root, index: index, lock: lock}, nil
	}
	return nil, fmt.Errorf("genslot: all %d generation slots of %s are held", MaxGenerations, root)
}

// Index is the slot number; 0 is the queue root.
func (s *Slot) Index() int { return s.index }

// Dir is where this generation keeps its queue.
func (s *Slot) Dir() string { return slotDir(s.root, s.index) }

// Release gives the slot up. The process exiting releases it too.
func (s *Slot) Release() error { return s.lock.Unlock() }

// AdoptOrphans passes absorb the directory of every other slot no running
// generation holds, holding that slot's lock meanwhile so no generation
// starts in it. absorb moves the records into this generation's queue and
// removes them from dir. It returns the first error after trying every slot.
func (s *Slot) AdoptOrphans(absorb func(dir string) error) error {
	var first error
	for index := range MaxGenerations {
		if index == s.index {
			continue
		}
		dir := slotDir(s.root, index)
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			continue
		}
		lock := flock.New(lockPath(s.root, index), flock.SetPermissions(0o600))
		held, err := lock.TryLock()
		if err != nil || !held {
			if err != nil && first == nil {
				first = fmt.Errorf("genslot: lock slot %d of %s: %w", index, s.root, err)
			}
			continue
		}
		err = absorb(dir)
		if err == nil && index != 0 {
			err = os.RemoveAll(dir)
		}
		_ = lock.Unlock()
		if err != nil && first == nil {
			first = fmt.Errorf("genslot: adopt slot %d of %s: %w", index, s.root, err)
		}
	}
	return first
}

// ReturnHome moves this generation's records into slot 0 and releases its
// slot. It waits up to wait for a generation still holding slot 0 to exit;
// if one outlives that, the records stay for a later generation to adopt.
// merge moves the records in from into the queue at home and removes them
// from from. Nothing moves for a generation already in slot 0.
func (s *Slot) ReturnHome(wait time.Duration, merge func(from, home string) error) error {
	defer func() { _ = s.Release() }()
	if s.index == 0 {
		return nil
	}
	home := flock.New(lockPath(s.root, 0), flock.SetPermissions(0o600))
	deadline := time.Now().Add(wait)
	for {
		held, err := home.TryLock()
		if err != nil {
			return fmt.Errorf("genslot: lock slot 0 of %s: %w", s.root, err)
		}
		if held {
			break
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer func() { _ = home.Unlock() }()
	if err := merge(s.Dir(), s.root); err != nil {
		return fmt.Errorf("genslot: return slot %d of %s home: %w", s.index, s.root, err)
	}
	return os.RemoveAll(s.Dir())
}
