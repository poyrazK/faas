package productemail

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail/sqlc"
)

type consentGate struct {
	ready chan struct{}
	users int
}

func (m *Memory) WithConsent(ctx context.Context, k Key, work func() error) error {
	m.mu.Lock()
	g := m.consent[k.ID()]
	if g == nil {
		g = &consentGate{ready: make(chan struct{}, 1)}
		g.ready <- struct{}{}
		m.consent[k.ID()] = g
	}
	g.users++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		g.users--
		if g.users == 0 {
			delete(m.consent, k.ID())
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.ready:
	}
	defer func() { g.ready <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return work()
}
func (p *Postgres) WithConsent(ctx context.Context, k Key, work func() error) error {
	// Try-lock waiters release their connection before waiting; they cannot fill
	// the pool behind a holder that still needs short repository transactions.
	for {
		tx, err := p.pool.Begin(ctx)
		if err != nil {
			return err
		}
		locked, err := sqlc.New().TryConsent(ctx, tx, "product-email-consent/v1/"+k.ID())
		if err == nil && locked {
			defer func() {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ProductControlEmailOwnerMaxDuration)
				defer cancel()
				_ = tx.Rollback(cleanup)
			}()
			return work()
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ProductControlEmailOwnerMaxDuration)
		_ = tx.Rollback(cleanup)
		cancel()
		if err != nil {
			return err
		}
		timer := time.NewTimer(api.ProductControlEmailConsentWait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
