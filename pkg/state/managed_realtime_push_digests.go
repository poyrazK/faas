package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const ManagedRealtimePushDigestMaxMembers = 128

// A digest is a frozen set of ledger rows sharing a durable ID and lease.
// ACKs cancel individual members, without cancelling other members of the set.
type ManagedRealtimePushDigestStore interface {
	AcquireManagedRealtimePushDigest(context.Context, string, string) (*ManagedRealtimePushDelivery, error)
	PrepareManagedRealtimePushDigest(context.Context, string, string) (*ManagedRealtimePushDelivery, error)
	CompleteManagedRealtimePushDigest(context.Context, string, string, int, string, bool, bool) error
}

func digestSchedule(p api.RealtimeNotificationPreferences, j ManagedRealtimePushDelivery) (time.Time, bool) {
	base := j.CreatedAt
	if j.NotBefore.After(base) {
		base = j.NotBefore
	}
	if j.Priority == "urgent" && p.AllowUrgentBypass {
		return base, false
	}
	if j.DigestID != "" {
		ready := j.DigestAt
		if base.After(ready) {
			ready = base
		}
		if j.Priority == "urgent" && p.DigestIntervalSeconds > 0 {
			interval := time.Duration(p.DigestIntervalSeconds) * time.Second
			end := base.Truncate(interval).Add(interval)
			if end.After(ready) {
				ready = end
			}
		}
		return ready, true
	}
	ready := base
	group := false
	if rateLimitApplies(p, j) {
		// Due alerts in the same quota window can share a summary.
		ready = base.Truncate(time.Duration(p.RateLimit.WindowSeconds) * time.Second)
		group = true
	}
	if p.DigestIntervalSeconds > 0 {
		interval := time.Duration(p.DigestIntervalSeconds) * time.Second
		ready = base.Truncate(interval).Add(interval)
		group = true
	}
	if p.SummarizeQuietHours == nil || *p.SummarizeQuietHours {
		if end, allowed := p.NextPushTime(base, j.Category, j.Device); allowed && end.After(base) {
			if end.After(ready) {
				ready = end
			}
			group = true
		}
	}
	if end, allowed := p.NextPushTime(ready, j.Category, j.Device); allowed && end.After(ready) {
		ready = end
	}
	return ready, group
}
func sameDigestGroup(a, b ManagedRealtimePushDelivery) bool {
	return a.EndpointID == b.EndpointID && a.Principal == b.Principal && a.Device == b.Device && a.Version == b.Version && a.Provider == b.Provider && a.Priority == b.Priority && a.Category == b.Category && a.GroupKey == b.GroupKey
}
func digestPayload(members []ManagedRealtimePushDelivery) *ManagedRealtimePushDelivery {
	if len(members) == 0 {
		return nil
	}
	latest := members[0]
	ids := map[string]bool{}
	for _, j := range members {
		ids[j.MessageID] = true
		if j.Sequence > latest.Sequence {
			latest = j
		}
	}
	latest.DigestCount = len(ids)
	return &latest
}
func digestCompletion(j ManagedRealtimePushDelivery, statusCode int, code string, retry, invalid bool, now time.Time) ManagedRealtimePushDelivery {
	j.Status = "failed"
	j.NextAttempt = now
	if statusCode >= 200 && statusCode < 300 {
		j.Status = "sent"
	} else if retry && !invalid && j.Attempts > 0 && j.Attempts <= len(pushRetryDelays) && now.Add(pushRetryDelays[j.Attempts-1]).Before(j.ExpiresAt) {
		j.Status = "pending"
		j.NextAttempt = now.Add(pushRetryDelays[j.Attempts-1])
	}
	j.StatusCode = statusCode
	j.Code = code
	j.UpdatedAt = now
	j.Lease = ""
	j.LeaseUntil = time.Time{}
	return j
}
func (m *MemStore) pushPreferencesLocked(j ManagedRealtimePushDelivery) api.RealtimeNotificationPreferences {
	p, ok := m.managedRealtimePushPreferences[managedRealtimeHistoryKey{endpointID: j.EndpointID, channel: j.Principal}]
	if !ok {
		return api.DefaultRealtimeNotificationPreferences()
	}
	return p
}
func (m *MemStore) AcquireManagedRealtimePushDigest(ctx context.Context, id, lease string) (*ManagedRealtimePushDelivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	seed := m.managedRealtimePushDeliveries[id]
	if seed.Status != "sending" || seed.Lease != lease || !seed.LeaseUntil.After(now) {
		return nil, nil
	}
	p := m.pushPreferencesLocked(seed)
	updated, send := pushPreferenceDecision(seed, p, now)
	if !send && seed.DigestID == "" {
		m.setPushDeliveryLocked(id, updated)
		return nil, nil
	}
	at, group := digestSchedule(p, seed)
	candidates := []ManagedRealtimePushDelivery{}
	for _, j := range m.managedRealtimePushDeliveries {
		if j.Status != "pending" && j.Status != "sending" || !j.ExpiresAt.After(now) || j.Attempts >= 7 && j.Status == "pending" {
			continue
		}
		if seed.DigestID != "" {
			if j.DigestID == seed.DigestID {
				candidates = append(candidates, j)
			}
			continue
		}
		if j.DigestID != "" || !sameDigestGroup(seed, j) || j.NextAttempt.After(now) {
			continue
		}
		ready, _ := digestSchedule(p, j)
		if j.ID == seed.ID || group && ready.Equal(at) {
			candidates = append(candidates, j)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ID == seed.ID {
			return candidates[j].ID != seed.ID
		}
		if candidates[j].ID == seed.ID {
			return false
		}
		return candidates[i].Sequence < candidates[j].Sequence
	})
	if len(candidates) > ManagedRealtimePushDigestMaxMembers {
		candidates = candidates[:ManagedRealtimePushDigestMaxMembers]
	}
	digestID := seed.DigestID
	if digestID == "" {
		digestID = uuid.NewString()
	}
	for i := range candidates {
		j := &candidates[i]
		if j.ID != seed.ID && (j.Status == "pending" || j.DigestID != "") {
			j.Attempts++
		}
		j.DigestID = digestID
		j.DigestAt = at
		j.Status = "sending"
		j.Lease = lease
		j.LeaseUntil = now.Add(time.Minute)
		j.UpdatedAt = now
		m.setPushDeliveryLocked(j.ID, *j)
	}
	payload := digestPayload(candidates)
	if payload == nil {
		return nil, nil
	}
	for _, j := range candidates {
		j.DigestCount = payload.DigestCount
		m.setPushDeliveryLocked(j.ID, j)
	}
	d, c, _ := m.pushDeviceLocked(*payload)
	payload.Config = append([]byte(nil), c.Sealed...)
	payload.Target = append([]byte(nil), d.Sealed...)
	return payload, nil
}
func (m *MemStore) PrepareManagedRealtimePushDigest(ctx context.Context, digestID, lease string) (*ManagedRealtimePushDelivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	members := []ManagedRealtimePushDelivery{}
	for id, j := range m.managedRealtimePushDeliveries {
		if j.DigestID != digestID || j.Status != "sending" || j.Lease != lease || !j.LeaseUntil.After(now) {
			continue
		}
		if !j.ExpiresAt.After(now) {
			m.cancelPushLocked(id, j, "expired")
			continue
		}
		updated, send := pushPreferenceDecision(j, m.pushPreferencesLocked(j), now)
		if !send {
			m.setPushDeliveryLocked(id, updated)
		} else {
			members = append(members, j)
		}
	}
	payload := digestPayload(members)
	if payload == nil {
		return nil, nil
	}
	if next := m.reservePushRateLocked(m.pushPreferencesLocked(*payload), *payload, now); next.After(now) {
		for _, j := range members {
			m.setPushDeliveryLocked(j.ID, deferPushRate(j, next, now))
		}
		return nil, nil
	}
	for _, j := range members {
		j.DigestCount = payload.DigestCount
		m.setPushDeliveryLocked(j.ID, j)
	}
	d, c, _ := m.pushDeviceLocked(*payload)
	payload.Config = append([]byte(nil), c.Sealed...)
	payload.Target = append([]byte(nil), d.Sealed...)
	return payload, nil
}
func (m *MemStore) CompleteManagedRealtimePushDigest(ctx context.Context, digestID, lease string, statusCode int, code string, retry, invalid bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for id, j := range m.managedRealtimePushDeliveries {
		if j.DigestID != digestID || j.Status != "sending" || j.Lease != lease {
			continue
		}
		if invalid {
			key := managedRealtimeDurableCursorKey{endpointID: j.EndpointID, principal: j.Principal, subscription: j.Device, channel: j.Principal}
			d := m.managedRealtimePushDevices[key]
			if d.Version == j.Version && d.Provider == j.Provider {
				d.Enabled = false
				d.Sealed = nil
				d.UpdatedAt = now
				m.managedRealtimePushDevices[key] = d
			}
		}
		m.setPushDeliveryLocked(id, digestCompletion(j, statusCode, code, retry, invalid, now))
	}
	return nil
}
func digestLockKey(j ManagedRealtimePushDelivery) string {
	raw, _ := json.Marshal([]any{j.EndpointID, j.Principal, j.Device, j.Version, j.Provider, j.Priority, j.Category, j.GroupKey})
	return "realtime-digest:" + string(raw)
}

// Notification text is bounded and contains no inbox message bodies.
func ManagedRealtimePushDigestBody(j ManagedRealtimePushDelivery) string {
	noun := "new notifications"
	switch j.Category {
	case "chat":
		noun = "new messages"
	case "jobs":
		noun = "job updates"
	}
	body := fmt.Sprintf("%d %s", j.DigestCount, noun)
	if j.GroupLabel != "" {
		body += " in " + j.GroupLabel
	}
	return body
}

var _ ManagedRealtimePushDigestStore = (*MemStore)(nil)

func pushPriorityRank(priority string) int {
	switch priority {
	case "urgent":
		return 0
	case "low":
		return 2
	default:
		return 1
	}
}
func pushPriorityPreferences(p api.RealtimeNotificationPreferences, j ManagedRealtimePushDelivery) api.RealtimeNotificationPreferences {
	if j.Priority == "urgent" && p.AllowUrgentBypass {
		p.QuietHours = nil
		p.DigestIntervalSeconds = 0
		summary := false
		p.SummarizeQuietHours = &summary
	}
	return p
}
