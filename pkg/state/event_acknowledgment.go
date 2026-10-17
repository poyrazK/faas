package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const EventAcknowledgmentTokenHeader = "x-gregale-event-acknowledgment-token"
const eventAcknowledgmentSecondsHeader = "x-gregale-event-acknowledgment-lease-seconds"

var ErrEventAcknowledgmentStale = errors.New("event acknowledgment expired, replaced or incompatible")

type EventAcknowledgmentStore interface {
	EventAcknowledgmentPolicy(context.Context, string, string, string, *int) (api.EventAcknowledgmentPolicy, error)
	OfferEventAcknowledgment(context.Context, Invocation, int, time.Duration) (string, error)
	AcceptEventAcknowledgment(context.Context, Invocation, string) (api.EventAcknowledgmentStatus, error)
	EventAcknowledgment(context.Context, string, string, string, string, string, json.RawMessage) (api.EventAcknowledgmentStatus, error)
	ExpireEventAcknowledgments(context.Context, int) (int, error)
}

type eventAcknowledgment struct {
	rejectionStatus  json.RawMessage
	completionDigest []byte
	claim            InvocationClaim
	digest           [32]byte
	phase            string
	seconds          int
	deadline         time.Time
	maxAttempts      int
	retryDelay       time.Duration
}

// The policy is acceptance-time metadata, not a live subscription lookup.
func EventAcknowledgmentSeconds(inv Invocation) (int, error) {
	if len(inv.Headers) == 0 {
		return 0, nil
	}
	var headers map[string]string
	if json.Unmarshal(inv.Headers, &headers) != nil {
		return 0, ErrInvalidArgument
	}
	raw := headers[eventAcknowledgmentSecondsHeader]
	if raw == "" {
		return 0, nil
	}
	seconds, err := strconv.Atoi(raw)
	acceptance, e := strconv.ParseInt(headers["x-gregale-event-acceptance-id"], 10, 64)
	_, subErr := operationUUID(headers["x-gregale-event-subscription-id"])
	if err != nil || seconds <= 0 || seconds > api.EventAcknowledgmentLeaseMaxSeconds || strconv.Itoa(seconds) != raw || e != nil || acceptance <= 0 || subErr != nil || (inv.Source != InvocationAsyncInvoke && inv.Source != InvocationReplay) || inv.OperationID != "" || inv.OnSuccessDestinationID != "" || inv.OnFailureDestinationID != "" {
		return 0, ErrInvalidArgument
	}
	return seconds, nil
}

// Only the dispatch copy carries a raw token. It is never persisted in headers.
func WithEventAcknowledgmentToken(inv Invocation, token string) (Invocation, error) {
	headers := map[string]string{}
	if err := json.Unmarshal(inv.Headers, &headers); err != nil {
		return inv, err
	}
	if headers == nil {
		return inv, ErrInvalidArgument
	}
	for key := range headers {
		if strings.EqualFold(key, EventAcknowledgmentTokenHeader) {
			delete(headers, key)
		}
	}
	headers[EventAcknowledgmentTokenHeader] = token
	encoded, err := json.Marshal(headers)
	inv.Headers = encoded
	return inv, err
}
func acknowledgmentToken() (string, [32]byte, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", [32]byte{}, err
	}
	raw := hex.EncodeToString(token[:])
	return raw, sha256.Sum256([]byte(raw)), nil
}
func acknowledgmentDigest(token string) ([32]byte, error) {
	raw, err := hex.DecodeString(token)
	if err != nil || len(raw) != 32 || token != strings.ToLower(token) {
		return [32]byte{}, ErrInvalidArgument
	}
	return sha256.Sum256([]byte(token)), nil
}
func acknowledgmentStatus(inv Invocation, ack eventAcknowledgment, now time.Time) api.EventAcknowledgmentStatus {
	out := api.EventAcknowledgmentStatus{InvocationID: inv.ID, Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration, State: string(inv.State), ProcessingState: string(inv.State), LeaseExpiresAt: cloneEventReceiptTime(inv.LeaseExpiresAt)}
	if ack.claim.Attempt > 0 && inv.Attempts == ack.claim.Attempt && inv.ReplayGeneration == ack.claim.ReplayGeneration {
		deadline := ack.deadline
		out.DeadlineAt = &deadline
	}
	if ack.phase == "rejected" && inv.Attempts == ack.claim.Attempt && inv.ReplayGeneration == ack.claim.ReplayGeneration {
		var saved api.EventAcknowledgmentStatus
		if json.Unmarshal(ack.rejectionStatus, &saved) == nil {
			out.Disposition, out.FailureReason = saved.Disposition, saved.FailureReason
			if inv.State == InvocationPending {
				out.NextAttemptAt = cloneEventReceiptTime(saved.NextAttemptAt)
			}
		}
	}
	switch inv.State {
	case InvocationPending:
		out.ProcessingState = "waiting"
	case InvocationDispatching:
		out.ProcessingState = "delivering"
		if ack.phase == "awaiting" {
			out.ProcessingState = "processing"
		}
		if inv.LeaseExpiresAt == nil || !inv.LeaseExpiresAt.After(now) {
			out.ProcessingState = "expired"
		}
	}
	return out
}
func validAcknowledgmentResult(result json.RawMessage) bool {
	return len(result) <= api.EventAcknowledgmentResultMaxBytes && utf8.Valid(result) && (len(result) == 0 || json.Valid(result))
}

