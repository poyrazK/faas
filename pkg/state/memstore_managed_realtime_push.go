package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

type managedRealtimePushKey struct{ endpointID, provider string }

func (m *MemStore) PutManagedRealtimePushProvider(ctx context.Context, p ManagedRealtimePushProvider) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validPushProvider(p.Provider) || len(p.Sealed) == 0 || len(p.Sealed) > 32768 {
		return ErrManagedRealtimeHistoryInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[p.EndpointID]; !ok {
		return ErrNotFound
	}
	p.Sealed = append([]byte(nil), p.Sealed...)
	p.UpdatedAt = time.Now().UTC()
	m.managedRealtimePushProviders[managedRealtimePushKey{p.EndpointID, p.Provider}] = p
	if !p.Enabled {
		for id, j := range m.managedRealtimePushDeliveries {
			if j.EndpointID == p.EndpointID && j.Provider == p.Provider {
				m.cancelPushLocked(id, j, "provider_disabled")
			}
		}
	}
	return nil
}
func (m *MemStore) ListManagedRealtimePushProviders(ctx context.Context, ep string) ([]ManagedRealtimePushProvider, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ManagedRealtimePushProvider{}
	for k, p := range m.managedRealtimePushProviders {
		if k.endpointID == ep {
			p.Sealed = append([]byte(nil), p.Sealed...)
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out, nil
}
func (m *MemStore) PutManagedRealtimePushDevice(ctx context.Context, d ManagedRealtimePushDevice) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := pushDeviceKey(d.EndpointID, d.Principal, d.Device)
	if err != nil {
		return err
	}
	if !validPushProvider(d.Provider) || len(d.Sealed) == 0 || len(d.Sealed) > 16384 || !validPushFingerprint(d.Fingerprint) {
		return ErrManagedRealtimeHistoryInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ep, ok := m.managedRealtimeEndpoints[d.EndpointID]; !ok || !ep.Enabled {
		return ErrNotFound
	}
	if !m.managedRealtimePushProviders[managedRealtimePushKey{d.EndpointID, d.Provider}].Enabled {
		return ErrManagedRealtimeFallbackSubscription
	}
	if _, exists := m.managedRealtimePushDevices[key]; !exists {
		total, principal := 0, 0
		for k := range m.managedRealtimePushDevices {
			if k.endpointID == d.EndpointID {
				total++
				if k.principal == key.principal {
					principal++
				}
			}
		}
		if total >= 1024 || principal >= 16 {
			return ErrManagedRealtimeDurableCursorLimit
		}
	}
	if existing, ok := m.managedRealtimePushDevices[key]; ok && existing.Enabled && existing.Provider == d.Provider && existing.Fingerprint == d.Fingerprint {
		existing.UpdatedAt = time.Now().UTC()
		m.managedRealtimePushDevices[key] = existing
		return nil
	}
	m.managedRealtimePushVersion++
	d.Version = m.managedRealtimePushVersion
	d.Principal = key.principal
	d.Enabled = true
	d.UpdatedAt = time.Now().UTC()
	d.Sealed = append([]byte(nil), d.Sealed...)
	m.managedRealtimePushDevices[key] = d
	for id, j := range m.managedRealtimePushDeliveries {
		if j.EndpointID == d.EndpointID && j.Principal == key.principal && j.Device == d.Device {
			m.cancelPushLocked(id, j, "device_rotated")
		}
	}
	return nil
}
func (m *MemStore) ListManagedRealtimePushDevices(ctx context.Context, ep, principal string) ([]ManagedRealtimePushDevice, error) {
	pk, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ManagedRealtimePushDevice{}
	for k, d := range m.managedRealtimePushDevices {
		if k.endpointID == ep && k.principal == pk {
			d.Sealed = nil
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out, nil
}
func (m *MemStore) DeleteManagedRealtimePushDevice(ctx context.Context, ep, principal, device string) error {
	key, err := pushDeviceKey(ep, principal, device)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.managedRealtimePushDevices, key)
	for id, j := range m.managedRealtimePushDeliveries {
		if j.EndpointID == ep && j.Principal == key.principal && j.Device == device {
			m.cancelPushLocked(id, j, "device_removed")
		}
	}
	return nil
}
func (m *MemStore) cancelPushLocked(id string, j ManagedRealtimePushDelivery, code string) {
	if j.Status != "pending" && j.Status != "sending" {
		return
	}
	j.Status = "cancelled"
	j.Code = code
	j.UpdatedAt = time.Now().UTC()
	j.Lease = ""
	j.LeaseUntil = time.Time{}
	m.setPushDeliveryLocked(id, j)
}
func (m *MemStore) ListManagedRealtimePushDeliveries(ctx context.Context, ep, principal string) ([]ManagedRealtimePushDelivery, error) {
	pk, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ManagedRealtimePushDelivery{}
	for _, j := range m.managedRealtimePushDeliveries {
		if j.EndpointID == ep && j.Principal == pk {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > 100 {
		out = out[:100]
	}
	return out, nil
}
func (m *MemStore) pushDeviceLocked(j ManagedRealtimePushDelivery) (ManagedRealtimePushDevice, ManagedRealtimePushProvider, bool) {
	key := managedRealtimeDurableCursorKey{endpointID: j.EndpointID, principal: j.Principal, subscription: j.Device, channel: j.Principal}
	d := m.managedRealtimePushDevices[key]
	p := m.managedRealtimePushProviders[managedRealtimePushKey{j.EndpointID, j.Provider}]
	return d, p, d.Enabled && p.Enabled && d.Version == j.Version && d.Provider == j.Provider && m.managedRealtimeEndpoints[j.EndpointID].Enabled
}
func (m *MemStore) ClaimManagedRealtimePush(ctx context.Context, batch int) ([]ManagedRealtimePushDelivery, error) {
	if batch < 1 || batch > 128 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	due := []ManagedRealtimePushDelivery{}
	for id, j := range m.managedRealtimePushDeliveries {
		if j.Status != "pending" && j.Status != "sending" {
			if j.UpdatedAt.Before(now.Add(-7 * 24 * time.Hour)) {
				delete(m.managedRealtimePushDeliveries, id)
			}
			continue
		}
		if j.Attempts >= 7 && (j.Status == "pending" || !j.LeaseUntil.After(now)) {
			j.Status = "failed"
			j.Code = "attempts_exhausted"
			j.UpdatedAt = now
			j.Lease = ""
			j.LeaseUntil = time.Time{}
			m.setPushDeliveryLocked(id, j)
			continue
		}
		_, _, active := m.pushDeviceLocked(j)
		if !active || !j.ExpiresAt.After(now) {
			reason := "disabled"
			if !j.ExpiresAt.After(now) {
				reason = "expired"
			}
			m.cancelPushLocked(id, j, reason)
			continue
		}
		if j.Status == "pending" && !j.NextAttempt.After(now) || j.Status == "sending" && !j.LeaseUntil.After(now) {
			due = append(due, j)
		}
	}
	eligible := due[:0]
	for _, j := range due {
		representative := true
		if j.DigestID != "" {
			for _, peer := range m.managedRealtimePushDeliveries {
				if peer.DigestID == j.DigestID && (peer.Status == "pending" || peer.Status == "sending") && peer.ID < j.ID {
					representative = false
					break
				}
			}
		}
		if representative {
			eligible = append(eligible, j)
		}
	}
	due = eligible
	sort.Slice(due, func(i, j int) bool {
		if pushPriorityRank(due[i].Priority) != pushPriorityRank(due[j].Priority) {
			return pushPriorityRank(due[i].Priority) < pushPriorityRank(due[j].Priority)
		}
		return due[i].NextAttempt.Before(due[j].NextAttempt)
	})
	if len(due) > batch {
		due = due[:batch]
	}
	for i := range due {
		j := &due[i]
		j.Status = "sending"
		j.Attempts++
		j.Lease = uuid.NewString()
		j.LeaseUntil = now.Add(time.Minute)
		j.UpdatedAt = now
		m.setPushDeliveryLocked(j.ID, *j)
		d, p, _ := m.pushDeviceLocked(*j)
		j.Config = append([]byte(nil), p.Sealed...)
		j.Target = append([]byte(nil), d.Sealed...)
	}
	return due, nil
}
func (m *MemStore) ManagedRealtimePushLeaseActive(ctx context.Context, id, lease string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.managedRealtimePushDeliveries[id]
	_, _, active := m.pushDeviceLocked(j)
	return active && j.Status == "sending" && j.Lease == lease && j.LeaseUntil.After(time.Now()), nil
}
func (m *MemStore) CompleteManagedRealtimePush(ctx context.Context, id, lease string, statusCode int, code string, retry, invalid bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.managedRealtimePushDeliveries[id]
	if !ok || j.Status != "sending" || j.Lease != lease {
		return nil
	}
	now := time.Now().UTC()
	j.Status = "failed"
	if statusCode >= 200 && statusCode < 300 {
		j.Status = "sent"
	} else if retry && !invalid && j.Attempts <= len(pushRetryDelays) && now.Add(pushRetryDelays[j.Attempts-1]).Before(j.ExpiresAt) {
		j.Status = "pending"
		j.NextAttempt = now.Add(pushRetryDelays[j.Attempts-1])
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
	j.StatusCode = statusCode
	j.Code = code
	j.UpdatedAt = now
	j.Lease = ""
	j.LeaseUntil = time.Time{}
	m.setPushDeliveryLocked(id, j)
	return nil
}

// Enqueue all current devices atomically, or retain the deadline when at capacity.
func (m *MemStore) enqueuePushLocked(key managedRealtimeFallbackKey, pending managedRealtimeFallback) (int, bool) {
	var devices []ManagedRealtimePushDevice
	total := 0
	for _, j := range m.managedRealtimePushDeliveries {
		if j.EndpointID == key.endpointID {
			total++
		}
	}
	for k, d := range m.managedRealtimePushDevices {
		if k.endpointID == key.endpointID && k.principal == key.principal && d.Enabled && m.managedRealtimePushProviders[managedRealtimePushKey{key.endpointID, d.Provider}].Enabled {
			devices = append(devices, d)
		}
	}
	if total+len(devices) > 4096 {
		terminal := []ManagedRealtimePushDelivery{}
		for _, j := range m.managedRealtimePushDeliveries {
			if j.EndpointID == key.endpointID && j.Status != "pending" && j.Status != "sending" {
				terminal = append(terminal, j)
			}
		}
		sort.Slice(terminal, func(i, j int) bool { return terminal[i].UpdatedAt.Before(terminal[j].UpdatedAt) })
		if len(terminal) < total+len(devices)-4096 {
			return 0, false
		}
		for _, j := range terminal {
			if total+len(devices) <= 4096 {
				break
			}
			delete(m.managedRealtimePushDeliveries, j.ID)
			total--
		}
	}
	now := time.Now().UTC()
	for _, d := range devices {
		superseded := false
		if pending.collapseKey != "" && fallbackPushExpiry(now, pending).After(now) {
			for oldID, old := range m.managedRealtimePushDeliveries {
				if old.EndpointID != key.endpointID || old.Principal != key.principal || old.Device != d.Device || old.CollapseKey != pending.collapseKey || old.Category != pending.category || old.Priority != pending.priority {
					continue
				}
				if old.Sequence > key.sequence {
					superseded = true
				}
				if old.Sequence < key.sequence && (old.Status == "pending" || old.Status == "sending") {
					m.cancelPushLocked(oldID, old, "superseded")
				}
			}
		}
		id := uuid.NewString()
		m.setPushDeliveryLocked(id, ManagedRealtimePushDelivery{ID: id, EndpointID: key.endpointID, Principal: key.principal, Device: d.Device, Provider: d.Provider, Version: d.Version, MessageID: pending.messageID, Sequence: key.sequence, CollapseKey: pending.collapseKey, NotBefore: pending.notBefore, Status: "pending", CreatedAt: now, UpdatedAt: now, NextAttempt: now, ExpiresAt: fallbackPushExpiry(now, pending), HardExpiresAt: fallbackHardExpiry(pending), Category: pending.category, Priority: pending.priority, GroupKey: pending.groupKey, GroupLabel: pending.groupLabel})
		if pending.notBefore.After(now) {
			scheduled := m.managedRealtimePushDeliveries[id]
			scheduled.Code = "scheduled"
			scheduled.NextAttempt = pending.notBefore
			if !scheduled.ExpiresAt.After(pending.notBefore) {
				scheduled.Status = "cancelled"
				scheduled.Code = "expired"
			}
			m.setPushDeliveryLocked(id, scheduled)
		}
		if superseded {
			m.cancelPushLocked(id, m.managedRealtimePushDeliveries[id], "superseded")
		}
	}
	return len(devices), true
}

var _ ManagedRealtimePushStore = (*MemStore)(nil)

func fallbackHardExpiry(p managedRealtimeFallback) time.Time {
	if p.ttlSeconds == 0 {
		return time.Time{}
	}
	return p.expiresAt
}
func fallbackPushExpiry(now time.Time, p managedRealtimeFallback) time.Time {
	if p.ttlSeconds > 0 {
		return p.expiresAt
	}
	end := now.Add(24 * time.Hour)
	if scheduled := p.notBefore.Add(time.Hour); scheduled.After(end) {
		end = scheduled
	}
	return end
}
