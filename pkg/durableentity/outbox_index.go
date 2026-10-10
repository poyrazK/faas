// adr: 933
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const outboxIndexPrefix = "gregale/durable-entities/v1/outbox-index/"

type OutboxWork struct {
	Entity    ID
	MessageID string
}

type OutboxPage struct {
	Work       []OutboxWork
	NextCursor string
	Failed     int
}

type outboxIndexEntry struct {
	Work     OutboxWork `json:"work"`
	DueAt    time.Time  `json:"due_at"`
	Attempts int        `json:"attempts"`
}

func (entry outboxIndexEntry) key() string {
	return fmt.Sprintf("%s%s/%s-%02d.json", outboxIndexPrefix, entry.DueAt.UTC().Format("20060102T150405.000000000Z"), entry.Work.MessageID, entry.Attempts)
}

func outboxHint(value manifest, state snapshot) (outboxIndexEntry, bool) {
	if len(state.Outbox) == 0 {
		return outboxIndexEntry{}, false
	}
	entry := outboxIndexEntry{Work: OutboxWork{Entity: state.ID, MessageID: state.Outbox[0].ID}, DueAt: time.Unix(0, 0).UTC()}
	if d := value.OutboxDelivery; d != nil {
		if d.Attempts == api.MaxDurableEntityOutboxAttempts {
			return outboxIndexEntry{}, false
		}
		entry.DueAt, entry.Attempts = d.NextAttemptAt, d.Attempts
	}
	return entry, true
}

func (m *Manager) publishOutboxHint(ctx context.Context, value manifest, state snapshot) {
	entry, ok := outboxHint(value, state)
	if !ok {
		return
	}
	body, err := json.Marshal(entry)
	if err != nil || len(body) > api.MaxDurableEntityOutboxIndexBytes {
		return
	}
	indexCtx, cancel := context.WithTimeout(ctx, api.DurableEntityOutboxIndexTimeout)
	defer cancel()
	// This immutable hint is never commit authority. Reconciliation repairs a
	// missing write; a publication ACK cannot be revoked by an index failure.
	_, _ = m.store.Put(indexCtx, entry.key(), body, "")
}

func (m *Manager) CheckOutboxDiscovery(ctx context.Context) error {
	if _, err := m.entityPrefixes(ctx, "", api.DurableEntityOutboxScanPageSize); err != nil {
		return err
	}
	store, ok := m.store.(CleanupStore)
	if !ok {
		return ErrUnsupported
	}
	page, err := store.ListEntityObjects(ctx, outboxIndexPrefix, "", api.DurableEntityOutboxScanPageSize)
	if err != nil {
		return err
	}
	if !validOutboxIndexPage("", page) {
		return ErrCorrupt
	}
	key := "gregale/durable-entities/v1/probes/outbox/" + uuid.NewString()
	if _, err := m.store.Put(ctx, key, []byte(`{"probe":true}`), ""); err != nil {
		return writeFailure("create outbox probe", err)
	}
	if err := store.DeleteEntityObject(ctx, key); err != nil {
		return err
	}
	if _, _, err := m.store.Get(ctx, key, api.MaxDurableEntityOutboxIndexBytes); !errors.Is(err, ErrNotFound) {
		return errors.Join(ErrUnsupported, err)
	}
	return nil
}

// ScanDueOutbox rotates through bounded entity pages, including messages
// committed before the relay was enabled. LIST only discovers candidate roots.
func (m *Manager) ScanDueOutbox(ctx context.Context, cursor string) (OutboxPage, error) {
	page, err := m.entityPrefixes(ctx, cursor, api.DurableEntityOutboxScanPageSize)
	if err != nil {
		return OutboxPage{}, err
	}
	result := OutboxPage{NextCursor: page.NextCursor}
	for _, prefix := range page.Prefixes {
		if err := ctx.Err(); err != nil {
			return OutboxPage{}, err
		}
		readCtx, cancel := context.WithTimeout(ctx, api.DurableEntityOutboxReadTimeout)
		entry, due, err := m.outboxAtPrefix(readCtx, prefix)
		cancel()
		if err != nil {
			result.Failed++
		} else if due {
			result.Work = append(result.Work, entry.Work)
		}
	}
	return result, nil
}

