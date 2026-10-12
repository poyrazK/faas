package socket

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

const RegistryCleanupWorkerPoll = 5 * time.Second
const registryWorkerLease = 30 * time.Second
const registryWorkerPageSize = 4
const registryWorkerPrefix = "tenant-registry/workers/v1/"

// Worker observations and claims are advisory. Only the quota root's explicit
// operation and epoch checkpoint authorize a cleanup step.
type RegistryCleanupWorkerStatus struct {
	OperationID      string    `json:"operation_id"`
	ObservedRevision uint64    `json:"observed_revision"`
	LastAttemptAt    time.Time `json:"last_attempt_at"`
	LastSuccessAt    time.Time `json:"last_success_at,omitempty"`
	NextAttemptAt    time.Time `json:"next_attempt_at"`
	LeaseUntil       time.Time `json:"lease_until,omitempty"`
	Failures         int       `json:"consecutive_failures"`
	LastError        string    `json:"last_error,omitempty"`
}
type registryWorkerRecord struct {
	Schema int    `json:"schema"`
	Scope  string `json:"scope"`
	Claim  string `json:"claim,omitempty"`
	RegistryCleanupWorkerStatus
}
type RegistryCleanupWorkerPage struct {
	NextCursor         string
	Attempted          int
	Failed             int
	Scanned            int
	Active             int
	Pruned             int
	HintRepairFailures int
	OldestActiveAt     time.Time
}

func workerError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, durableentity.ErrUncertain):
		return "uncertain"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "deadline"
	case errors.Is(err, durableentity.ErrBusy):
		return "busy"
	case errors.Is(err, durableentity.ErrConflict):
		return "conflict"
	case errors.Is(err, durableentity.ErrCorrupt):
		return "corrupt"
	case errors.Is(err, ErrPolicyMismatch):
		return "policy_mismatch"
	case errors.Is(err, durableentity.ErrUnsupported):
		return "unsupported"
	default:
		return "provider"
	}
}
func validWorkerError(s string) bool {
	switch s {
	case "", "uncertain", "deadline", "busy", "conflict", "corrupt", "policy_mismatch", "unsupported", "provider":
		return true
	}
	return false
}
func (q *Quotas) readRegistryWorker(ctx context.Context, key string) (registryWorkerRecord, string, error) {
	body, version, err := q.Store.Get(ctx, registryWorkerPrefix+key, 4096)
	if errors.Is(err, durableentity.ErrNotFound) {
		return registryWorkerRecord{}, "", nil
	}
	if err != nil {
		return registryWorkerRecord{}, "", err
	}
	var r registryWorkerRecord
	if version == "" || len(body) > 4096 || json.Unmarshal(body, &r) != nil || r.Schema != 1 || r.Scope != key || !sessionUUID(r.OperationID) || r.ObservedRevision == 0 || r.LastAttemptAt.IsZero() || r.NextAttemptAt.IsZero() || r.Failures < 0 || r.Failures > 10 || !validWorkerError(r.LastError) || (r.Claim != "" && (!sessionUUID(r.Claim) || r.LeaseUntil.Before(r.LastAttemptAt.Add(registryWorkerLease)))) || (r.Claim == "" && !r.LeaseUntil.IsZero()) {
		return registryWorkerRecord{}, "", durableentity.ErrCorrupt
	}
	return r, version, nil
}
func (q *Quotas) putRegistryWorker(ctx context.Context, r registryWorkerRecord, version string) (string, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	v, err := q.Store.Put(ctx, registryWorkerPrefix+r.Scope, body, version)
	if errors.Is(err, durableentity.ErrConflict) {
		return "", err
	}
	if err != nil || v == "" {
		return "", errors.Join(durableentity.ErrUncertain, err)
	}
	return v, nil
}

// Status is observational, matched to the root's current operation. A lease may
// remain after process loss; it expires without modifying the cleanup barrier.
func (q *Quotas) RegistryCleanupWorkerStatus(ctx context.Context, id durableentity.ID) (*RegistryCleanupWorkerStatus, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return nil, err
	}
	key := scopeKey(scope)
	root, _, err := q.read(ctx, key)
	if err != nil {
		return nil, err
	}
	if root.RegistryCleanup == nil {
		return nil, nil
	}
	r, _, err := q.readRegistryWorker(ctx, key)
	if err != nil {
		return nil, err
	}
	if r.OperationID != root.RegistryCleanup.OperationID {
		return nil, nil
	}
	copy := r.RegistryCleanupWorkerStatus
	return &copy, nil
}

