package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ QueueLeaseStore = (*MemStore)(nil)

// pullBindingLocked finds the enabled production pull binding for a queue.
func (m *MemStore) pullBindingLocked(accountID, appID, queueName string) (QueueBinding, bool) {
	var found QueueBinding
	for _, b := range m.queueBindings {
		if b.AccountID != accountID || b.AppID != appID || b.QueueName != queueName || b.DeploymentScope != "" ||
			b.Mode != "pull" || !b.Enabled || b.RetiredAt != nil {
			continue
		}
		if found.ID == "" || b.CreatedAt.Before(found.CreatedAt) || (b.CreatedAt.Equal(found.CreatedAt) && b.ID < found.ID) {
			found = b
		}
	}
	return found, found.ID != ""
}

// queueLeaseRowLocked reports whether inv is production work for the binding.
func (m *MemStore) queueLeaseRowLocked(inv Invocation, appID string, binding QueueBinding) bool {
	if inv.AppID != appID || inv.Source != InvocationQueue || inv.EnvironmentID != "" || inv.WorkPolicyName != "" || inv.QuotaReserved {
		return false
	}
	if _, owned := m.invocationEnvironmentQueueAdmissions[inv.ID]; owned {
		return false
	}
	if _, owned := m.invocationEnvironmentQueueReceipts[inv.ID]; owned {
		return false
	}
	return inv.QueueBindingID == binding.ID || (inv.QueueBindingID == "" && inv.QueueName == binding.QueueName)
}

func (m *MemStore) LeaseQueueMessages(_ context.Context, req QueueLeaseRequest) ([]QueueLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	account, ok := m.accounts[req.AccountID]
	if !ok {
		return nil, ErrNotFound
	}
	messages, visibility, err := queueLeaseLimits(account, req)
	if err != nil {
		return nil, err
	}
	binding, ok := m.pullBindingLocked(req.AccountID, req.AppID, req.QueueName)
	if !ok {
		return nil, ErrNotFound
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	var ready []Invocation
	active := 0
	for _, inv := range m.invocations {
		if !m.queueLeaseRowLocked(inv, req.AppID, binding) {
			continue
		}
		switch {
		case inv.State == InvocationDispatching && inv.LeaseExpiresAt != nil && inv.LeaseExpiresAt.After(now):
			active++
		case inv.State == InvocationPending && !inv.DueAt.After(now) && (inv.DeadlineAt == nil || inv.DeadlineAt.After(now)):
			ready = append(ready, inv)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return environmentQueueInvocationBefore(ready[i], ready[j]) })
	messages = min(messages, binding.MaxConcurrency-active)
	limits := api.MustLimitsFor(account.Plan)
	leases := []QueueLease{}
	usedBytes := 0
	for _, inv := range ready {
		if len(leases) >= messages {
			break
		}
		if environmentQueueDeliveryAttemptsExhausted(inv, limits) {
			inv = exhaustEnvironmentQueueDelivery(inv, now)
			inv.LastError = "queue lease attempt budget exhausted after lease expiry"
			m.invocations[inv.ID] = inv
			continue
		}
		if !queueLeaseFits(len(leases), usedBytes, inv) {
			break
		}
		usedBytes += queueLeaseBytes(inv)
		receipt, digest, err := newQueueLeaseReceipt(inv.ID)
		if err != nil {
			return nil, err
		}
		until := now.Add(time.Duration(visibility) * time.Second)
		if inv.ReceivedAt == nil {
			inv.ReceivedAt = &now
		}
		inv.State, inv.LeaseExpiresAt, inv.InstanceID = InvocationDispatching, &until, req.InstanceID
		inv.Attempts++
		m.invocations[inv.ID] = inv
		m.invocationQueueLeaseReceipts[inv.ID] = queueLeaseReceipt{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration, TokenHash: digest}
		leases = append(leases, QueueLease{Invocation: inv, Receipt: receipt})
	}
	return leases, nil
}

// heldQueueLeaseLocked returns the message a receipt still owns, or the
// failure reason.
func (m *MemStore) heldQueueLeaseLocked(accountID, appID, queueName, receipt string, now time.Time) (Invocation, string) {
	id, digest, ok := parseQueueLeaseReceipt(receipt)
	if !ok {
		return Invocation{}, QueueLeaseReasonInvalid
	}
	inv, found := m.invocations[id]
	if !found || inv.AccountID != accountID || inv.AppID != appID {
		return Invocation{}, QueueLeaseReasonInvalid
	}
	held, issued := m.invocationQueueLeaseReceipts[id]
	if !issued || !queueLeaseOwned(inv, accountID, appID, queueName, held, digest, now) {
		return Invocation{}, QueueLeaseReasonExpired
	}
	return inv, ""
}

func (m *MemStore) AckQueueLeases(_ context.Context, accountID, appID, queueName string, receipts []string) (QueueLeaseOutcome, error) {
	return m.settleQueueLeases(receipts, func(receipt string) (string, error) {
		m.mu.Lock()
		inv, reason := m.heldQueueLeaseLocked(accountID, appID, queueName, receipt, time.Now().UTC())
		m.mu.Unlock()
		if reason != "" {
			return reason, nil
		}
		claim := InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}
		if err := m.completeInvocation(inv.ID, inv.Attempts, &claim, nil); err != nil {
			if errors.Is(err, ErrNotFound) {
				return QueueLeaseReasonExpired, nil
			}
			return "", err
		}
		m.mu.Lock()
		delete(m.invocationQueueLeaseReceipts, inv.ID)
		m.mu.Unlock()
		return "", nil
	})
}

