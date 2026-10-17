package internalmail

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

const ProducerOutboxPath = OperationsPath + "/producer-outbox"

type ProducerOutboxItem struct {
	ID              string     `json:"id"`
	AccountID       string     `json:"account_id"`
	ProducerID      string     `json:"producer_id"`
	Template        string     `json:"template"`
	TemplateVersion int32      `json:"template_version"`
	State           string     `json:"state"`
	Attempts        int32      `json:"attempts"`
	Revision        int64      `json:"revision"`
	Code            string     `json:"code,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	NextAttemptAt   time.Time  `json:"next_attempt_at"`
	LeaseUntil      *time.Time `json:"lease_until,omitempty"`
	RecoveryHeld    bool       `json:"recovery_held"`
}

type ProducerOutboxCount struct {
	State        string `json:"state"`
	Total        int64  `json:"total"`
	RecoveryHeld int64  `json:"recovery_held"`
}

func validProducerOutboxState(state string) bool {
	switch state {
	case "", "queued", "claimed", "admitted", "failed", "expired", "cancelled", "suppressed":
		return true
	}
	return false
}

func validProducerOutboxList(list OperatorList) bool {
	return list.Limit > 0 && list.Limit <= api.InternalMailMaxOperatorPageSize && (list.After == "" || canonicalOperatorID(list.After)) && validProducerOutboxState(list.Status)
}

type producerOutboxStore interface {
	producerOutboxCounts(context.Context) ([]ProducerOutboxCount, error)
	producerOutboxItems(context.Context, OperatorList) (OperatorPage[ProducerOutboxItem], error)
	producerOutboxItem(context.Context, string) (ProducerOutboxItem, error)
}

func (s *Service) producerOutboxEnabled() bool {
	return s.enabled() && s.ProducerOutboxEnabled != nil && s.ProducerOutboxEnabled()
}

func (s *Service) producerOutboxOperations() (producerOutboxStore, error) {
	if !s.producerOutboxEnabled() || !s.operationsEnabled() {
		return nil, ErrDisabled
	}
	store, ok := s.Store.(producerOutboxStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store, nil
}

func (s *Service) ProducerOutboxCounts(ctx context.Context) ([]ProducerOutboxCount, error) {
	store, err := s.producerOutboxOperations()
	if err != nil {
		return nil, err
	}
	return store.producerOutboxCounts(ctx)
}
func (s *Service) ProducerOutboxItems(ctx context.Context, list OperatorList) (OperatorPage[ProducerOutboxItem], error) {
	store, err := s.producerOutboxOperations()
	if err != nil {
		return OperatorPage[ProducerOutboxItem]{}, err
	}
	if !validProducerOutboxList(list) {
		return OperatorPage[ProducerOutboxItem]{}, ErrInvalid
	}
	return store.producerOutboxItems(ctx, list)
}
func (s *Service) ProducerOutboxItem(ctx context.Context, id string) (ProducerOutboxItem, error) {
	store, err := s.producerOutboxOperations()
	if err != nil {
		return ProducerOutboxItem{}, err
	}
	if !canonicalOperatorID(id) {
		return ProducerOutboxItem{}, ErrInvalid
	}
	return store.producerOutboxItem(ctx, id)
}

func (s *PGStore) ProducerOutboxReady(ctx context.Context) error {
	return outboxq.New().Ready(ctx, s.pool)
}

func (s *PGStore) producerOutboxCounts(ctx context.Context) ([]ProducerOutboxCount, error) {
	rows, err := outboxq.New().OperatorSummary(ctx, s.pool, s.namespace)
	counts := make([]ProducerOutboxCount, 0, len(rows))
	for _, r := range rows {
		counts = append(counts, ProducerOutboxCount{r.State, r.Total, r.RecoveryHeld})
	}
	return counts, err
}

func outboxItem(r outboxq.OperatorItemsRow) ProducerOutboxItem {
	return ProducerOutboxItem{ID: idString(r.ID), AccountID: idString(r.AccountID), ProducerID: r.ProducerID, Template: r.Template, TemplateVersion: r.TemplateVersion, State: r.State, Attempts: r.Attempts, Code: r.Code,
		Revision: r.Revision, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, ExpiresAt: r.ExpiresAt.Time, NextAttemptAt: r.NextAttemptAt.Time, LeaseUntil: timePointer(r.LeaseUntil), RecoveryHeld: r.RecoveryHeld}
}

func (s *PGStore) producerOutboxItems(ctx context.Context, list OperatorList) (OperatorPage[ProducerOutboxItem], error) {
	rows, err := outboxq.New().OperatorItems(ctx, s.pool, outboxq.OperatorItemsParams{Namespace: s.namespace, AfterID: pgID(operatorAfterID(list.After)), Status: list.Status, PageLimit: int32(list.Limit + 1)})
	items := make([]ProducerOutboxItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, outboxItem(r))
	}
	return operatorPage(items, list.Limit, func(r ProducerOutboxItem) string { return r.ID }), err
}

func (s *PGStore) producerOutboxItem(ctx context.Context, id string) (ProducerOutboxItem, error) {
	r, err := outboxq.New().OperatorItem(ctx, s.pool, outboxq.OperatorItemParams{Namespace: s.namespace, ID: pgID(id)})
	return outboxItem(outboxq.OperatorItemsRow(r)), storeError(err)
}