func (m *Manager) outboxAtPrefix(ctx context.Context, prefix string) (outboxIndexEntry, bool, error) {
	value, err := m.manifestAtPrefix(ctx, prefix)
	if err != nil {
		return outboxIndexEntry{}, false, err
	}
	state, err := m.readSnapshot(ctx, value)
	if err != nil {
		return outboxIndexEntry{}, false, err
	}
	m.publishOutboxHint(ctx, value, state)
	entry, ok := outboxHint(value, state)
	return entry, ok && !m.now().Before(entry.DueAt), nil
}

func validOutboxIndexPage(cursor string, page CleanupObjects) bool {
	if len(page.Keys) > api.DurableEntityOutboxScanPageSize || len(page.NextCursor) > api.MaxObjectS3ListCursorBytes || page.NextCursor != "" && (page.NextCursor == cursor || len(page.Keys) == 0) {
		return false
	}
	previous := ""
	for _, key := range page.Keys {
		if !strings.HasPrefix(key, outboxIndexPrefix) || key <= previous {
			return false
		}
		previous = key
	}
	return true
}

// ScanIndexedDueOutbox checks every hint against authenticated committed state.
// It skips future hints and prunes obsolete entries; reconciliation is separate.
func (m *Manager) ScanIndexedDueOutbox(ctx context.Context, cursor string) (OutboxPage, error) {
	store, ok := m.store.(CleanupStore)
	if !ok {
		return OutboxPage{}, ErrUnsupported
	}
	if len(cursor) > api.MaxObjectS3ListCursorBytes {
		return OutboxPage{}, ErrInvalid
	}
	page, err := store.ListEntityObjects(ctx, outboxIndexPrefix, cursor, api.DurableEntityOutboxScanPageSize)
	if err != nil {
		return OutboxPage{}, err
	}
	if !validOutboxIndexPage(cursor, page) {
		return OutboxPage{}, ErrCorrupt
	}
	result := OutboxPage{NextCursor: page.NextCursor}
	for _, key := range page.Keys {
		readCtx, cancel := context.WithTimeout(ctx, api.DurableEntityOutboxReadTimeout)
		work, due, future, err := m.indexedOutbox(readCtx, store, key)
		cancel()
		if ctx.Err() != nil {
			return OutboxPage{}, ctx.Err()
		}
		if err != nil {
			result.Failed++
		} else if future {
			result.NextCursor = ""
			break
		} else if due {
			result.Work = append(result.Work, work)
		}
	}
	return result, nil
}

func (m *Manager) indexedOutbox(ctx context.Context, store CleanupStore, key string) (OutboxWork, bool, bool, error) {
	body, version, err := m.store.Get(ctx, key, api.MaxDurableEntityOutboxIndexBytes)
	if errors.Is(err, ErrNotFound) {
		return OutboxWork{}, false, false, nil
	}
	if err != nil {
		return OutboxWork{}, false, false, err
	}
	var entry outboxIndexEntry
	if len(body) > api.MaxDurableEntityOutboxIndexBytes || version == "" || json.Unmarshal(body, &entry) != nil || !entry.Work.Entity.valid() ||
		!validUUID(entry.Work.MessageID) || !validAlarm(&entry.DueAt) || entry.Attempts < 0 || entry.Attempts >= api.MaxDurableEntityOutboxAttempts || entry.key() != key {
		_ = store.DeleteEntityObject(ctx, key)
		return OutboxWork{}, false, false, ErrCorrupt
	}
	if m.now().Before(entry.DueAt) {
		return OutboxWork{}, false, true, nil
	}
	current, due, err := m.outboxAtPrefix(ctx, entry.Work.Entity.prefix())
	if errors.Is(err, ErrNotFound) && !errors.Is(err, ErrCorrupt) {
		return OutboxWork{}, false, false, store.DeleteEntityObject(ctx, key)
	}
	if err != nil {
		return OutboxWork{}, false, false, err
	}
	if !due || current.Work != entry.Work || current.Attempts != entry.Attempts || !current.DueAt.Equal(entry.DueAt) {
		return OutboxWork{}, false, false, store.DeleteEntityObject(ctx, key)
	}
	return entry.Work, true, false, nil
}
