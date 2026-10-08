package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

// MemStore mirrors PgStore's ADR-733 crash capture rules: opt-in for the 5xx
// trigger, one capture in flight per app, a cooldown between requests, and
// the requested → capturing → ready → expired / failed lifecycle.

func (m *MemStore) ensureCrashLocked() {
	if m.crashCaptures == nil {
		m.crashCaptures = make(map[string]CrashCapture)
	}
	if m.crashSettings == nil {
		m.crashSettings = make(map[string]CrashSnapshotSettings)
	}
}

func (m *MemStore) SetCrashSnapshotSettings(_ context.Context, accountID, appID string, enabled bool, now time.Time) (CrashSnapshotSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID {
		return CrashSnapshotSettings{}, ErrNotFound
	}
	settings := CrashSnapshotSettings{AppID: appID, AccountID: accountID, Enabled: enabled, UpdatedAt: memTime(now)}
	m.crashSettings[appID] = settings
	return settings, nil
}

func (m *MemStore) CrashSnapshotSettingsFor(_ context.Context, accountID, appID string) (CrashSnapshotSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	settings, ok := m.crashSettings[appID]
	if !ok || settings.AccountID != accountID {
		return CrashSnapshotSettings{AppID: appID, AccountID: accountID}, nil
	}
	return settings, nil
}

// crashCaptureBlockedLocked reports an in-flight capture or one requested
// within the cooldown.
func (m *MemStore) crashCaptureBlockedLocked(appID string, cooldown time.Duration, now time.Time) bool {
	for _, c := range m.crashCaptures {
		if c.AppID != appID {
			continue
		}
		if c.Status == CrashCaptureRequested || c.Status == CrashCaptureCapturing || c.RequestedAt.After(now.Add(-cooldown)) {
			return true
		}
	}
	return false
}

func (m *MemStore) insertCrashCaptureLocked(app App, ins Instance, trigger string, statusCode *int, route string, now time.Time) CrashCapture {
	if len(route) > 512 {
		route = route[:512]
	}
	at := memTime(now)
	c := CrashCapture{
		ID: uuid.NewString(), AccountID: app.AccountID, AppID: app.ID, DeploymentID: ins.DeploymentID,
		InstanceID: ins.ID, Trigger: trigger, StatusCode: statusCode, Route: route,
		Status: CrashCaptureRequested, RequestedAt: at, UpdatedAt: at,
	}
	m.crashCaptures[c.ID] = c
	return c
}

func (m *MemStore) RequestHTTPCrashCapture(_ context.Context, appID, instanceID string, statusCode int, route string, cooldown time.Duration, now time.Time) (CrashCapture, error) {
	if statusCode < 500 || statusCode > 599 {
		return CrashCapture{}, ErrCrashCaptureRefused
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	app, ok := m.apps[appID]
	ins, insOK := m.instances[instanceID]
	settings := m.crashSettings[appID]
	if !ok || app.Status == AppDeleted || !insOK || ins.AppID != appID || ins.State != string(StateRunning) ||
		IsFork(ins.Mode) || !settings.Enabled || m.crashCaptureBlockedLocked(appID, cooldown, now) {
		return CrashCapture{}, ErrCrashCaptureRefused
	}
	code := statusCode
	return m.insertCrashCaptureLocked(app, ins, CrashTriggerHTTP5xx, &code, route, now), nil
}

func (m *MemStore) RequestManualCrashCapture(_ context.Context, accountID, appID string, cooldown time.Duration, now time.Time) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted || m.crashCaptureBlockedLocked(appID, cooldown, now) {
		return CrashCapture{}, ErrCrashCaptureRefused
	}
	var newest Instance
	found := false
	for _, ins := range m.instances {
		if ins.AppID != appID || ins.State != string(StateRunning) || IsFork(ins.Mode) {
			continue
		}
		if !found || ins.StartedAt.After(newest.StartedAt) {
			newest, found = ins, true
		}
	}
	if !found {
		return CrashCapture{}, ErrCrashCaptureRefused
	}
	return m.insertCrashCaptureLocked(app, newest, CrashTriggerManual, nil, "", now), nil
}

func (m *MemStore) ClaimNextCrashCapture(_ context.Context, now time.Time) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	var next CrashCapture
	found := false
	for _, c := range m.crashCaptures {
		if c.Status != CrashCaptureRequested {
			continue
		}
		if !found || c.RequestedAt.Before(next.RequestedAt) || (c.RequestedAt.Equal(next.RequestedAt) && c.ID < next.ID) {
			next, found = c, true
		}
	}
	if !found {
		return CrashCapture{}, ErrNotFound
	}
	next.Status = CrashCaptureCapturing
	m.touchCrashLocked(&next, now)
	return next, nil
}

