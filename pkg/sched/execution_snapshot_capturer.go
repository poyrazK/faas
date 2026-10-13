package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// runtimeSnapshotCaptureTimeout bounds one capture: cold boot, guest
	// readiness, pause/snapshot, and the one-time digest of the objects.
	runtimeSnapshotCaptureTimeout = 3 * time.Minute
	// runtimeSnapshotCaptureRetryAfter keeps a failing identity from turning
	// every cold-booted Run into another capture attempt.
	runtimeSnapshotCaptureRetryAfter = 10 * time.Minute
	runtimeSnapshotCaptureQueue      = 16
)

// RuntimeSnapshotCaptureVMM is the narrow vmmd surface one capture needs. The
// production VMMRouter implements it.
type RuntimeSnapshotCaptureVMM interface {
	RestoreExecution(context.Context, string, ExecutionRestoreRequest) (*ExecutionRestoreOutcome, error)
	PauseAndSnapshot(ctx context.Context, nodeID, instance, vmstatePath, storageKey, vmstateStorageKey string, beforeCheckpoint bool) (SnapshotBytes, error)
	Destroy(ctx context.Context, nodeID, instance string) error
}

// RuntimeSnapshotRegistrar catalogs a capture vmmd already stored.
type RuntimeSnapshotRegistrar interface {
	RegisterStoredRuntimeSnapshot(ctx context.Context, identity RuntimeSnapshotIdentity, storageKey string, memBytes, vmstateBytes int64, createdAt time.Time) (RuntimeSnapshot, error)
}

type runtimeSnapshotCaptureJob struct {
	key      string
	identity RuntimeSnapshotIdentity
	template ExecutionRestoreRequest
}

// RuntimeSnapshotCapturer fills runtime snapshot catalog misses in the
// background (ADR-171 snapshot contract). Each capture cold-boots a dedicated
// payload-free execution VM with exactly the missing identity's machine,
// waits for the guest to be listening, parks it into fresh storage keys, and
// registers the pair. No tenant source, input, or identity ever enters the
// captured VM: the template carries only the machine shape and host-side
// accounting fields of the Run that observed the miss. The Run that observed
// the miss is unaffected; it cold-boots as before.
type RuntimeSnapshotCapturer struct {
	vmm       RuntimeSnapshotCaptureVMM
	registrar RuntimeSnapshotRegistrar
	log       *slog.Logger
	now       func() time.Time
	newID     func() string

	mu       sync.Mutex
	pending  map[string]struct{}
	failedAt map[string]time.Time
	jobs     chan runtimeSnapshotCaptureJob
}

func NewRuntimeSnapshotCapturer(vmm RuntimeSnapshotCaptureVMM, registrar RuntimeSnapshotRegistrar, log *slog.Logger) *RuntimeSnapshotCapturer {
	if log == nil {
		log = slog.Default()
	}
	return &RuntimeSnapshotCapturer{
		vmm: vmm, registrar: registrar, log: log, now: time.Now, newID: uuid.NewString,
		pending: map[string]struct{}{}, failedAt: map[string]time.Time{},
		jobs: make(chan runtimeSnapshotCaptureJob, runtimeSnapshotCaptureQueue),
	}
}

// Request queues a capture for identity unless one is pending, recently
// failed, or the queue is full. It never blocks a Run's claim path.
func (c *RuntimeSnapshotCapturer) Request(identity RuntimeSnapshotIdentity, template ExecutionRestoreRequest) bool {
	if c == nil {
		return false
	}
	key, err := identity.Key()
	if err != nil || template.NodeID == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.pending[key]; ok {
		return false
	}
	if failed, ok := c.failedAt[key]; ok && c.now().Sub(failed) < runtimeSnapshotCaptureRetryAfter {
		return false
	}
	select {
	case c.jobs <- runtimeSnapshotCaptureJob{key: key, identity: identity, template: captureTemplate(template)}:
		c.pending[key] = struct{}{}
		return true
	default:
		return false
	}
}

// captureTemplate keeps only the machine envelope. Host-side broker grants
// and the observing Run's snapshot/deadline are dropped.
func captureTemplate(request ExecutionRestoreRequest) ExecutionRestoreRequest {
	request.ID = ""
	request.LeaseToken = ""
	request.OutboundIntegrationIDs = nil
	request.Snapshot = SnapshotRef{}
	request.DeadlineAt = time.Time{}
	return request
}

// Run processes captures one at a time until ctx is cancelled. Captures are
// serialized so a burst of misses cannot boot many extra VMs on one node.
func (c *RuntimeSnapshotCapturer) Run(ctx context.Context) {
	if c == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-c.jobs:
			err := c.capture(ctx, job)
			c.mu.Lock()
			delete(c.pending, job.key)
			if err != nil {
				c.failedAt[job.key] = c.now()
			} else {
				delete(c.failedAt, job.key)
			}
			c.mu.Unlock()
			if err != nil && !errors.Is(err, context.Canceled) {
				c.log.Warn("schedd: runtime snapshot capture failed", "runtime", job.identity.Runtime,
					"memory_mb", job.identity.MemoryMB, "error_class", executionErrorClass(err), "err", err)
			}
		}
	}
}

func (c *RuntimeSnapshotCapturer) capture(parent context.Context, job runtimeSnapshotCaptureJob) error {
	if c.vmm == nil || c.registrar == nil {
		return ErrRuntimeSnapshotUnwired
	}
	ctx, cancel := context.WithTimeout(parent, runtimeSnapshotCaptureTimeout)
	defer cancel()
	request := job.template
	request.ID = c.newID()
	request.DeadlineAt = c.now().Add(runtimeSnapshotCaptureTimeout)
	started := c.now()
	outcome, err := c.vmm.RestoreExecution(ctx, request.NodeID, request)
	if err != nil {
		return fmt.Errorf("sched: capture boot: %w", err)
	}
	if outcome == nil || outcome.Instance != request.ID {
		destroyCtx, cancelDestroy := context.WithTimeout(context.WithoutCancel(parent), defaultExecutionDestroyTimeout)
		defer cancelDestroy()
		return errors.Join(errors.New("sched: capture boot returned an unexpected instance"),
			c.vmm.Destroy(destroyCtx, request.NodeID, request.ID))
	}
	storageKey, vmstateKey := NewRuntimeSnapshotCaptureKey()
	// PauseAndSnapshot waits for the guest's resume-hook acknowledgement,
	// snapshots, and destroys the VM on every path.
	bytes, err := c.vmm.PauseAndSnapshot(ctx, request.NodeID, request.ID, "", storageKey, vmstateKey, false)
	if err != nil {
		return fmt.Errorf("sched: capture snapshot: %w", err)
	}
	entry, err := c.registrar.RegisterStoredRuntimeSnapshot(ctx, job.identity, storageKey, bytes.MemBytes, bytes.VMStateBytes, c.now())
	if errors.Is(err, ErrRuntimeSnapshotConflict) {
		// Another schedd published the same identity first.
		return nil
	}
	if err != nil {
		return fmt.Errorf("sched: register capture: %w", err)
	}
	c.log.Info("schedd: runtime snapshot captured", "runtime", job.identity.Runtime,
		"memory_mb", job.identity.MemoryMB, "mem_bytes", entry.MemBytes,
		"duration_ms", c.now().Sub(started).Milliseconds())
	return nil
}
