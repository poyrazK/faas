package realtime

import (
	"container/heap"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Absorb moves the callbacks pending in the outbox rooted at dir, and its
// dead letters, into q. A two-generation hand-off leaves the old generation's
// outbox in its own directory (docs/adr/1736-zero-downtime-daemon-restarts.md);
// the generation that adopts it delivers those callbacks in their per-connection
// order. Event IDs are kept, so a callback already in q is not added twice.
// ErrCallbackOutboxFull stops the move; what is left stays in dir.
func (q *CallbackOutbox) Absorb(dir string) (int, error) {
	src, err := NewCallbackOutbox(CallbackOutboxConfig{Root: dir, DeadLetterMaxBytes: q.deadMaxBytes})
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(src.items))
	for id := range src.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	moved := 0
	for _, id := range ids {
		ok, err := q.absorbItem(src.items[id])
		if err != nil {
			return moved, err
		}
		if ok {
			moved++
		}
	}
	if err := q.absorbDeadLetters(src); err != nil {
		return moved, err
	}
	if err := syncCallbackOutboxDir(q.root); err != nil {
		return moved, err
	}
	return moved, syncCallbackOutboxDir(dir)
}

func (q *CallbackOutbox) absorbItem(from *callbackOutboxItem) (bool, error) {
	id := from.record.Event.ID
	q.mu.Lock()
	defer q.mu.Unlock()
	_, pending := q.items[id]
	_, dead := q.deadIDs[id]
	if pending || dead {
		if err := os.Remove(from.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return false, nil
	}
	if q.bytes+from.size > q.maxBytes {
		return false, ErrCallbackOutboxFull
	}
	path := filepath.Join(q.root, id+".json")
	if err := os.Rename(from.path, path); err != nil {
		return false, fmt.Errorf("realtime: adopt callback outbox item %q: %w", id, err)
	}
	item := &callbackOutboxItem{
		record: from.record, path: path, size: from.size, enqueuedAt: from.enqueuedAt,
		readyIndex: -1, retryIndex: -1,
	}
	q.items[id] = item
	heap.Push(&q.pendingAge, item)
	q.addConnectionItemLocked(item)
	q.bytes += item.size
	return true, nil
}

func (q *CallbackOutbox) absorbDeadLetters(src *CallbackOutbox) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, dead := range src.dead {
		from := filepath.Join(src.deadRoot, dead.id+".json")
		if _, exists := q.deadIDs[dead.id]; exists {
			if err := os.Remove(from); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if err := os.Rename(from, filepath.Join(q.deadRoot, dead.id+".json")); err != nil {
			return fmt.Errorf("realtime: adopt callback dead letter %q: %w", dead.id, err)
		}
		heap.Push(&q.dead, dead)
		q.deadIDs[dead.id] = struct{}{}
		q.deadBytes += dead.size
	}
	if err := syncCallbackOutboxDir(q.deadRoot); err != nil {
		return err
	}
	return q.pruneDeadLetters()
}
