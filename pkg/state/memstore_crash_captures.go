package state

import (
	"context"
	"slices"
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
	route = truncateUTF8(route, 512)
	at := memTime(now)
	c := CrashCapture{
		ID: uuid.NewString(), AccountID: app.AccountID, AppID: app.ID, DeploymentID: ins.DeploymentID,
		InstanceID: ins.ID, Trigger: trigger, StatusCode: statusCode, Route: route,
		Status: CrashCaptureRequested, RequestedAt: at, UpdatedAt: at, PlaintextState: CrashPlaintextPresent,
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

func (m *MemStore) RequestSDKCrashCapture(_ context.Context, appID, instanceID, route, reason string, cooldown time.Duration, now time.Time) (CrashCapture, error) {
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
	c := m.insertCrashCaptureLocked(app, ins, CrashTriggerSDK, nil, route, now)
	c.Reason = truncateUTF8(reason, CrashCaptureReasonMaxBytes)
	m.crashCaptures[c.ID] = c
	return c, nil
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
	c.SealedKey, c.PlaintextState = nil, CrashPlaintextAbsent
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

// activeForkPinnedLocked mirrors the SQL "active fork" rule: a fork queued,
// restoring or running against the capture.
func (m *MemStore) activeForkPinnedLocked(captureID string) bool {
	for _, f := range m.appForks {
		if f.CrashCaptureID != nil && *f.CrashCaptureID == captureID &&
			(f.Status == AppForkQueued || f.Status == AppForkRestoring || f.Status == AppForkRunning) {
			return true
		}
	}
	return false
}

func (m *MemStore) crashCapturesWhereLocked(limit int, keep func(CrashCapture) bool) []CrashCapture {
	out := make([]CrashCapture, 0)
	for _, c := range m.crashCaptures {
		if c.Status == CrashCaptureReady && keep(c) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CapturedAt.Equal(*out[j].CapturedAt) {
			return out[i].CapturedAt.Before(*out[j].CapturedAt)
		}
		return out[i].ID < out[j].ID
	})
	if limit = clampAppForkListLimit(limit); len(out) > limit {
		out = out[:limit]
	}
	return out
}

// moveCrashPlaintextLocked is the MemStore compare-and-swap behind every
// encryption transition.
func (m *MemStore) moveCrashPlaintextLocked(id string, now time.Time, from []CrashCapturePlaintext, guard func(CrashCapture) bool, apply func(*CrashCapture)) (CrashCapture, error) {
	m.ensureCrashLocked()
	c, ok := m.crashCaptures[id]
	if !ok || c.Status != CrashCaptureReady || !slices.Contains(from, c.PlaintextState) || (guard != nil && !guard(c)) {
		return CrashCapture{}, ErrNotFound
	}
	apply(&c)
	m.touchCrashLocked(&c, now)
	return c, nil
}

func (m *MemStore) CrashCapturesToEncrypt(_ context.Context, now time.Time, limit int) ([]CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	return m.crashCapturesWhereLocked(limit, func(c CrashCapture) bool {
		return c.PlaintextState == CrashPlaintextPresent && c.ExpiresAt.After(now)
	}), nil
}

func (m *MemStore) MarkCrashCaptureEncrypted(_ context.Context, id string, sealedKey []byte, now time.Time) (CrashCapture, error) {
	if len(sealedKey) == 0 {
		return CrashCapture{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.moveCrashPlaintextLocked(id, now, []CrashCapturePlaintext{CrashPlaintextPresent}, nil, func(c *CrashCapture) {
		at := memTime(now)
		c.SealedKey, c.EncryptedAt, c.PlaintextState = slices.Clone(sealedKey), &at, CrashPlaintextPurging
		if m.activeForkPinnedLocked(c.ID) {
			c.PlaintextState = CrashPlaintextStaged
		}
	})
}

func (m *MemStore) CrashCapturesToPurge(_ context.Context, limit int) ([]CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	return m.crashCapturesWhereLocked(limit, func(c CrashCapture) bool {
		return c.PlaintextState == CrashPlaintextPurging ||
			((c.PlaintextState == CrashPlaintextStaging || c.PlaintextState == CrashPlaintextStaged) && !m.activeForkPinnedLocked(c.ID))
	}), nil
}

func (m *MemStore) BeginCrashCapturePurge(_ context.Context, id string, now time.Time) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.moveCrashPlaintextLocked(id, now,
		[]CrashCapturePlaintext{CrashPlaintextStaging, CrashPlaintextStaged, CrashPlaintextPurging},
		func(c CrashCapture) bool { return !m.activeForkPinnedLocked(c.ID) },
		func(c *CrashCapture) { c.PlaintextState = CrashPlaintextPurging })
}

func (m *MemStore) FinishCrashCapturePurge(_ context.Context, id string, now time.Time) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.moveCrashPlaintextLocked(id, now, []CrashCapturePlaintext{CrashPlaintextPurging}, nil,
		func(c *CrashCapture) { c.PlaintextState = CrashPlaintextAbsent })
}

func (m *MemStore) CrashCapturesToStage(_ context.Context, now time.Time, limit int) ([]CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCrashLocked()
	return m.crashCapturesWhereLocked(limit, func(c CrashCapture) bool {
		return (c.PlaintextState == CrashPlaintextAbsent || c.PlaintextState == CrashPlaintextStaging) &&
			c.ExpiresAt.After(now) && m.activeForkPinnedLocked(c.ID)
	}), nil
}

func (m *MemStore) BeginCrashCaptureStage(_ context.Context, id string, now time.Time) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.moveCrashPlaintextLocked(id, now,
		[]CrashCapturePlaintext{CrashPlaintextAbsent, CrashPlaintextStaging},
		func(c CrashCapture) bool { return m.activeForkPinnedLocked(c.ID) },
		func(c *CrashCapture) { c.PlaintextState = CrashPlaintextStaging })
}

func (m *MemStore) FinishCrashCaptureStage(_ context.Context, id string, now time.Time) (CrashCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.moveCrashPlaintextLocked(id, now, []CrashCapturePlaintext{CrashPlaintextStaging}, nil,
		func(c *CrashCapture) { c.PlaintextState = CrashPlaintextStaged })
}

// crashCaptureClaimableLocked mirrors ClaimNextAppFork's ADR-733 gate: a
// fork pinned to a ready capture waits until its plaintext is readable.
func (m *MemStore) crashCaptureClaimableLocked(f AppFork) bool {
	if f.CrashCaptureID == nil {
		return true
	}
	c, ok := m.crashCaptures[*f.CrashCaptureID]
	return ok && (c.Status != CrashCaptureReady || c.PlaintextReadable())
}

func (m *MemStore) LiveCrashCaptureDeploymentIDs(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, c := range m.crashCaptures {
		if c.Status != CrashCaptureCapturing && c.Status != CrashCaptureReady {
			continue
		}
		if _, ok := seen[c.DeploymentID]; !ok {
			seen[c.DeploymentID] = struct{}{}
			out = append(out, c.DeploymentID)
		}
	}
	sort.Strings(out)
	return out, nil
}
