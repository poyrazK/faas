package sched

// Warm sandbox session snapshots (ADR-20261027090013 §12). After a shape's
// first cold-booted session on a node ends cleanly, schedd asks that node's
// vmmd to capture the shape: the same drives, machine and network mode,
// booted with no session data and no template command. Later sessions of
// the shape restore it; one that cold-boots instead drops it, and the next
// clean cold boot captures again. Snapshots are per schedd process, like
// the agent preset snapshots, and a snapshot is cache (ADR-005).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// sandboxSnapshotShape is a warm snapshot's slot: one node, the session's
// drives (a template's build app, not its build, so a rebuild replaces its
// slot), machine and network mode.
type sandboxSnapshotShape struct {
	node, base, drive, account string
	vcpu, memMiB, cpuMilli     int
	network                    string
}

// sandboxSnapshot is a captured snapshot and the exact drive1 it holds.
type sandboxSnapshot struct {
	snap     *AgentRunSnapshot
	driveKey string
}

// sandboxSnapshotCaptureTimeout bounds one capture.
const sandboxSnapshotCaptureTimeout = 3 * time.Minute

// sandboxDriveKey is the drive1 a session boots.
func sandboxDriveKey(spec SandboxSpec) string {
	if spec.ImageKey != "" {
		return spec.ImageKey
	}
	return spec.LayerKey
}

func (r *AgentRunner) sandboxShape(node string, spec SandboxSpec) sandboxSnapshotShape {
	shape := sandboxSnapshotShape{node: node, base: spec.BaseKey, drive: spec.LayerKey, vcpu: spec.VCPU, memMiB: spec.MemSizeMiB,
		cpuMilli: spec.CPUMillicores, network: spec.Network}
	if spec.ImageKey != "" {
		// A template's snapshot holds the account's image: its slot is the
		// account's own.
		shape.drive, shape.account = storagekey.SandboxImageIdentity(spec.ImageKey), spec.AccountID
	}
	return shape
}

// sandboxSnapshotFor is the shape's snapshot when it holds this session's
// exact drive1; a template rebuilt since its capture cold-boots.
func (r *AgentRunner) sandboxSnapshotFor(shape sandboxSnapshotShape, spec SandboxSpec) *AgentRunSnapshot {
	if !r.cfg.SandboxSnapshots {
		return nil
	}
	r.snapMu.Lock()
	defer r.snapMu.Unlock()
	if s, ok := r.sandboxSnaps[shape]; ok && s.driveKey == sandboxDriveKey(spec) {
		return s.snap
	}
	return nil
}

func (r *AgentRunner) dropSandboxSnapshot(shape sandboxSnapshotShape, snap *AgentRunSnapshot) {
	r.snapMu.Lock()
	defer r.snapMu.Unlock()
	if s, ok := r.sandboxSnaps[shape]; ok && s.snap == snap {
		delete(r.sandboxSnaps, shape)
	}
}

// claimSandboxCapture reports whether this caller should capture the
// shape: snapshots are on, no capture runs, and the shape's slot is empty or
// holds another build. A session that cold-booted while another session's
// capture of the same build ran finds that capture in the slot and leaves
// it. The slots are bounded.
func (r *AgentRunner) claimSandboxCapture(shape sandboxSnapshotShape, spec SandboxSpec) bool {
	if !r.cfg.SandboxSnapshots {
		return false
	}
	r.snapMu.Lock()
	defer r.snapMu.Unlock()
	if r.sandboxCapturing[shape] {
		return false
	}
	s, ok := r.sandboxSnaps[shape]
	if ok && s.driveKey == sandboxDriveKey(spec) {
		return false
	}
	if !ok && len(r.sandboxSnaps)+len(r.sandboxCapturing) >= api.SandboxWarmSnapshotsMax {
		return false
	}
	r.sandboxCapturing[shape] = true
	return true
}

// captureSandbox asks the node's vmmd for the shape's warm snapshot. Its
// keys are fixed per slot, so a later capture replaces this one's objects
// instead of adding more.
func (r *AgentRunner) captureSandbox(ctx context.Context, p AgentRunPlacement, spec SandboxSpec, shape sandboxSnapshotShape, release func()) {
	defer release()
	ctx, cancel := context.WithTimeout(ctx, sandboxSnapshotCaptureTimeout)
	defer cancel()
	capture := SandboxSpec{Instance: p.Instance, SandboxID: spec.SandboxID, AccountID: spec.AccountID, Plan: spec.Plan,
		BaseKey: spec.BaseKey, LayerKey: spec.LayerKey, ImageKey: spec.ImageKey, VCPU: spec.VCPU, MemSizeMiB: spec.MemSizeMiB,
		CPUMillicores: spec.CPUMillicores, Network: spec.Network}
	namespace := storagekey.SandboxSnapshotNamespace(spec.AccountID, spec.ImageKey != "")
	var snap *AgentRunSnapshot
	router, ok := r.router.(sandboxCaptureRouter)
	err := fmt.Errorf("sched: the vmmd router cannot capture sandbox sessions")
	if ok {
		snap, err = router.CaptureSandboxSnapshot(ctx, p.NodeID, capture, namespace, sandboxCaptureID(shape))
	}
	r.snapMu.Lock()
	delete(r.sandboxCapturing, shape)
	if err == nil {
		r.sandboxSnaps[shape] = sandboxSnapshot{snap: snap, driveKey: sandboxDriveKey(spec)}
	}
	r.snapMu.Unlock()
	if err != nil {
		slog.Default().Warn("sched: sandbox snapshot capture failed", "node", p.NodeID, "drive", shape.drive, "network", shape.network, "err", err)
	}
}

