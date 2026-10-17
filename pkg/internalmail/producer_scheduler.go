package internalmail

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const ProducerSchedulerPath = OperationsPath + "/producer-scheduler"
const producerThrottleCode = "producer_throttled"

var ErrProducerThrottled = errors.New("internal mail producer dispatch limit reached")

// DispatchPolicy is trusted enrollment, including the empty legacy producer.
// Zero preserves the existing FIFO scheduler and creates no reservations.
type DispatchPolicy struct {
	Enabled bool
	IDs     []string
	Limits  []int32
}

func (p DispatchPolicy) valid() bool {
	if !p.Enabled {
		return len(p.IDs) == 0 && len(p.Limits) == 0
	}
	if len(p.IDs) == 0 || len(p.IDs) > api.InternalMailMaxProducers+1 || len(p.IDs) != len(p.Limits) {
		return false
	}
	seen := map[string]bool{}
	for i, id := range p.IDs {
		if id != "" && !validProducerID(id) || seen[id] || p.Limits[i] < 1 || p.Limits[i] > api.InternalMailMaxProducerDispatch {
			return false
		}
		seen[id] = true
	}
	return seen[""]
}

func (p DispatchPolicy) limit(id string) (int32, bool) {
	for i, key := range p.IDs {
		if id == key {
			return p.Limits[i], true
		}
	}
	return 0, false
}

func (p Producer) dispatchLimit() int {
	if p.DispatchPerMinute == 0 {
		return api.InternalMailProducerDispatch
	}
	return p.DispatchPerMinute
}

func (s *Service) producerSchedulingEnabled() bool {
	return s.enabled() && s.ProducerSchedulingEnabled != nil && s.ProducerSchedulingEnabled()
}

func (s *Service) dispatchPolicy() (DispatchPolicy, error) {
	if !s.producerSchedulingEnabled() {
		return DispatchPolicy{}, nil
	}
	p := DispatchPolicy{Enabled: true, IDs: []string{""}, Limits: []int32{api.InternalMailProducerDispatch}}
	if s.producersEnabled() {
		ids := make([]string, 0, len(s.Producers))
		for id := range s.Producers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			producer, err := s.producer(id)
			if err != nil || producer.dispatchLimit() < 1 || producer.dispatchLimit() > api.InternalMailMaxProducerDispatch {
				return DispatchPolicy{}, ErrInvalid
			}
			p.IDs = append(p.IDs, id)
			p.Limits = append(p.Limits, int32(producer.dispatchLimit()))
		}
	}
	if !p.valid() {
		return DispatchPolicy{}, ErrInvalid
	}
	return p, nil
}

type producerSchedulerStore interface {
	claimScheduled(context.Context, domainAuthorization, []string, bool, PacingPolicy, bool, bool, DispatchPolicy) (Recipient, error)
	beginScheduled(context.Context, Recipient, string, domainAuthorization, []string, bool, PacingPolicy, bool, bool, DispatchPolicy) error
	producerDispatchStatus(context.Context, DispatchPolicy) ([]ProducerDispatchStatus, error)
}

type ProducerDispatchStatus struct {
	ProducerID        string     `json:"producer_id"`
	DispatchPerMinute int32      `json:"dispatch_per_minute"`
	InWindow          int64      `json:"in_window"`
	Remaining         int64      `json:"remaining"`
	RateLimited       bool       `json:"rate_limited"`
	RetryAt           *time.Time `json:"retry_at,omitempty"`
	DueRecipients     int64      `json:"due_recipients"`
	OldestDueSeconds  float64    `json:"oldest_due_seconds"`
}

func (s *Service) ProducerDispatchStatus(ctx context.Context) ([]ProducerDispatchStatus, error) {
	if !s.producerSchedulingEnabled() || !s.operationsEnabled() {
		return nil, ErrDisabled
	}
	p, err := s.dispatchPolicy()
	if err != nil {
		return nil, err
	}
	store, ok := s.Store.(producerSchedulerStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store.producerDispatchStatus(ctx, p)
}