// Sweep reconciles hints from at most four quota roots, then attempts one cleanup
// page per eligible scope. It never creates an operation. Discovery cursors are
// disposable; root/checkpoint and worker claims survive process restart.
func (q *Quotas) SweepRegistryCleanups(ctx context.Context, cursor string, allow func(durableentity.ID) bool) (RegistryCleanupWorkerPage, error) {
	out := RegistryCleanupWorkerPage{}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if len(cursor) > api.MaxObjectS3ListCursorBytes {
		return out, durableentity.ErrInvalid
	}
	store, ok := q.Store.(durableentity.CleanupStore)
	if !ok {
		return out, durableentity.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	scanCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	page, err := store.ListEntityObjects(scanCtx, "tenant-quotas/v1/", cursor, registryWorkerPageSize)
	stop()
	if err != nil {
		return out, err
	}
	if len(page.Keys) > registryWorkerPageSize || !validRegistryCleanupPage("tenant-quotas/v1/", cursor, page) {
		return out, durableentity.ErrCorrupt
	}
	out.NextCursor = page.NextCursor
	out.Scanned = len(page.Keys)
	for _, path := range page.Keys {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		key := strings.TrimPrefix(path, "tenant-quotas/v1/")
		if !validScopeKey(key) {
			continue
		}
		callCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		r, _, err := q.read(callCtx, key)
		if err == nil && registryCleanupActive(r) {
			id := registryCleanupScopeID(r)
			if allow != nil && allow(id) {
				out.observeActive(r.RegistryCleanup)
				hintErr := q.syncRegistryCleanupHint(callCtx, key, r.RegistryCleanup)
				if hintErr != nil {
					out.HintRepairFailures++
				}
				if !q.now().Before(r.RegistryCleanup.NotBefore) {
					var attempted bool
					attempted, err = q.runRegistryCleanupAttempt(callCtx, key, id, r)
					if attempted {
						out.Attempted++
					}
				}
				err = errors.Join(err, hintErr)
			}
		}
		stop()
		if err != nil {
			out.Failed++
		}
	}
	return out, nil
}
func (q *Quotas) runRegistryCleanupAttempt(ctx context.Context, key string, id durableentity.ID, root quotaRecord) (bool, error) {
	r, version, err := q.readRegistryWorker(ctx, key)
	if err != nil {
		return false, err
	}
	now := q.now()
	if r.Claim != "" && now.Before(r.LeaseUntil) {
		return false, nil
	}
	c := root.RegistryCleanup
	if r.OperationID == c.OperationID && now.Before(r.NextAttemptAt) {
		return false, nil
	}
	if r.OperationID != c.OperationID {
		r = registryWorkerRecord{Schema: 1, Scope: key, RegistryCleanupWorkerStatus: RegistryCleanupWorkerStatus{OperationID: c.OperationID}}
	}
	if r.ObservedRevision != c.Revision {
		r.Failures = 0
		r.LastError = ""
	}
	r.ObservedRevision = c.Revision
	r.LastAttemptAt = now
	r.NextAttemptAt = now.Add(RegistryCleanupWorkerPoll)
	r.Claim = uuid.NewString()
	r.LeaseUntil = now.Add(registryWorkerLease)
	version, err = q.putRegistryWorker(ctx, r, version)
	if err != nil {
		return false, err
	} // Unknown claim acknowledgement confers no work authority.
	stepCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	var next *RegistryCleanup
	if root.Limits != q.Limits {
		err = ErrPolicyMismatch
	} else {
		next, err = q.StepRegistryCleanup(stepCtx, id, c.OperationID, c.Revision, false)
	}
	stop()
	if err == nil {
		r.ObservedRevision = next.Revision
		r.LastSuccessAt = q.now()
		r.Failures = 0
		r.LastError = ""
		r.NextAttemptAt = q.now().Add(RegistryCleanupWorkerPoll)
	} else {
		r.Failures = min(10, r.Failures+1)
		r.LastError = workerError(err)
		delay := min(5*time.Minute, RegistryCleanupWorkerPoll*time.Duration(1<<r.Failures))
		r.NextAttemptAt = q.now().Add(delay)
	}
	r.Claim = ""
	r.LeaseUntil = time.Time{}
	// This CAS cannot overwrite a replacement worker's claim. If observation
	// acknowledgement is lost, next discovery inspects the root before any step.
	_, recordErr := q.putRegistryWorker(ctx, r, version)
	return true, errors.Join(err, recordErr)
}

// Verify discovery and sidecar permissions without stepping any scope or
// touching another process's objects. Existing cleanup's probe covers deletion.
func (q *Quotas) CheckRegistryCleanupWorker(ctx context.Context) error {
	store, ok := q.Store.(durableentity.CleanupStore)
	if !ok {
		return durableentity.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityCleanupTimeout)
	defer cancel()
	page, err := store.ListEntityObjects(ctx, "tenant-quotas/v1/", "", registryWorkerPageSize)
	if err != nil {
		return err
	}
	if len(page.Keys) > registryWorkerPageSize || !validRegistryCleanupPage("tenant-quotas/v1/", "", page) {
		return durableentity.ErrCorrupt
	}
	key := registryWorkerPrefix + "probes/" + uuid.NewString() + "/probe.json"
	body := []byte(`{"probe":true}`)
	version, err := q.Store.Put(ctx, key, body, "")
	if err != nil || version == "" {
		return errors.Join(durableentity.ErrUncertain, err)
	}
	version, err = q.Store.Put(ctx, key, body, version)
	if err != nil || version == "" {
		return errors.Join(durableentity.ErrUncertain, err)
	}
	if err = store.DeleteEntityObject(ctx, key); err != nil {
		return err
	}
	if _, _, err = q.Store.Get(ctx, key, 4096); !errors.Is(err, durableentity.ErrNotFound) {
		return durableentity.ErrCorrupt
	}
	return nil
}

// Counts describe this bounded page before stepping, not a fleet backlog.
func (p *RegistryCleanupWorkerPage) observeActive(c *RegistryCleanup) {
	p.Active++
	if p.OldestActiveAt.IsZero() || c.At.Before(p.OldestActiveAt) {
		p.OldestActiveAt = c.At
	}
}
