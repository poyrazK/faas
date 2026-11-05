package state

// Admitted runtimes survive a vmmd restart (ADR-595 extension,
// docs/adr/20261018-runtime-admission-rebind.md). A re-bind records that an
// instance's admission, issued under incarnation From, is held by the node's
// current incarnation To since ownership epoch VMMDEpoch. Receipts and
// bindings never change; the runtime guards resolve a receipt's issuing
// incarnation through the instance's re-binds. schedd writes them from the
// successor's adoption report, under the epoch fence.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ApplicationStandardRebind is one hop of an admission's holder chain.
type ApplicationStandardRebind struct {
	InstanceID      string
	NodeID          string
	FromIncarnation string
	ToIncarnation   string
	VMMDEpoch       uint64
	Observed        ApplicationStandardRebindObserved
	CreatedAt       time.Time
}

// ApplicationStandardRebindObserved is what the successor saw of the adopted
// guest; it must equal the instance's current receipt.
type ApplicationStandardRebindObserved struct {
	Token               string `json:"token"`
	CompletedAtUnixNano int64  `json:"completed_at_unix_nano"`
	Netns               string `json:"netns"`
	HostIP              string `json:"host_ip"`
	LeaseUID            int32  `json:"lease_uid"`
	Paused              bool   `json:"paused"`
}

// ApplicationStandardRebindStore records and lists re-binds. Recording the
// same hop again is a no-op, so a retried report write is safe.
// ErrApplicationStandardRuntimeStale means the hop does not extend the
// admission's current holder to the node's current one, or the instance's
// residency no longer equals its receipt.
type ApplicationStandardRebindStore interface {
	RecordApplicationStandardRebind(ctx context.Context, r ApplicationStandardRebind) error
	// ListApplicationStandardRebinds returns an instance's hops, oldest first.
	ListApplicationStandardRebinds(ctx context.Context, instanceID string) ([]ApplicationStandardRebind, error)
}

var (
	_ ApplicationStandardRebindStore = (*PgStore)(nil)
	_ ApplicationStandardRebindStore = (*MemStore)(nil)
)

func (r ApplicationStandardRebind) validArgs() bool {
	for _, id := range []string{r.InstanceID, r.NodeID, r.FromIncarnation, r.ToIncarnation} {
		if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil || parsed.String() != id {
			return false
		}
	}
	o := r.Observed
	return r.FromIncarnation != r.ToIncarnation && r.VMMDEpoch > 0 && r.VMMDEpoch < 1<<62 &&
		o.Token != "" && o.CompletedAtUnixNano > 0 && o.Netns != "" && o.HostIP != "" && o.LeaseUID > 0
}

// sameHop reports whether two re-binds record the same hop.
func (r ApplicationStandardRebind) sameHop(other ApplicationStandardRebind) bool {
	return r.InstanceID == other.InstanceID && r.NodeID == other.NodeID && r.FromIncarnation == other.FromIncarnation &&
		r.ToIncarnation == other.ToIncarnation && r.VMMDEpoch == other.VMMDEpoch && r.Observed == other.Observed
}

func (s *PgStore) RecordApplicationStandardRebind(ctx context.Context, r ApplicationStandardRebind) error {
	if !r.validArgs() {
		return ErrInvalidArgument
	}
	observed, err := json.Marshal(r.Observed)
	if err != nil {
		return fmt.Errorf("record application standard rebind: %w", err)
	}
	err = sqlc.New().InsertApplicationStandardRebind(ctx, s.pool, sqlc.InsertApplicationStandardRebindParams{
		InstanceID: mustPgUUID(r.InstanceID), NodeID: mustPgUUID(r.NodeID),
		FromIncarnation: mustPgUUID(r.FromIncarnation), ToIncarnation: mustPgUUID(r.ToIncarnation),
		VmmdEpoch: int64(r.VMMDEpoch), Observed: observed,
	})
	if err == nil {
		return nil
	}
	err = mapErr(err)
	if errors.Is(err, ErrApplicationStandardRuntimeStale) || errors.Is(err, ErrConflict) {
		if recorded, lerr := s.ListApplicationStandardRebinds(ctx, r.InstanceID); lerr == nil && containsRebindHop(recorded, r) {
			return nil
		}
	}
	return fmt.Errorf("record application standard rebind: %w", err)
}