func (m *MemStore) NackQueueLeases(_ context.Context, accountID, appID, queueName string, receipts []string, delay time.Duration, lastError string) (QueueLeaseOutcome, error) {
	return m.settleQueueLeases(receipts, func(receipt string) (string, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		now := time.Now().UTC().Truncate(time.Microsecond)
		inv, reason := m.heldQueueLeaseLocked(accountID, appID, queueName, receipt, now)
		if reason != "" {
			return reason, nil
		}
		next, err := queueLeaseRetry(inv, m.accounts[accountID].Plan, now, delay, lastError)
		if err != nil {
			return "", err
		}
		m.invocations[inv.ID] = next
		delete(m.invocationQueueLeaseReceipts, inv.ID)
		return "", nil
	})
}

func (m *MemStore) ExtendQueueLeases(_ context.Context, accountID, appID, queueName string, receipts []string, visibilitySeconds int) (QueueLeaseOutcome, error) {
	m.mu.Lock()
	account, ok := m.accounts[accountID]
	m.mu.Unlock()
	if !ok {
		return QueueLeaseOutcome{}, ErrNotFound
	}
	_, visibility, err := queueLeaseLimits(account, QueueLeaseRequest{QueueName: queueName, MaxMessages: 1, VisibilitySeconds: visibilitySeconds})
	if err != nil {
		return QueueLeaseOutcome{}, err
	}
	return m.settleQueueLeases(receipts, func(receipt string) (string, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		now := time.Now().UTC().Truncate(time.Microsecond)
		inv, reason := m.heldQueueLeaseLocked(accountID, appID, queueName, receipt, now)
		if reason != "" {
			return reason, nil
		}
		until := now.Add(time.Duration(visibility) * time.Second)
		inv.LeaseExpiresAt = &until
		m.invocations[inv.ID] = inv
		return "", nil
	})
}

func (m *MemStore) settleQueueLeases(receipts []string, apply func(string) (string, error)) (QueueLeaseOutcome, error) {
	if len(receipts) == 0 || len(receipts) > api.QueueLeaseMaxReceipts {
		return QueueLeaseOutcome{}, ErrInvalidArgument
	}
	out := QueueLeaseOutcome{Succeeded: []string{}, Failed: []QueueLeaseFailure{}}
	for _, receipt := range receipts {
		reason, err := apply(receipt)
		if err != nil {
			return QueueLeaseOutcome{}, err
		}
		if reason == "" {
			out.Succeeded = append(out.Succeeded, receipt)
		} else {
			out.Failed = append(out.Failed, QueueLeaseFailure{Receipt: receipt, Reason: reason})
		}
	}
	return out, nil
}

func (m *MemStore) QueueLeasesByInstance(_ context.Context, appID string) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	out := map[string]int{}
	for _, inv := range m.invocations {
		if inv.AppID == appID && inv.Source == InvocationQueue && inv.State == InvocationDispatching &&
			inv.InstanceID != "" && inv.LeaseExpiresAt != nil && inv.LeaseExpiresAt.After(now) {
			out[inv.InstanceID]++
		}
	}
	return out, nil
}
