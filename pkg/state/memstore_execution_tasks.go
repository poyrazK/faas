package state

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func executionTaskKey(account, name string, version int) string {
	return account + "\x00" + name + "\x00" + strconv.Itoa(version)
}
func (m *MemStore) PublishExecutionTaskVersion(_ context.Context, v ExecutionTaskVersion) (ExecutionTaskVersion, bool, error) {
	if err := validateExecutionTaskVersion(v); err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := executionTaskKey(v.AccountID, v.Name, v.Version)
	if old, ok := m.executionTaskVersions[key]; ok {
		if old.Digest != v.Digest {
			return ExecutionTaskVersion{}, false, ErrExecutionTaskConflict
		}
		return cloneExecutionTaskVersion(old), true, nil
	}
	account, ok := m.accounts[v.AccountID]
	if !ok {
		return ExecutionTaskVersion{}, false, ErrNotFound
	}
	for _, id := range v.Metadata.AgentEndpointIDs {
		ep, ok := m.managedRealtimeEndpoints[id]
		if !ok || ep.AccountID != v.AccountID {
			return ExecutionTaskVersion{}, false, ErrNotFound
		}
	}
	names := map[string]bool{}
	var versions, retained int64
	for _, old := range m.executionTaskVersions {
		if old.AccountID == v.AccountID {
			versions++
			retained += int64(len(old.SealedDefinition))
			names[old.Name] = true
		}
	}
	if err := taskQuota(account.Plan, versions, int64(len(names)), retained, !names[v.Name], int64(len(v.SealedDefinition))); err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	if m.executionTaskVersions == nil {
		m.executionTaskVersions = map[string]ExecutionTaskVersion{}
	}
	v.Status = "active"
	v.CreatedAt = time.Now().UTC()
	v.Metadata.ID = v.ID
	v.Metadata.Status = v.Status
	v.Metadata.CreatedAt = v.CreatedAt.Format(time.RFC3339Nano)
	m.executionTaskVersions[key] = cloneExecutionTaskVersion(v)
	return cloneExecutionTaskVersion(v), false, nil
}
func (m *MemStore) ExecutionTaskVersionByName(_ context.Context, account, name string, version int) (ExecutionTaskVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.executionTaskVersions[executionTaskKey(account, name, version)]
	if !ok {
		return ExecutionTaskVersion{}, ErrNotFound
	}
	return cloneExecutionTaskVersion(v), nil
}
func (m *MemStore) ListExecutionTaskVersions(_ context.Context, account, afterName string, afterVersion, limit int) ([]ExecutionTaskVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ExecutionTaskVersion{}
	for _, v := range m.executionTaskVersions {
		if v.AccountID == account && (v.Name > afterName || v.Name == afterName && v.Version < afterVersion) {
			v.SealedDefinition = nil
			out = append(out, cloneExecutionTaskVersion(v))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version > out[j].Version
	})
	if limit < 1 || limit > api.ExecutionTaskListMax+1 {
		limit = api.ExecutionTaskListMax + 1
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemStore) RetireExecutionTaskVersion(_ context.Context, account, name string, version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := executionTaskKey(account, name, version)
	v, ok := m.executionTaskVersions[key]
	if !ok {
		return ErrNotFound
	}
	v.Status = "retired"
	clear(v.SealedDefinition)
	v.SealedDefinition = nil
	v.Metadata.Status = v.Status
	m.executionTaskVersions[key] = v
	return nil
}
func cloneExecutionTaskInvocation(v ExecutionTaskInvocation) ExecutionTaskInvocation {
	v.RequestDigest = append([]byte(nil), v.RequestDigest...)
	if v.Callback != nil {
		c := *v.Callback
		v.Callback = &c
	}
	if v.DeliveredAt != nil {
		t := *v.DeliveredAt
		v.DeliveredAt = &t
	}
	return v
}
func (m *MemStore) ExecutionTaskInvocationByID(_ context.Context, account, id string) (ExecutionTaskInvocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.executionTaskInvocations[id]
	if !ok || v.AccountID != account {
		return v, ErrNotFound
	}
	return cloneExecutionTaskInvocation(v), nil
}
func (m *MemStore) PendingExecutionTaskCallbacks(_ context.Context, afterID string, limit int) ([]ExecutionTaskInvocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit < 1 || limit > api.ExecutionTaskCallbackBatch {
		limit = api.ExecutionTaskCallbackBatch
	}
	out := []ExecutionTaskInvocation{}
	for id, v := range m.executionTaskInvocations {
		r, ok := m.executions[id]
		if ok && id > afterID && v.Callback != nil && v.DeliveredAt == nil && r.Status.Terminal() {
			out = append(out, cloneExecutionTaskInvocation(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExecutionID < out[j].ExecutionID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemStore) MarkExecutionTaskCallbackDelivered(_ context.Context, account, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.executionTaskInvocations[id]
	if !ok || v.AccountID != account || v.Callback == nil {
		return ErrNotFound
	}
	if v.DeliveredAt == nil {
		now := time.Now().UTC()
		v.DeliveredAt = &now
		m.executionTaskInvocations[id] = v
	}
	return nil
}
func (m *MemStore) taskCallbackReplayLocked(account string, c *ExecutionTaskCallback, fingerprint [32]byte) (Execution, bool, error) {
	if c == nil {
		return Execution{}, false, nil
	}
	key := string(c.Key())
	for _, v := range m.executionTaskInvocations {
		if v.AccountID == account && v.Callback != nil && string(v.Callback.Key()) == key {
			if err := compareExecutionAdmission(v.RequestDigest, fingerprint); err != nil {
				return Execution{}, true, err
			}
			r, ok := m.executions[v.ExecutionID]
			if !ok {
				return Execution{}, true, ErrNotFound
			}
			return cloneExecution(r), true, nil
		}
	}
	return Execution{}, false, nil
}
func (m *MemStore) validateTaskAdmissionLocked(p CreateExecutionParams) (ExecutionTaskVersion, error) {
	if p.Task == nil {
		return ExecutionTaskVersion{}, nil
	}
	if p.Task.RequestDigest == [32]byte{} || validateExecutionTaskCallback(p.Task.Callback) != nil {
		return ExecutionTaskVersion{}, ErrExecutionInvalid
	}
	var v ExecutionTaskVersion
	found := false
	for _, candidate := range m.executionTaskVersions {
		if candidate.AccountID == p.AccountID && candidate.ID == p.Task.VersionID {
			v = candidate
			found = true
			break
		}
	}
	if !found || v.Status != "active" {
		return v, ErrExecutionTaskUnavailable
	}
	if err := matchTaskAdmission(v, p); err != nil {
		return v, err
	}
	if endpoint := runCallbackEndpoint(p.Task); endpoint != "" {
		ep, ok := m.managedRealtimeEndpoints[endpoint]
		if !ok || ep.AccountID != p.AccountID {
			if p.Task.PipelineEndpointID != "" {
				return v, ErrExecutionTaskUnavailable
			}
			return v, ErrNotFound
		}
	}
	if p.Task.Callback != nil {
		if _, exists, err := m.taskCallbackReplayLocked(p.AccountID, p.Task.Callback, p.Task.RequestDigest); exists || err != nil {
			return v, ErrExecutionTaskConflict
		}
	}
	return v, nil
}
func (m *MemStore) addTaskInvocationLocked(p CreateExecutionParams, r *Execution, v ExecutionTaskVersion) {
	if p.Task == nil {
		return
	}
	if m.executionTaskInvocations == nil {
		m.executionTaskInvocations = map[string]ExecutionTaskInvocation{}
	}
	ref := api.ExecutionTaskRef{ID: v.ID, Name: v.Name, Version: v.Version, Digest: v.Digest}
	r.Task = &ref
	m.executionTaskInvocations[r.ID] = cloneExecutionTaskInvocation(ExecutionTaskInvocation{ExecutionID: r.ID, AccountID: r.AccountID, VersionID: v.ID, Ref: ref, RequestDigest: p.Task.RequestDigest[:], Callback: p.Task.Callback})
}

// Keep callback envelopes as JSON objects in both backends; no callback URL or token is retained.
func taskCallbackJSON(c *ExecutionTaskCallback) []byte {
	if c == nil {
		return []byte("{}")
	}
	raw, _ := json.Marshal(c)
	return raw
}

var _ ExecutionTaskStore = (*MemStore)(nil)
