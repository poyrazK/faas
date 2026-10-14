package state

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type memProfileCapture struct {
	accountID string
	capture   api.ProfileCapture
	claimedAt time.Time
	blobs     []ProfileCaptureBlob
}

func copyProfileCapture(c api.ProfileCapture) api.ProfileCapture {
	c.Kinds = append([]string(nil), c.Kinds...)
	c.Profiles = append([]api.ProfileCaptureProfile{}, c.Profiles...)
	if c.CompletedAt != nil {
		t := *c.CompletedAt
		c.CompletedAt = &t
	}
	if c.OOMKilledAt != nil {
		t := *c.OOMKilledAt
		c.OOMKilledAt = &t
	}
	return c
}

func (m *MemStore) profileCaptureLocked(accountID, appID, id string) (*memProfileCapture, bool) {
	row, ok := m.profileCaptures[id]
	if !ok || row.accountID != accountID || row.capture.AppID != appID || !m.profileInvestigationOwnedLocked(accountID, appID) {
		return nil, false
	}
	return row, true
}

func (m *MemStore) CreateProfileCapture(_ context.Context, accountID, appID string, c api.ProfileCapture) error {
	return m.insertProfileCapture(accountID, appID, c, 0)
}

func (m *MemStore) QueueAutomaticProfileCapture(_ context.Context, accountID, appID string, c api.ProfileCapture, minInterval time.Duration) error {
	if c.Trigger == "" || c.Trigger == api.ProfileCaptureTriggerManual || minInterval <= 0 {
		return fmt.Errorf("automatic profile capture needs a trigger and minimum interval")
	}
	return m.insertProfileCapture(accountID, appID, c, minInterval)
}

func (m *MemStore) insertProfileCapture(accountID, appID string, c api.ProfileCapture, automaticInterval time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.profileInvestigationOwnedLocked(accountID, appID) {
		return ErrNotFound
	}
	recent := 0
	for _, row := range m.profileCaptures {
		if row.accountID == accountID && !row.capture.CreatedAt.Before(c.CreatedAt.Add(-time.Hour)) {
			recent++
		}
		if row.capture.AppID == appID && !row.capture.Done() {
			return ErrProfileCaptureActive
		}
	}
	if automaticInterval > 0 {
		for _, row := range m.profileCaptures {
			if row.capture.AppID == appID && row.capture.Trigger == c.Trigger && !row.capture.CreatedAt.Before(c.CreatedAt.Add(-automaticInterval)) {
				return ErrProfileCaptureRecent
			}
		}
	}
	if recent >= api.ProfileCaptureMaxPerAccountHour {
		return ErrProfileCaptureQuota
	}
	if m.profileCaptures == nil {
		m.profileCaptures = map[string]*memProfileCapture{}
	}
	stored := copyProfileCapture(c)
	stored.AppID, stored.Status = appID, api.ProfileCaptureQueued
	if stored.Trigger == "" {
		stored.Trigger = api.ProfileCaptureTriggerManual
	}
	stored.Profiles, stored.CompletedAt = []api.ProfileCaptureProfile{}, nil
	m.profileCaptures[c.ID] = &memProfileCapture{accountID: accountID, capture: stored}
	return nil
}

func (m *MemStore) GetProfileCapture(_ context.Context, accountID, appID, id string) (api.ProfileCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.profileCaptureLocked(accountID, appID, id)
	if !ok {
		return api.ProfileCapture{}, ErrNotFound
	}
	return copyProfileCapture(row.capture), nil
}

