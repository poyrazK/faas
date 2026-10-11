package state

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// orderedEventRecipientSharesLane compares immutable recipient snapshots. A
// lane is the app, policy, and canonical resolved key used by async work.
func orderedEventRecipientSharesLane(left, right PublishedEventRecipient, leftPayload, rightPayload []byte) bool {
	if left.AppID != right.AppID || left.Work == nil || right.Work == nil ||
		left.Work.PolicyName == "" || left.Work.PolicyName != right.Work.PolicyName ||
		(left.Work.Action != EventWorkInvoke && left.Work.Action != EventWorkCancelPending) ||
		(right.Work.Action != EventWorkInvoke && right.Work.Action != EventWorkCancelPending) ||
		(!left.Work.Ordered && left.Work.RoutingOrder == 0 &&
			!right.Work.Ordered && right.Work.RoutingOrder == 0) {
		return false
	}
	leftSelector, err := workpolicy.ParseSelector(left.Work.KeySelector)
	if err != nil {
		return false
	}
	rightSelector, err := workpolicy.ParseSelector(right.Work.KeySelector)
	if err != nil {
		return false
	}
	leftKey, err := leftSelector.Resolve(json.RawMessage(leftPayload))
	if err != nil {
		return false
	}
	rightKey, err := rightSelector.Resolve(json.RawMessage(rightPayload))
	return err == nil && leftKey == rightKey
}

func (m *MemStore) memEventWorkLaneOrderedLocked(appID, policyName string) bool {
	for _, binding := range m.eventWorkBindings {
		if binding.Ordered && binding.PolicyName == policyName && sameMemUUID(binding.AppID, appID) {
			return true
		}
	}
	return false
}

// orderedEventRecipientBlockedLocked is called while MemStore.mu is held. A
// younger recipient waits until earlier same-lane routing reaches a terminal
// state. Same-receipt positions are included only by per-recipient workers;
// whole-receipt routing already walks its immutable snapshot serially.
func (m *MemStore) orderedEventRecipientBlockedLocked(current *PublishedEventWork, recipientIndex int, includeSameReceipt bool) bool {
	returnWork, _ := m.orderedEventRecipientBlockerLocked(current, recipientIndex, includeSameReceipt)
	return returnWork != nil
}

func (m *MemStore) orderedEventRecipientBlockerLocked(current *PublishedEventWork, recipientIndex int, includeSameReceipt bool) (*PublishedEventWork, int) {
	if current == nil || recipientIndex < 0 || recipientIndex >= len(current.RecipientSnapshot) {
		return nil, -1
	}
	target := current.RecipientSnapshot[recipientIndex]
	if target.Work == nil {
		return nil, -1
	}
	var oldest *PublishedEventWork
	oldestIndex := -1
	for _, prior := range m.eventFanout {
		if prior.ID > current.ID {
			continue
		}
		for priorIndex, recipient := range prior.RecipientSnapshot {
			if prior.ID == current.ID && (!includeSameReceipt || priorIndex >= recipientIndex) {
				continue
			}
			if !orderedEventRecipientSharesLane(recipient, target, prior.Payload, current.Payload) {
				continue
			}
			state := eventRecipientRoutingStateLocked(prior, recipient.ID)
			if state != PublishedEventRecipientEnqueued && state != PublishedEventRecipientFiltered && state != PublishedEventRecipientFailed {
				if oldest == nil || prior.ID < oldest.ID || prior.ID == oldest.ID && priorIndex < oldestIndex {
					oldest, oldestIndex = prior, priorIndex
				}
			}
		}
	}
	return oldest, oldestIndex
}

func eventRecipientRoutingStateLocked(receipt *PublishedEventWork, subscriptionID string) string {
	if routed := receipt.routingRecipients[subscriptionID]; routed != nil && routed.State != "" {
		return routed.State
	}
	if progress := receipt.RecipientProgress[subscriptionID]; progress.State != "" {
		return progress.State
	}
	return PublishedEventRecipientPending
}
