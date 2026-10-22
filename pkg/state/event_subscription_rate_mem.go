package state

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type memSubscriptionRateCommand struct {
	Account, App, Sub string
	Request           []byte
	Receipt           api.EventSubscriptionRateCommandReceipt
}

func subscriptionRateMem(c *memEventSubscriptionControl, now time.Time) (api.EventSubscriptionRateLimit, int64, time.Time) {
	if c == nil || c.RateIntervalMS == 0 {
		return subscriptionRateStatus(false, 0, 1000, 1, 1000, now, now)
	}
	return subscriptionRateStatus(c.RateEnabled, c.RateRevision, c.RateIntervalMS, c.RateBurst, c.RateCreditMS, c.RateRefilledAt, now)
}
func (m *MemStore) SetEventSubscriptionRateLimit(ctx context.Context, account, app, sub string, req api.EventSubscriptionRateRequest) (api.EventSubscriptionRateCommandReceipt, error) {
	var zero api.EventSubscriptionRateCommandReceipt
	req, raw, err := normalizeSubscriptionRate(req)
	if err != nil {
		return zero, err
	}
	if validSubscriptionControlIDs(account, app, sub) != nil {
		return zero, ErrInvalidArgument
	}
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return zero, ErrNotFound
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	key := canonicalMemUUID(sub) + "/" + req.CommandID
	if saved, ok := m.eventSubscriptionRateCommands[key]; ok && saved.Receipt.RetainUntil.After(now) {
		if !bytes.Equal(saved.Request, raw) {
			return zero, ErrEventSubscriptionRateCommandConflict
		}
		return cloneSubscriptionRateReceipt(saved.Receipt), nil
	}
	count := 0
	for _, saved := range m.eventSubscriptionRateCommands {
		if sameMemUUID(saved.Sub, sub) && saved.Receipt.RetainUntil.After(now) {
			count++
		}
	}
	if count >= api.EventSubscriptionRateCommandMaxReceipts {
		return zero, ErrEventSubscriptionRateCommandLimit
	}
	c := m.eventSubscriptionControls[canonicalMemUUID(sub)]
	previous, oldCredit, _ := subscriptionRateMem(c, now)
	if c != nil && c.RateEnabled && c.RateRefilledAt.After(now) {
		return zero, ErrConflict
	}
	if m.eventSubscriptionControls == nil {
		m.eventSubscriptionControls = map[string]*memEventSubscriptionControl{}
	}
	if c == nil {
		c = &memEventSubscriptionControl{AccountID: canonicalMemUUID(account), AppID: canonicalMemUUID(app), RateIntervalMS: 1000, RateBurst: 1, RateCreditMS: 1000, RateRefilledAt: now, UpdatedAt: now}
		m.eventSubscriptionControls[canonicalMemUUID(sub)] = c
	}
	applied := previous.Revision == *req.ExpectedRevision
	if applied {
		credit := req.IntervalMillis * req.Burst
		if previous.Enabled && *req.Enabled {
			credit = min(credit, oldCredit*req.IntervalMillis/previous.IntervalMillis)
		}
		c.RateEnabled = *req.Enabled
		c.RateIntervalMS = req.IntervalMillis
		c.RateBurst = req.Burst
		c.RateCreditMS = credit
		c.RateRefilledAt = now
		c.RateRevision++
		c.UpdatedAt = now
	}
	c.RateSequence++
	status, _, _ := subscriptionRateMem(c, now)
	receipt := rateCommandReceipt(req, app, sub, c.RateSequence, status, applied, now)
	if m.eventSubscriptionRateCommands == nil {
		m.eventSubscriptionRateCommands = map[string]memSubscriptionRateCommand{}
	}
	for key, saved := range m.eventSubscriptionRateCommands {
		if sameMemUUID(saved.Sub, sub) && !saved.Receipt.RetainUntil.After(now) {
			delete(m.eventSubscriptionRateCommands, key)
		}
	}
	m.eventSubscriptionRateCommands[key] = memSubscriptionRateCommand{canonicalMemUUID(account), canonicalMemUUID(app), canonicalMemUUID(sub), raw, receipt}
	return cloneSubscriptionRateReceipt(receipt), nil
}
func (m *MemStore) GetEventSubscriptionRateCommand(ctx context.Context, account, app, sub, command string) (api.EventSubscriptionRateCommandReceipt, error) {
	var zero api.EventSubscriptionRateCommandReceipt
	if validSubscriptionControlIDs(account, app, sub) != nil || !validRateCommandID(command) {
		return zero, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	saved, ok := m.eventSubscriptionRateCommands[canonicalMemUUID(sub)+"/"+strings.ToLower(command)]
	if !ok || !sameMemUUID(saved.Account, account) || !sameMemUUID(saved.App, app) || !saved.Receipt.RetainUntil.After(time.Now()) || !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return zero, ErrNotFound
	}
	return cloneSubscriptionRateReceipt(saved.Receipt), nil
}
func (m *MemStore) ListEventSubscriptionRateCommands(ctx context.Context, account, app, sub string, after int64) (api.EventSubscriptionRateCommands, error) {
	out := api.EventSubscriptionRateCommands{Receipts: []api.EventSubscriptionRateCommandReceipt{}}
	if validSubscriptionControlIDs(account, app, sub) != nil || after < 0 {
		return out, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	now := time.Now()
	for _, saved := range m.eventSubscriptionRateCommands {
		if sameMemUUID(saved.Account, account) && sameMemUUID(saved.App, app) && sameMemUUID(saved.Sub, sub) && saved.Receipt.Sequence > after && saved.Receipt.RetainUntil.After(now) {
			out.Receipts = append(out.Receipts, cloneSubscriptionRateReceipt(saved.Receipt))
		}
	}
	sort.Slice(out.Receipts, func(i, j int) bool { return out.Receipts[i].Sequence < out.Receipts[j].Sequence })
	if len(out.Receipts) > api.EventSubscriptionRateCommandPageSize {
		out.Receipts = out.Receipts[:api.EventSubscriptionRateCommandPageSize]
		out.NextAfter = out.Receipts[api.EventSubscriptionRateCommandPageSize-1].Sequence
	}
	return out, nil
}

func cloneSubscriptionRateReceipt(r api.EventSubscriptionRateCommandReceipt) api.EventSubscriptionRateCommandReceipt {
	if r.Result.NextAvailableAt != nil {
		t := *r.Result.NextAvailableAt
		r.Result.NextAvailableAt = &t
	}
	return r
}