func (m *MemStore) ListProfileCaptures(_ context.Context, accountID, appID string) ([]api.ProfileCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []api.ProfileCapture{}
	if !m.profileInvestigationOwnedLocked(accountID, appID) {
		return out, nil
	}
	for _, row := range m.profileCaptures {
		if row.accountID == accountID && row.capture.AppID == appID {
			out = append(out, copyProfileCapture(row.capture))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > api.ProfileCaptureMaxListed {
		out = out[:api.ProfileCaptureMaxListed]
	}
	return out, nil
}

func (m *MemStore) ProfileCaptureBlobs(_ context.Context, accountID, appID, id string) ([]ProfileCaptureBlob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.profileCaptureLocked(accountID, appID, id)
	if !ok {
		return []ProfileCaptureBlob{}, nil
	}
	out := make([]ProfileCaptureBlob, 0, len(row.blobs))
	for _, b := range row.blobs {
		out = append(out, ProfileCaptureBlob{Kind: b.Kind, ProcessID: b.ProcessID, Profile: append([]byte(nil), b.Profile...)})
	}
	return out, nil
}

func (m *MemStore) ClaimProfileCapture(_ context.Context, now time.Time) (ClaimedProfileCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var next *memProfileCapture
	for _, row := range m.profileCaptures {
		if row.capture.Status != api.ProfileCaptureQueued {
			continue
		}
		if next == nil || row.capture.CreatedAt.Before(next.capture.CreatedAt) || row.capture.CreatedAt.Equal(next.capture.CreatedAt) && row.capture.ID < next.capture.ID {
			next = row
		}
	}
	if next == nil {
		return ClaimedProfileCapture{}, ErrNotFound
	}
	next.capture.Status, next.claimedAt = api.ProfileCaptureCapturing, now
	return ClaimedProfileCapture{ID: next.capture.ID, AppID: next.capture.AppID, AccountID: next.accountID, Capture: copyProfileCapture(next.capture)}, nil
}

func (m *MemStore) FinishProfileCapture(_ context.Context, id string, result api.ProfileCapture, blobs []ProfileCaptureBlob, now time.Time) error {
	if err := validProfileCaptureFinish(result, blobs); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.profileCaptures[id]
	if !ok || row.capture.Status != api.ProfileCaptureCapturing {
		return ErrNotFound
	}
	c := &row.capture
	c.Status, c.InstanceID, c.DeploymentID = result.Status, result.InstanceID, result.DeploymentID
	c.Processes, c.Dropped, c.Reason, c.Partial = result.Processes, result.Dropped, result.Reason, result.Partial
	c.Profiles = append([]api.ProfileCaptureProfile{}, result.Profiles...)
	completed := now
	c.CompletedAt = &completed
	row.blobs = nil
	for _, b := range blobs {
		row.blobs = append(row.blobs, ProfileCaptureBlob{Kind: b.Kind, ProcessID: b.ProcessID, Profile: append([]byte(nil), b.Profile...)})
	}
	return nil
}

func (m *MemStore) StartProfileCapture(_ context.Context, id, instanceID, deploymentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.profileCaptures[id]
	if !ok || row.capture.Status != api.ProfileCaptureCapturing {
		return ErrNotFound
	}
	row.capture.InstanceID, row.capture.DeploymentID = instanceID, deploymentID
	return nil
}

func (m *MemStore) StampProfileCaptureOOM(_ context.Context, appID, instanceID string, oom ProfileCaptureOOM, since time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var newest *memProfileCapture
	for _, row := range m.profileCaptures {
		c := row.capture
		if c.AppID != appID || c.InstanceID != instanceID || c.CreatedAt.Before(since) || !slices.Contains(c.Kinds, api.ProfileKindHeap) {
			continue
		}
		if newest == nil || c.CreatedAt.After(newest.capture.CreatedAt) || c.CreatedAt.Equal(newest.capture.CreatedAt) && c.ID < newest.capture.ID {
			newest = row
		}
	}
	if newest == nil {
		return "", ErrNotFound
	}
	killed := oom.KilledAt
	newest.capture.OOMKilledAt, newest.capture.OOMPeakMB, newest.capture.OOMPlanMB = &killed, oom.PeakMB, oom.PlanMB
	return newest.capture.ID, nil
}

func (m *MemStore) LatestOOMProfileCapture(_ context.Context, accountID, appID, deploymentID string) (api.ProfileCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.profileInvestigationOwnedLocked(accountID, appID) {
		return api.ProfileCapture{}, ErrNotFound
	}
	var newest *api.ProfileCapture
	for _, row := range m.profileCaptures {
		c := row.capture
		if row.accountID != accountID || c.AppID != appID || c.DeploymentID != deploymentID || c.Status != api.ProfileCaptureReady || c.OOMKilledAt == nil {
			continue
		}
		if newest == nil || c.CreatedAt.After(newest.CreatedAt) || c.CreatedAt.Equal(newest.CreatedAt) && c.ID < newest.ID {
			copied := copyProfileCapture(c)
			newest = &copied
		}
	}
	if newest == nil {
		return api.ProfileCapture{}, ErrNotFound
	}
	return *newest, nil
}

func (m *MemStore) ExpireProfileCaptures(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, row := range m.profileCaptures {
		c := &row.capture
		stale := c.Status == api.ProfileCaptureCapturing && row.claimedAt.Before(now.Add(-ProfileCaptureInterruptedAfter)) ||
			c.Status == api.ProfileCaptureQueued && c.CreatedAt.Before(now.Add(-ProfileCaptureQueuedTimeout))
		if stale {
			completed := now
			c.Status, c.Reason, c.CompletedAt = api.ProfileCaptureFailed, profileCaptureInterruptedReason, &completed
			n++
		}
		if c.ExpiresAt.Before(now) {
			delete(m.profileCaptures, id)
			n++
		}
	}
	return n, nil
}
