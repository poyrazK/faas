package state

import (
	"bytes"
	"context"
	"sort"
	"time"
)

func (m *MemStore) PruneExpiredManagedRealtimeDirectMessageReceipts(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > ManagedRealtimeDirectMessageReceiptPruneMaxBatch {
		return 0, ErrManagedRealtimeDirectMessageInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var removed int64
	for key, receipt := range m.managedRealtimeDirectReceipts {
		if !receipt.expiresAt.After(now) {
			delete(m.managedRealtimeDirectReceipts, key)
			removed++
			if removed == int64(batch) {
				break
			}
		}
	}
	return removed, nil
}

func (m *MemStore) BeginManagedRealtimeDirectMessageReceipt(ctx context.Context, endpointID, messageID string, fingerprint []byte) (bool, bool, error) {
	if err := validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, fingerprint); err != nil {
		return false, false, err
	}
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	now := time.Now().UTC()
	key := managedRealtimeDirectMessageKey{endpointID: endpointID, messageID: messageID}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[endpointID]; !ok {
		return false, false, ErrNotFound
	}
	active := 0
	for candidate, receipt := range m.managedRealtimeDirectReceipts {
		if !receipt.expiresAt.After(now) {
			delete(m.managedRealtimeDirectReceipts, candidate)
			continue
		}
		if candidate.endpointID == endpointID {
			active++
		}
	}
	if existing := m.managedRealtimeDirectReceipts[key]; existing != nil {
		if !bytes.Equal(existing.fingerprint, fingerprint) {
			return false, false, ErrManagedRealtimeDirectMessageConflict
		}
		if existing.dispatchComplete {
			return false, false, nil
		}
		if existing.dispatchLeaseUntil.After(now) {
			return false, true, nil
		}
		existing.dispatchLeaseUntil = now.Add(ManagedRealtimeDirectMessageDispatchLease)
		return true, false, nil
	}
	if active >= ManagedRealtimeDirectMessageMaxReceipts {
		return false, false, ErrManagedRealtimeDirectMessageLimit
	}
	m.managedRealtimeDirectReceipts[key] = &managedRealtimeDirectMessageState{
		fingerprint: append([]byte(nil), fingerprint...), createdAt: now,
		expiresAt:          now.Add(ManagedRealtimeDirectMessageReceiptRetention),
		dispatchLeaseUntil: now.Add(ManagedRealtimeDirectMessageDispatchLease),
		deliveries:         make(map[string]managedRealtimeDirectMessageDeliveryState),
	}
	return true, false, nil
}

func (m *MemStore) RegisterManagedRealtimeDirectMessageTargets(ctx context.Context, endpointID, messageID, nodeID string, targets []ManagedRealtimeDirectMessageTarget) ([]string, error) {
	if err := validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, make([]byte, 32)); err != nil || validateManagedRealtimeDirectMessageNode(nodeID) != nil || validateManagedRealtimeDirectMessageTargets(targets) != nil {
		return nil, ErrManagedRealtimeDirectMessageInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	key := managedRealtimeDirectMessageKey{endpointID: endpointID, messageID: messageID}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt := m.managedRealtimeDirectReceipts[key]
	if receipt == nil || !receipt.expiresAt.After(now) {
		return nil, ErrNotFound
	}
	newCount := 0
	for _, target := range targets {
		if _, exists := receipt.deliveries[target.ConnectionID]; !exists {
			newCount++
		}
	}
	activeDeliveries := 0
	for candidate, candidateReceipt := range m.managedRealtimeDirectReceipts {
		if candidate.endpointID == endpointID && candidateReceipt.expiresAt.After(now) {
			activeDeliveries += len(candidateReceipt.deliveries)
		}
	}
	if activeDeliveries+newCount > ManagedRealtimeDirectMessageMaxDeliveries {
		return nil, ErrManagedRealtimeDirectMessageLimit
	}
	for _, target := range targets {
		if existing, ok := receipt.deliveries[target.ConnectionID]; ok && (existing.nodeID != nodeID || existing.ackSupported != target.AckSupported) {
			return nil, ErrManagedRealtimeDirectMessageInvalid
		}
	}
	created := make([]string, 0, newCount)
	for _, target := range targets {
		if _, ok := receipt.deliveries[target.ConnectionID]; ok {
			continue
		}
		queueStatus := ManagedRealtimeDirectQueuePending
		if !target.AckSupported {
			queueStatus = ManagedRealtimeDirectQueueUnsupported
		}
		receipt.deliveries[target.ConnectionID] = managedRealtimeDirectMessageDeliveryState{
			connectionID: target.ConnectionID, nodeID: nodeID, ackSupported: target.AckSupported,
			queueStatus: queueStatus, createdAt: now,
		}
		created = append(created, target.ConnectionID)
	}
	sort.Strings(created)
	return created, nil
}