func (m *MemStore) EventAcknowledgmentPolicy(ctx context.Context, account, app, sub string, seconds *int) (api.EventAcknowledgmentPolicy, error) {
	if err := ctx.Err(); err != nil {
		return api.EventAcknowledgmentPolicy{}, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventAcknowledgmentPolicy{}, err
	}
	if seconds != nil && (*seconds < 0 || *seconds > api.EventAcknowledgmentLeaseMaxSeconds) {
		return api.EventAcknowledgmentPolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	target := m.eventRoutingRetryTargetLocked(account, app, sub)
	if target == nil {
		return api.EventAcknowledgmentPolicy{}, ErrNotFound
	}
	if seconds != nil {
		for key, row := range m.eventSubscriptions {
			if sameMemUUID(row.ID, sub) {
				row.AcknowledgmentLeaseSeconds = *seconds
				row.UpdatedAt = time.Now()
				m.eventSubscriptions[key] = row
			}
		}
		target.AcknowledgmentLeaseSeconds = *seconds
	}
	return api.EventAcknowledgmentPolicy{SubscriptionID: canonicalMemUUID(sub), LeaseSeconds: target.AcknowledgmentLeaseSeconds}, nil
}
func (m *MemStore) OfferEventAcknowledgment(ctx context.Context, inv Invocation, budget int, delay time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	seconds, err := EventAcknowledgmentSeconds(inv)
	if err != nil || seconds == 0 || budget <= 0 || budget > api.DurableRetryMaxAttempts || delay.Milliseconds() <= 0 {
		return "", ErrInvalidArgument
	}
	token, digest, err := acknowledgmentToken()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.invocations[inv.ID]
	claim := InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}
	if !ok || !validInvocationAttemptClaim(current, claim) {
		return "", ErrEventAcknowledgmentStale
	}
	actual, err := EventAcknowledgmentSeconds(current)
	if err != nil || actual != seconds {
		return "", ErrInvalidArgument
	}
	if m.eventAcknowledgments == nil {
		m.eventAcknowledgments = map[string]eventAcknowledgment{}
	}
	if old, ok := m.eventAcknowledgments[inv.ID]; ok && old.claim == claim {
		return "", ErrEventAcknowledgmentStale
	}
	m.eventAcknowledgments[inv.ID] = eventAcknowledgment{claim: claim, digest: digest, phase: "offered", seconds: seconds, deadline: time.Now().Add(api.EventAcknowledgmentLifetime), maxAttempts: budget, retryDelay: delay}
	return token, nil
}
func (m *MemStore) AcceptEventAcknowledgment(ctx context.Context, inv Invocation, token string) (api.EventAcknowledgmentStatus, error) {
	return m.EventAcknowledgment(ctx, inv.AccountID, inv.AppID, inv.ID, token, "accept", nil)
}
func (m *MemStore) EventAcknowledgment(ctx context.Context, account, app, id, token, action string, result json.RawMessage) (api.EventAcknowledgmentStatus, error) {
	if err := ctx.Err(); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if err := validSubscriptionControlIDs(account, app, id); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if err := validateAcknowledgmentFailure(action, result); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if !validAcknowledgmentResult(result) {
		return api.EventAcknowledgmentStatus{}, ErrInvalidArgument
	}
	id = canonicalMemUUID(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invocations[id]
	target, exists := m.eventSubscriptionAppLocked(app)
	ack, found := m.eventAcknowledgments[id]
	if !ok || !exists || target.Status == AppDeleted || !sameMemUUID(target.AccountID, account) || !sameMemUUID(inv.AccountID, account) || !sameMemUUID(inv.AppID, app) {
		return api.EventAcknowledgmentStatus{}, ErrNotFound
	}
	now := time.Now()
	if !found {
		seconds, err := EventAcknowledgmentSeconds(inv)
		if action == "get" && err == nil && seconds > 0 {
			return acknowledgmentStatus(inv, eventAcknowledgment{}, now), nil
		}
		return api.EventAcknowledgmentStatus{}, ErrNotFound
	}
	if action == "get" {
		return acknowledgmentStatus(inv, ack, now), nil
	}
	digest, err := acknowledgmentDigest(token)
	if err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if subtle.ConstantTimeCompare(digest[:], ack.digest[:]) != 1 || ack.claim.Attempt != inv.Attempts || ack.claim.ReplayGeneration != inv.ReplayGeneration {
		return api.EventAcknowledgmentStatus{}, ErrEventAcknowledgmentStale
	}
	if ack.phase == "rejected" {
		if action == "renew" {
			return m.rejectAcknowledgmentRenewalLocked(inv, ack, action, now)
		}
		return replayAcknowledgmentFailure(inv, ack, action, result)
	}
	if ack.phase == "completed" && inv.State == InvocationCompleted {
		if action == "accept" || (action == "complete" && bytes.Equal(acknowledgmentCompletionDigest(result), ack.completionDigest)) {
			return acknowledgmentStatus(inv, ack, now), nil
		}
		return m.rejectAcknowledgmentRenewalLocked(inv, ack, action, now)
	}
	if !validInvocationAttemptClaim(inv, ack.claim) || !ack.deadline.After(now) {
		return m.rejectAcknowledgmentRenewalLocked(inv, ack, action, now)
	}
	switch action {
	case "retry", "dead-letter":
		return m.rejectEventAcknowledgmentLocked(inv, ack, action, result, now)
	case "accept", "renew":
		if action == "renew" && ack.phase != "awaiting" {
			return m.rejectAcknowledgmentRenewalLocked(inv, ack, action, now)
		}
		if action == "accept" && ack.phase == "awaiting" {
			return acknowledgmentStatus(inv, ack, now), nil
		}
		until := now.Add(time.Duration(ack.seconds) * time.Second)
		if until.After(ack.deadline) {
			until = ack.deadline
		}
		inv.LeaseExpiresAt = &until
		ack.phase = "awaiting"
		m.invocations[id] = inv
		m.eventAcknowledgments[id] = ack
	case "complete":
		if err := m.completeInvocationAndQueueServingAckLocked(id, 0, &ack.claim, normalizedAcknowledgmentResult(result), nil); err != nil {
			return api.EventAcknowledgmentStatus{}, err
		}
		ack.phase = "completed"
		ack.completionDigest = acknowledgmentCompletionDigest(result)
		m.eventAcknowledgments[id] = ack
		inv = m.invocations[id]
	default:
		return api.EventAcknowledgmentStatus{}, ErrInvalidArgument
	}
	m.recordAcknowledgmentOutcomeLocked(inv, ack, acknowledgmentOutcomeKind(action), now)
	return acknowledgmentStatus(inv, ack, now), nil
}
func normalizedAcknowledgmentResult(result json.RawMessage) json.RawMessage {
	if len(result) == 0 {
		return json.RawMessage("null")
	}
	return result
}
func (m *MemStore) ExpireEventAcknowledgments(ctx context.Context, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if limit <= 0 || limit > api.EventAcknowledgmentSweepBatch {
		limit = api.EventAcknowledgmentSweepBatch
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	count := 0
	for key, row := range m.eventAcknowledgmentOutcomes {
		if !row.RetainUntil.After(now) {
			delete(m.eventAcknowledgmentOutcomes, key)
			count++
			if count >= limit {
				break
			}
		}
	}
	m.pruneAcknowledgmentMetricsLocked(now, limit)
	count = 0
	for id, ack := range m.eventAcknowledgments {
		inv, ok := m.invocations[id]
		if !ok || ack.phase != "awaiting" || inv.Attempts != ack.claim.Attempt || inv.ReplayGeneration != ack.claim.ReplayGeneration || inv.State != InvocationDispatching || inv.LeaseExpiresAt == nil || inv.LeaseExpiresAt.After(now) {
			continue
		}
		m.recordAcknowledgmentOutcomeLocked(inv, ack, "expired", now)
		reserved := inv.QuotaReserved
		inv.QuotaReserved = false
		inv.LeaseExpiresAt = nil
		inv.InstanceID = ""
		inv.LastError = "event acknowledgment lease expired"
		inv.State = InvocationPending
		inv.Outcome = nil
		inv.DueAt = now.Add(ack.retryDelay)
		if inv.Attempts >= ack.maxAttempts {
			inv.State = InvocationDeadLetter
			outcome := OutcomeDeadLetter
			inv.Outcome = &outcome
			inv.CompletedAt = &now
		}
		m.setInvocationLocked(id, inv)
		if reserved {
			m.decrementAccountAsyncInflightLocked(inv.AccountID)
		}
		count++
		if count >= limit {
			break
		}
	}
	return count, nil
}

func acknowledgmentCompletionDigest(result json.RawMessage) []byte {
	decoder := json.NewDecoder(bytes.NewReader(normalizedAcknowledgmentResult(result)))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	digest := sha256.Sum256(canonical)
	return digest[:]
}
