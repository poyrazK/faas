// adr: 712
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

const alarmIndexPrefix = "gregale/durable-entities/v1/alarm-index/"

func (m *Manager) checkAlarmIndex(ctx context.Context) error {
	store, ok := m.store.(CleanupStore)
	if !ok {
		return ErrUnsupported
	}
	page, err := store.ListEntityObjects(ctx, alarmIndexPrefix, "", api.DurableEntityAlarmScanPageSize)
	if err != nil {
		return err
	}
	if !validAlarmIndexPage("", page) {
		return ErrCorrupt
	}
	key := "gregale/durable-entities/v1/probes/alarms/" + uuid.NewString()
	if _, err := m.store.Put(ctx, key, []byte(`{"probe":true}`), ""); err != nil {
		return writeFailure("create alarm probe", err)
	}
	if err := store.DeleteEntityObject(ctx, key); err != nil {
		return err
	}
	if _, _, err := m.store.Get(ctx, key, api.MaxDurableEntityAlarmIndexBytes); !errors.Is(err, ErrNotFound) {
		return errors.Join(ErrUnsupported, err)
	}
	return nil
}

// Index objects are disposable, immutable hints. No hint is ownership or proof
// of a committed alarm. Lexical time ordering skips future entries on each pass.
type alarmIndexEntry struct {
	Alarm    Alarm     `json:"alarm"`
	DueAt    time.Time `json:"due_at"`
	Attempts int       `json:"attempts"`
}

func (entry alarmIndexEntry) key() string {
	hash := strings.TrimSuffix(strings.TrimPrefix(entry.Alarm.Entity.prefix(), entityObjectPrefix), "/")
	return fmt.Sprintf("%s%s/%s-%020d-%02d.json", alarmIndexPrefix, entry.DueAt.UTC().Format("20060102T150405.000000000Z"), hash, entry.Alarm.Version, entry.Attempts)
}

func (m *Manager) publishAlarmHint(ctx context.Context, value manifest, state snapshot) {
	if state.AlarmAt != nil {
		_ = m.putAlarmHint(ctx, Alarm{Entity: state.ID, Version: state.Version, At: *state.AlarmAt}, value.AlarmDelivery)
	}
}

func (m *Manager) putAlarmHint(ctx context.Context, alarm Alarm, delivery *alarmDelivery) error {
	entry := alarmIndexEntry{Alarm: alarm, DueAt: alarm.At.UTC()}
	entry.Alarm.At = alarm.At.UTC()
	if delivery != nil {
		if delivery.Attempts >= api.MaxDurableEntityAlarmAttempts {
			return nil
		}
		entry.Attempts, entry.DueAt = delivery.Attempts, delivery.NextAttemptAt
	}
	body, err := json.Marshal(entry)
	if err != nil || len(body) > api.MaxDurableEntityAlarmIndexBytes {
		return ErrLimit
	}
	indexCtx, cancel := context.WithTimeout(ctx, api.DurableEntityAlarmIndexTimeout)
	defer cancel()
	version, err := m.store.Put(indexCtx, entry.key(), body, "")
	if errors.Is(err, ErrConflict) {
		return nil // The same immutable hint already exists.
	}
	if err != nil {
		return writeFailure("publish alarm hint", err)
	}
	if version == "" {
		return ErrUncertain
	}
	return nil
}