func (s *PgStore) ListApplicationStandardRebinds(ctx context.Context, instanceID string) ([]ApplicationStandardRebind, error) {
	if _, err := uuid.Parse(instanceID); err != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListApplicationStandardRebinds(ctx, s.pool, mustPgUUID(instanceID))
	if err != nil {
		return nil, fmt.Errorf("list application standard rebinds: %w", mapErr(err))
	}
	out := make([]ApplicationStandardRebind, 0, len(rows))
	for _, row := range rows {
		hop := ApplicationStandardRebind{
			InstanceID: pgUUIDString(row.InstanceID), NodeID: pgUUIDString(row.NodeID),
			FromIncarnation: pgUUIDString(row.FromIncarnation), ToIncarnation: pgUUIDString(row.ToIncarnation),
			VMMDEpoch: uint64(row.VmmdEpoch), CreatedAt: timeFromPgtype(row.CreatedAt),
		}
		if err := json.Unmarshal(row.Observed, &hop.Observed); err != nil {
			return nil, fmt.Errorf("list application standard rebinds: observed: %w", err)
		}
		out = append(out, hop)
	}
	return out, nil
}

func containsRebindHop(recorded []ApplicationStandardRebind, r ApplicationStandardRebind) bool {
	for _, hop := range recorded {
		if hop.sameHop(r) {
			return true
		}
	}
	return false
}