// sandboxCaptureID names a slot's capture keys.
func sandboxCaptureID(shape sandboxSnapshotShape) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%s", shape.base, shape.drive, shape.account,
		shape.vcpu, shape.memMiB, shape.cpuMilli, shape.network)))
	return hex.EncodeToString(sum[:16])
}

// sandboxCaptureRouter is the VMMRouter surface captures use.
type sandboxCaptureRouter interface {
	CaptureSandboxSnapshot(ctx context.Context, nodeID string, spec SandboxSpec, namespace, captureID string) (*AgentRunSnapshot, error)
}

// CaptureSandboxSnapshot captures a session's warm snapshot on nodeID's vmmd.
func (r *VMMRouter) CaptureSandboxSnapshot(ctx context.Context, nodeID string, spec SandboxSpec, namespace, captureID string) (*AgentRunSnapshot, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	c, ok := cli.(interface {
		CaptureSandboxSnapshot(context.Context, SandboxSpec, string, string) (*AgentRunSnapshot, error)
	})
	if !ok {
		return nil, api.NewProblem(501, api.CodeNotImplemented, "Sandbox sessions unavailable", "vmmd client does not support sandbox snapshots")
	}
	return c.CaptureSandboxSnapshot(ctx, spec, namespace, captureID)
}

// sandboxSnapshotDeleter is the VMMRouter surface the template sweep uses.
type sandboxSnapshotDeleter interface {
	DeleteSandboxSnapshot(ctx context.Context, nodeID string, snap *AgentRunSnapshot) error
}

// DeleteSandboxSnapshot removes a warm session snapshot's objects on
// nodeID's vmmd.
func (r *VMMRouter) DeleteSandboxSnapshot(ctx context.Context, nodeID string, snap *AgentRunSnapshot) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	c, ok := cli.(interface {
		DeleteSandboxSnapshot(context.Context, *AgentRunSnapshot) error
	})
	if !ok {
		return api.NewProblem(501, api.CodeNotImplemented, "Sandbox sessions unavailable", "vmmd client does not support sandbox snapshots")
	}
	return c.DeleteSandboxSnapshot(ctx, snap)
}

// sandboxTemplateSlug is the build app slug of a template slot, or "" for a
// preset or seller-image slot.
func sandboxTemplateSlug(shape sandboxSnapshotShape) string {
	slug, ok := strings.CutPrefix(shape.drive, "apps/")
	if !ok || shape.account == "" || !api.IsSandboxTemplateAppSlug(slug) {
		return ""
	}
	return slug
}

// sandboxTemplateGone reports whether a template slot's build app is
// deleted, or no longer the slot account's template: no session will
// restore the slot again.
func (r *AgentRunner) sandboxTemplateGone(ctx context.Context, shape sandboxSnapshotShape, slug string) (bool, error) {
	app, err := r.engine.store.AppBySlug(ctx, slug)
	if errors.Is(err, state.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("sched: read template build app %s: %w", slug, err)
	}
	return app.Status == state.AppDeleted || !app.SandboxTemplate || app.AccountID != shape.account, nil
}

// SweepSandboxTemplateSnapshots drops the warm snapshots of templates whose
// build app is gone and deletes their objects on the node
// (ADR-20261027090013 §12). A slot whose objects could not be deleted is
// dropped anyway and its failure logged: its keys are fixed per slot, and
// nothing restores it again. It returns how many slots it dropped.
func (r *AgentRunner) SweepSandboxTemplateSnapshots(ctx context.Context) int {
	type slot struct {
		shape sandboxSnapshotShape
		snap  *AgentRunSnapshot
		slug  string
	}
	r.snapMu.Lock()
	var slots []slot
	for shape, s := range r.sandboxSnaps {
		if slug := sandboxTemplateSlug(shape); slug != "" {
			slots = append(slots, slot{shape: shape, snap: s.snap, slug: slug})
		}
	}
	r.snapMu.Unlock()
	deleter, canDelete := r.router.(sandboxSnapshotDeleter)
	dropped := 0
	for _, s := range slots {
		gone, err := r.sandboxTemplateGone(ctx, s.shape, s.slug)
		if err != nil {
			slog.Default().Warn("sched: sandbox template snapshot sweep", "app", s.slug, "err", err)
			continue
		}
		if !gone {
			continue
		}
		r.dropSandboxSnapshot(s.shape, s.snap)
		dropped++
		if !canDelete {
			continue
		}
		if err := deleter.DeleteSandboxSnapshot(ctx, s.shape.node, s.snap); err != nil {
			slog.Default().Warn("sched: delete a deleted template's sandbox snapshot", "node", s.shape.node, "app", s.slug,
				"key", s.snap.StorageKey, "err", err)
		}
	}
	return dropped
}

// RunSandboxTemplateSnapshotSweep sweeps every
// api.SandboxTemplateSnapshotSweepSec until ctx ends. It does nothing while
// warm session snapshots are off.
func (r *AgentRunner) RunSandboxTemplateSnapshotSweep(ctx context.Context) {
	if !r.cfg.SandboxSnapshots {
		return
	}
	t := time.NewTicker(time.Duration(api.SandboxTemplateSnapshotSweepSec) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.SweepSandboxTemplateSnapshots(ctx)
		}
	}
}