// ScanIndexedDueAlarms visits one bounded time-ordered page. Missing hints are
// repaired by ScanDueAlarms's independent rotating entity scan. Every candidate
// is checked against committed state here and again under ownership at dispatch.
func (m *Manager) ScanIndexedDueAlarms(ctx context.Context, cursor string) (AlarmPage, error) {
	store, ok := m.store.(CleanupStore)
	if !ok {
		return AlarmPage{}, ErrUnsupported
	}
	if len(cursor) > api.MaxObjectS3ListCursorBytes {
		return AlarmPage{}, ErrInvalid
	}
	page, err := store.ListEntityObjects(ctx, alarmIndexPrefix, cursor, api.DurableEntityAlarmScanPageSize)
	if err != nil {
		return AlarmPage{}, err
	}
	if !validAlarmIndexPage(cursor, page) {
		return AlarmPage{}, ErrCorrupt
	}
	result := AlarmPage{NextCursor: page.NextCursor}
	for _, key := range page.Keys {
		readCtx, cancel := context.WithTimeout(ctx, api.DurableEntityAlarmReadTimeout)
		alarm, due, future, err := m.indexedAlarm(readCtx, store, key)
		cancel()
		if ctx.Err() != nil {
			return AlarmPage{}, ctx.Err()
		}
		if err != nil {
			result.Failed++
		} else if future {
			result.NextCursor = ""
			break
		} else if due {
			result.Alarms = append(result.Alarms, alarm)
		}
	}
	return result, nil
}

func validAlarmIndexPage(cursor string, page CleanupObjects) bool {
	if len(page.Keys) > api.DurableEntityAlarmScanPageSize || len(page.NextCursor) > api.MaxObjectS3ListCursorBytes || page.NextCursor != "" && (page.NextCursor == cursor || len(page.Keys) == 0) {
		return false
	}
	previous := ""
	for _, key := range page.Keys {
		if !strings.HasPrefix(key, alarmIndexPrefix) || key <= previous {
			return false
		}
		previous = key
	}
	return true
}

func (m *Manager) readAlarmIndex(ctx context.Context, key string) (alarmIndexEntry, error) {
	body, version, err := m.store.Get(ctx, key, api.MaxDurableEntityAlarmIndexBytes)
	if err != nil {
		return alarmIndexEntry{}, err
	}
	var entry alarmIndexEntry
	if len(body) > api.MaxDurableEntityAlarmIndexBytes || version == "" || json.Unmarshal(body, &entry) != nil ||
		!entry.Alarm.Entity.valid() || entry.Alarm.Version == 0 || !validAlarm(&entry.Alarm.At) || !validAlarm(&entry.DueAt) ||
		entry.DueAt.Before(entry.Alarm.At) || entry.Attempts < 0 || entry.Attempts >= api.MaxDurableEntityAlarmAttempts || entry.key() != key {
		return alarmIndexEntry{}, ErrCorrupt
	}
	return entry, nil
}

func (m *Manager) indexedAlarm(ctx context.Context, store CleanupStore, key string) (Alarm, bool, bool, error) {
	entry, err := m.readAlarmIndex(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return Alarm{}, false, false, nil // Another worker pruned this hint.
	}
	if errors.Is(err, ErrCorrupt) {
		_ = store.DeleteEntityObject(ctx, key) // Reconciliation recreates valid hints.
	}
	if err != nil {
		return Alarm{}, false, false, err
	}
	if m.now().Before(entry.DueAt) {
		return Alarm{}, false, true, nil
	}
	value, _, err := m.readManifest(ctx, entry.Alarm.Entity)
	if errors.Is(err, ErrNotFound) {
		return Alarm{}, false, false, store.DeleteEntityObject(ctx, key)
	}
	if err != nil {
		return Alarm{}, false, false, err
	}
	state, err := m.readSnapshot(ctx, value)
	if err != nil {
		return Alarm{}, false, false, err
	}
	if !currentAlarmIndex(entry, value, state) {
		return Alarm{}, false, false, store.DeleteEntityObject(ctx, key)
	}
	return entry.Alarm, true, false, nil
}

func currentAlarmIndex(entry alarmIndexEntry, value manifest, state snapshot) bool {
	if state.Version != entry.Alarm.Version || state.AlarmAt == nil || !state.AlarmAt.Equal(entry.Alarm.At) {
		return false
	}
	if value.AlarmDelivery == nil {
		return entry.Attempts == 0 && entry.DueAt.Equal(entry.Alarm.At)
	}
	delivery := value.AlarmDelivery
	return delivery.Attempts < api.MaxDurableEntityAlarmAttempts && entry.Attempts == delivery.Attempts && entry.DueAt.Equal(delivery.NextAttemptAt)
}
