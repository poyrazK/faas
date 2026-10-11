// adr: 712
package durableentity

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const entityObjectPrefix = "gregale/durable-entities/v1/entities/"
const alarmRequestPrefix = "__gregale_alarm/"

var ErrAlarmObsolete = errors.New("durable entity alarm was replaced or cleared")

// EntityPrefixLister uses delimiter pagination, so snapshots never amplify
// discovery pages. Listings are hints; the committed snapshot is authoritative.
type EntityPrefixLister interface {
	ListEntityPrefixes(context.Context, string, string, int32) (EntityPrefixPage, error)
}

type EntityPrefixPage struct {
	Prefixes   []string
	NextCursor string
}

type Alarm struct {
	Entity  ID
	Version uint64
	At      time.Time
}

type AlarmPage struct {
	Alarms     []Alarm
	NextCursor string
	Failed     int
}

// CheckAlarmDiscovery verifies that startup can list the private entity prefix.
func (m *Manager) CheckAlarmDiscovery(ctx context.Context) error {
	_, err := m.alarmPrefixes(ctx, "")
	return err
}

func (m *Manager) alarmPrefixes(ctx context.Context, cursor string) (EntityPrefixPage, error) {
	listCtx, cancel := context.WithTimeout(ctx, api.DurableEntityAlarmReadTimeout)
	defer cancel()
	return m.entityPrefixes(listCtx, cursor, api.DurableEntityAlarmScanPageSize)
}

func (m *Manager) entityPrefixes(ctx context.Context, cursor string, limit int32) (EntityPrefixPage, error) {
	lister, ok := m.store.(EntityPrefixLister)
	if !ok {
		return EntityPrefixPage{}, ErrUnsupported
	}
	if len(cursor) > api.MaxObjectS3ListCursorBytes {
		return EntityPrefixPage{}, ErrInvalid
	}
	page, err := lister.ListEntityPrefixes(ctx, entityObjectPrefix, cursor, limit)
	if err != nil {
		return EntityPrefixPage{}, err
	}
	if len(page.Prefixes) > int(limit) || len(page.NextCursor) > api.MaxObjectS3ListCursorBytes || page.NextCursor != "" && (page.NextCursor == cursor || len(page.Prefixes) == 0) {
		return EntityPrefixPage{}, ErrCorrupt
	}
	return page, nil
}

// ScanDueAlarms reads one bounded page, including alarms stored before delivery
// was enabled. Bad entities fail closed individually and cannot hold the cursor.
func (m *Manager) ScanDueAlarms(ctx context.Context, cursor string) (AlarmPage, error) {
	page, err := m.alarmPrefixes(ctx, cursor)
	if err != nil {
		return AlarmPage{}, err
	}
	result := AlarmPage{NextCursor: page.NextCursor}
	for _, prefix := range page.Prefixes {
		if err := ctx.Err(); err != nil {
			return AlarmPage{}, err
		}
		alarm, due, err := m.alarmAtPrefix(ctx, prefix)
		if err != nil {
			result.Failed++
		} else if due {
			result.Alarms = append(result.Alarms, alarm)
		}
	}
	return result, nil
}

func (m *Manager) alarmAtPrefix(ctx context.Context, prefix string) (Alarm, bool, error) {
	readCtx, cancel := context.WithTimeout(ctx, api.DurableEntityAlarmReadTimeout)
	defer cancel()
	value, err := m.manifestAtPrefix(readCtx, prefix)
	if err != nil {
		return Alarm{}, false, err
	}
	state, err := m.readSnapshot(readCtx, value)
	if err != nil {
		return Alarm{}, false, err
	}
	if state.AlarmAt == nil || m.now().Before(*state.AlarmAt) {
		return Alarm{}, false, nil
	}
	return Alarm{Entity: state.ID, Version: state.Version, At: *state.AlarmAt}, true, nil
}

func (m *Manager) manifestAtPrefix(ctx context.Context, prefix string) (manifest, error) {
	hash := strings.TrimSuffix(strings.TrimPrefix(prefix, entityObjectPrefix), "/")
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != sha256HexLength/2 || prefix != entityObjectPrefix+hash+"/" {
		return manifest{}, ErrCorrupt
	}
	body, etag, err := m.store.Get(ctx, prefix+"manifest.json", api.MaxDurableEntityManifestBytes)
	if err != nil {
		return manifest{}, err
	}
	var value manifest
	if len(body) > api.MaxDurableEntityManifestBytes || etag == "" || json.Unmarshal(body, &value) != nil || !value.ID.valid() || value.ID.prefix() != prefix || !validManifest(value.ID, value) {
		return manifest{}, ErrCorrupt
	}
	return value, nil
}

func IsAlarmRequestID(id string) bool { return strings.HasPrefix(id, alarmRequestPrefix) }

// InvokeAlarm consumes one observed due state version. Changed or cancelled
// alarms are skipped under ownership before guest dispatch. A successful alarm
// commits state, its receipt and its replacement/cleared deadline atomically.
// Callbacks may run more than once after failures and must avoid external effects.
func (m *Manager) InvokeAlarm(ctx context.Context, alarm Alarm, owner string, handler func(context.Context, View) (Transition, error)) (Result, error) {
	if !alarm.Entity.valid() || alarm.Version == 0 || !validAlarm(&alarm.At) || m.now().Before(alarm.At) || handler == nil {
		return Result{}, ErrInvalid
	}
	request := AlarmRequest(alarm)
	return m.invoke(ctx, alarm.Entity, owner, request, func(ctx context.Context, view View) (Transition, error) {
		if view.Version != alarm.Version || view.AlarmAt == nil || !view.AlarmAt.Equal(alarm.At) || m.now().Before(*view.AlarmAt) {
			return Transition{}, ErrAlarmObsolete
		}
		return handler(ctx, view)
	})
}

// AlarmRequest is deterministic across workers/restarts. Its namespace cannot
// be submitted through ordinary Invoke calls.
func AlarmRequest(alarm Alarm) Request {
	body, _ := json.Marshal(struct {
		Type    string    `json:"type"`
		At      time.Time `json:"scheduled_at"`
		Version uint64    `json:"state_version"`
	}{"alarm", alarm.At.UTC(), alarm.Version})
	return Request{ID: fmt.Sprintf("%s%d", alarmRequestPrefix, alarm.Version), Payload: body}
}