// MemStore mirrors application_standard_rebind_guard.
func (m *MemStore) RecordApplicationStandardRebind(ctx context.Context, r ApplicationStandardRebind) error {
	if !r.validArgs() {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if containsRebindHop(m.instanceApplicationStandardRebinds[r.InstanceID], r) {
		return nil
	}
	if err := m.checkStandardRebindLocked(r); err != nil {
		return err
	}
	if m.instanceApplicationStandardRebinds == nil {
		m.instanceApplicationStandardRebinds = map[string][]ApplicationStandardRebind{}
	}
	r.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	m.instanceApplicationStandardRebinds[r.InstanceID] = append(m.instanceApplicationStandardRebinds[r.InstanceID], r)
	return nil
}

func (m *MemStore) checkStandardRebindLocked(r ApplicationStandardRebind) error {
	if _, ok := m.computeNodes[r.NodeID]; !ok {
		return ErrApplicationStandardRuntimeStale
	}
	epoch := m.computeNodeVMMDEpochs[r.NodeID]
	if m.computeNodeRuntimeIncarnations[r.NodeID] != r.ToIncarnation || epoch.Incarnation != r.ToIncarnation || epoch.Epoch != r.VMMDEpoch {
		return ErrApplicationStandardRuntimeStale
	}
	ins, ok := m.instances[r.InstanceID]
	if !ok || ins.NodeID != r.NodeID || (ins.State != string(StateRunning) && ins.State != string(StateWarm)) {
		return ErrApplicationStandardRuntimeStale
	}
	receipt, ok := m.currentStandardReceiptLocked(r.InstanceID)
	if !ok || m.effectiveStandardIncarnationLocked(r.InstanceID, receipt.Binding.Incarnation) != r.FromIncarnation {
		return ErrApplicationStandardRuntimeStale
	}
	from, fromOK := m.computeNodeRuntimeOrder[r.NodeID+"\x00"+r.FromIncarnation]
	to, toOK := m.computeNodeRuntimeOrder[r.NodeID+"\x00"+r.ToIncarnation]
	if !fromOK || !toOK || from >= to {
		return ErrApplicationStandardRuntimeStale
	}
	o := r.Observed
	if o.Token != receipt.Binding.Token || o.CompletedAtUnixNano != receipt.CompletedAtUnixNano ||
		o.Netns != receipt.Netns || ins.Netns != receipt.Netns || o.HostIP != receipt.HostIP || !sameHostIP(ins.HostIP, receipt.HostIP) ||
		o.LeaseUID != receipt.LeaseUID || ins.GuestUID != int(receipt.LeaseUID) || o.Paused != receipt.Paused || receipt.Paused != (ins.State == string(StateWarm)) {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func sameHostIP(a, b string) bool {
	pa, errA := netip.ParseAddr(a)
	pb, errB := netip.ParseAddr(b)
	return errA == nil && errB == nil && pa == pb
}

// currentStandardReceiptLocked is the instance's serving receipt: its
// promotion's when it has one, else its boot's.
func (m *MemStore) currentStandardReceiptLocked(id string) (runtimeadmission.Receipt, bool) {
	boot, ok := m.instanceApplicationStandardBoots[m.instanceApplicationStandardBootTokens[id]]
	if !ok || boot.Receipt == nil || boot.Binding.InstanceID != id {
		return runtimeadmission.Receipt{}, false
	}
	if token := m.instanceApplicationStandardPromotionTokens[id]; token != "" {
		p, ok := m.instanceApplicationStandardPromotions[token]
		if !ok || p.Receipt == nil || !p.Grant.Parent.Equal(*boot.Receipt) {
			return runtimeadmission.Receipt{}, false
		}
		return *p.Receipt, true
	}
	return *boot.Receipt, true
}

// effectiveStandardIncarnationLocked mirrors
// application_standard_effective_incarnation.
func (m *MemStore) effectiveStandardIncarnationLocked(instanceID, issued string) string {
	held := issued
	for hops := 0; hops < 4096; hops++ {
		next := ""
		for _, hop := range m.instanceApplicationStandardRebinds[instanceID] {
			if hop.FromIncarnation == held {
				next = hop.ToIncarnation
				break
			}
		}
		if next == "" {
			break
		}
		held = next
	}
	return held
}

// heldStandardIncarnationLocked is the incarnation a parent receipt must
// literally name for validateStandardBootBinding: its own when the node's
// current incarnation holds it through re-binds, else the current one.
func (m *MemStore) heldStandardIncarnationLocked(instanceID, nodeID, issued string) string {
	current := m.computeNodeRuntimeIncarnations[nodeID]
	if issued != current && m.effectiveStandardIncarnationLocked(instanceID, issued) == current {
		return issued
	}
	return current
}

func (m *MemStore) ListApplicationStandardRebinds(ctx context.Context, instanceID string) ([]ApplicationStandardRebind, error) {
	if _, err := uuid.Parse(instanceID); err != nil {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]ApplicationStandardRebind(nil), m.instanceApplicationStandardRebinds[instanceID]...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// eraseStandardRebindsForNodeLocked mirrors the cascade from a deleted node's
// incarnation history.
func (m *MemStore) eraseStandardRebindsForNodeLocked(nodeID string) {
	for id, hops := range m.instanceApplicationStandardRebinds {
		kept := hops[:0]
		for _, hop := range hops {
			if hop.NodeID != nodeID {
				kept = append(kept, hop)
			}
		}
		if len(kept) == 0 {
			delete(m.instanceApplicationStandardRebinds, id)
		} else {
			m.instanceApplicationStandardRebinds[id] = kept
		}
	}
}

// heldPromotionIssuer is the incarnation a promotion's parent is held from
// through re-binds: the parent's own when it is not the node's current one.
// Only call it with a parent the promotion lock accepted, which it does for
// another incarnation only through the instance's re-binds.
func heldPromotionIssuer(parent runtimeadmission.Receipt, current string) string {
	if parent.Binding.Incarnation != current {
		return parent.Binding.Incarnation
	}
	return ""
}