func (m *MemStore) UpdateManagedRealtimeDirectMessageDeliveries(ctx context.Context, endpointID, messageID, nodeID string, results []ManagedRealtimeDirectMessageDeliveryResult) error {
	if validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, make([]byte, 32)) != nil || validateManagedRealtimeDirectMessageNode(nodeID) != nil || validateManagedRealtimeDirectMessageResults(results) != nil {
		return ErrManagedRealtimeDirectMessageInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key := managedRealtimeDirectMessageKey{endpointID: endpointID, messageID: messageID}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt := m.managedRealtimeDirectReceipts[key]
	if receipt == nil || !receipt.expiresAt.After(time.Now().UTC()) {
		return ErrNotFound
	}
	for _, result := range results {
		delivery, ok := receipt.deliveries[result.ConnectionID]
		if !ok || delivery.nodeID != nodeID {
			return ErrNotFound
		}
	}
	now := time.Now().UTC()
	for _, result := range results {
		delivery := receipt.deliveries[result.ConnectionID]
		if delivery.acknowledgedAt == nil {
			delivery.queueStatus = result.QueueStatus
		}
		if result.QueueStatus == ManagedRealtimeDirectQueueQueued && delivery.queuedAt == nil {
			queuedAt := now
			delivery.queuedAt = &queuedAt
		}
		receipt.deliveries[result.ConnectionID] = delivery
	}
	return nil
}

func (m *MemStore) AcknowledgeManagedRealtimeDirectMessage(ctx context.Context, endpointID, messageID, nodeID, connectionID string) error {
	if validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, make([]byte, 32)) != nil || validateManagedRealtimeDirectMessageNode(nodeID) != nil || connectionID == "" || len(connectionID) > 128 {
		return ErrManagedRealtimeDirectMessageInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key := managedRealtimeDirectMessageKey{endpointID: endpointID, messageID: messageID}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt := m.managedRealtimeDirectReceipts[key]
	if receipt == nil || !receipt.expiresAt.After(time.Now().UTC()) {
		return ErrNotFound
	}
	delivery, ok := receipt.deliveries[connectionID]
	if !ok || delivery.nodeID != nodeID || !delivery.ackSupported {
		return ErrNotFound
	}
	if delivery.acknowledgedAt == nil {
		acknowledgedAt := time.Now().UTC()
		delivery.acknowledgedAt = &acknowledgedAt
		receipt.deliveries[connectionID] = delivery
	}
	return nil
}

func (m *MemStore) CompleteManagedRealtimeDirectMessageReceipt(ctx context.Context, endpointID, messageID string, summary ManagedRealtimeDirectMessageSummary) error {
	if validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, make([]byte, 32)) != nil || summary.Recipients < 0 || summary.Queued < 0 || summary.Unsupported < 0 || summary.QueueFull < 0 || summary.Failed < 0 {
		return ErrManagedRealtimeDirectMessageInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key := managedRealtimeDirectMessageKey{endpointID: endpointID, messageID: messageID}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt := m.managedRealtimeDirectReceipts[key]
	if receipt == nil || !receipt.expiresAt.After(time.Now().UTC()) {
		return ErrNotFound
	}
	receipt.summary = summary
	receipt.dispatchComplete = true
	return nil
}

func (m *MemStore) GetManagedRealtimeDirectMessageReceipt(ctx context.Context, endpointID, messageID string) (ManagedRealtimeDirectMessageReceipt, error) {
	if validateManagedRealtimeDirectMessageReceipt(endpointID, messageID, make([]byte, 32)) != nil {
		return ManagedRealtimeDirectMessageReceipt{}, ErrManagedRealtimeDirectMessageInvalid
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeDirectMessageReceipt{}, err
	}
	key := managedRealtimeDirectMessageKey{endpointID: endpointID, messageID: messageID}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt := m.managedRealtimeDirectReceipts[key]
	if receipt == nil || !receipt.expiresAt.After(time.Now().UTC()) {
		delete(m.managedRealtimeDirectReceipts, key)
		return ManagedRealtimeDirectMessageReceipt{}, ErrNotFound
	}
	return projectManagedRealtimeDirectMessageReceipt(key, receipt, time.Now().UTC()), nil
}

func projectManagedRealtimeDirectMessageReceipt(key managedRealtimeDirectMessageKey, receipt *managedRealtimeDirectMessageState, now time.Time) ManagedRealtimeDirectMessageReceipt {
	out := ManagedRealtimeDirectMessageReceipt{
		EndpointID: key.endpointID, MessageID: key.messageID, CreatedAt: receipt.createdAt,
		ExpiresAt: receipt.expiresAt, DispatchComplete: receipt.dispatchComplete,
		Summary: receipt.summary, Deliveries: make([]ManagedRealtimeDirectMessageDelivery, 0, len(receipt.deliveries)),
	}
	for _, delivery := range receipt.deliveries {
		out.Deliveries = append(out.Deliveries, managedRealtimeDirectDeliveryProjection(delivery, now))
	}
	sort.Slice(out.Deliveries, func(i, j int) bool { return out.Deliveries[i].ConnectionID < out.Deliveries[j].ConnectionID })
	return out
}