func (m *MemStore) touchCrashLocked(c *CrashCapture, now time.Time) {
	if at := memTime(now); at.After(c.UpdatedAt) {
		c.UpdatedAt = at
	}
	m.crashCaptures[c.ID] = *c
}

func (m *MemStore) CompleteCrashCapture(_ context.Context, p CompleteCrashCaptureParams) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	c, ok := m.crashCaptures[p.ID]
	if !ok || c.Status != CrashCaptureCapturing {
		return CrashCapture{}, ErrNotFound
	}
	storageKey, vmstateKey, fcVersion, memBytes := p.StorageKey, p.VMStateStorageKey, p.FCVersion, p.MemBytes
	captured, expires := memTime(p.CapturedAt), memTime(p.ExpiresAt)
	c.Status, c.StorageKey, c.VMStateStorageKey, c.FCVersion, c.MemBytes = CrashCaptureReady, &storageKey, &vmstateKey, &fcVersion, &memBytes
	c.CapturedAt, c.ExpiresAt = &captured, &expires
	m.touchCrashLocked(&c, p.CapturedAt)
	return c, nil
}

func (m *MemStore) FailCrashCapture(_ context.Context, id, code, message string, now time.Time) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	c, ok := m.crashCaptures[id]
	if !ok || (c.Status != CrashCaptureRequested && c.Status != CrashCaptureCapturing) {
		return CrashCapture{}, ErrNotFound
	}
	m.failCrashLocked(&c, code, message, now)
	return c, nil
}

func (m *MemStore) failCrashLocked(c *CrashCapture, code, message string, now time.Time) {
	at := memTime(now)
	c.Status, c.FailureCode, c.FailureMessage, c.FinishedAt = CrashCaptureFailed, &code, &message, &at
	m.touchCrashLocked(c, now)
}

func (m *MemStore) FailStaleCrashCaptures(_ context.Context, cutoff, now time.Time) ([]CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	out := make([]CrashCapture, 0)
	for _, c := range m.crashCaptures {
		if c.Status == CrashCaptureCapturing && c.UpdatedAt.Before(cutoff) {
			m.failCrashLocked(&c, "capture_timeout", "the capture did not finish in time", now)
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *MemStore) sortedCrashCapturesLocked(keep func(CrashCapture) bool, newestFirst bool) []CrashCapture {
	out := make([]CrashCapture, 0)
	for _, c := range m.crashCaptures {
		if keep(c) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].RequestedAt, out[j].RequestedAt
		if out[i].Status == CrashCaptureReady && out[j].Status == CrashCaptureReady && !newestFirst {
			a, b = *out[i].ExpiresAt, *out[j].ExpiresAt
		}
		if !a.Equal(b) {
			return a.After(b) == newestFirst
		}
		return (out[i].ID > out[j].ID) == newestFirst
	})
	return out
}

func (m *MemStore) ExpiredCrashCaptures(_ context.Context, now time.Time, limit int) ([]CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	out := m.sortedCrashCapturesLocked(func(c CrashCapture) bool {
		return c.Status == CrashCaptureReady && !c.ExpiresAt.After(now)
	}, false)
	if limit = clampAppForkListLimit(limit); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ExpireCrashCapture(_ context.Context, id string, now time.Time) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	c, ok := m.crashCaptures[id]
	if !ok || c.Status != CrashCaptureReady {
		return CrashCapture{}, ErrNotFound
	}
	at := memTime(now)
	c.Status, c.FinishedAt = CrashCaptureExpired, &at
	m.touchCrashLocked(&c, now)
	return c, nil
}

func (m *MemStore) ListCrashCaptures(_ context.Context, accountID, appID string, limit int) ([]CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	out := m.sortedCrashCapturesLocked(func(c CrashCapture) bool { return c.AccountID == accountID && c.AppID == appID }, true)
	if limit = clampAppForkListLimit(limit); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) CrashCaptureByID(_ context.Context, accountID, appID, id string) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	c, ok := m.crashCaptures[id]
	if !ok || c.AccountID != accountID || c.AppID != appID {
		return CrashCapture{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) CrashCaptureForRestore(_ context.Context, id string) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	c, ok := m.crashCaptures[id]
	if !ok {
		return CrashCapture{}, ErrNotFound
	}
	return c, nil
}

// forkTargetForCaptureLocked resolves a fork pinned to a crash capture: the
// capture must be the app's, ready and unexpired.
func (m *MemStore) forkTargetForCaptureLocked(p CreateAppForkParams) (string, bool) {
	m.ensureCrashLocked()
	c, ok := m.crashCaptures[p.CrashCaptureID]
	if !ok || c.AppID != p.AppID || c.AccountID != p.AccountID || c.Status != CrashCaptureReady ||
		c.ExpiresAt == nil || !c.ExpiresAt.After(p.CreatedAt) {
		return "", false
	}
	return c.DeploymentID, true
}
