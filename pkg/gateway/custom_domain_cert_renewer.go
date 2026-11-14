// adr: 520
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// CustomDomainCertRenewer repairs missed notifications, failed issuance and
// expiring certificates in legacy DNS-01 mode. It is independent of surfaces.
type CustomDomainCertRenewer struct {
	store   state.CustomDomainCertificateRetryReader
	issuer  WildcardCertIssuer
	log     *slog.Logger
	clock   func() time.Time
	enabled func() bool
}

func NewCustomDomainCertRenewer(store state.CustomDomainCertificateRetryReader, issuer WildcardCertIssuer, log *slog.Logger) *CustomDomainCertRenewer {
	if log == nil {
		log = slog.Default()
	}
	return &CustomDomainCertRenewer{store: store, issuer: issuer, log: log, clock: time.Now,
		enabled: func() bool { return !api.CustomDomainTLSOnDemand() }}
}

func (r *CustomDomainCertRenewer) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(api.CertRenewTickSeconds) * time.Second)
	defer ticker.Stop()
	for {
		if err := r.tickOnce(ctx); err != nil && ctx.Err() == nil {
			r.log.Warn("gateway: custom-domain certificate retry pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *CustomDomainCertRenewer) tickOnce(ctx context.Context) error {
	if r.enabled != nil && !r.enabled() {
		return nil
	}
	if r.store == nil || r.issuer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, api.CustomDomainCertRetryPassTimeout)
	defer cancel()
	now := r.clock()
	claims, err := r.store.ListCustomDomainCertificateRetries(ctx, now,
		now.Add(time.Duration(api.CertRenewBeforeNotAfterDays)*24*time.Hour), api.CustomDomainCertRetryBatchLimit)
	if err != nil {
		return fmt.Errorf("list custom-domain certificate retries: %w", err)
	}
	jobs := make(chan state.CustomDomain)
	var workers sync.WaitGroup
	for range min(len(claims), api.CustomDomainCertRetryWorkers) {
		workers.Go(func() {
			for claim := range jobs {
				if ctx.Err() != nil {
					return
				}
				jobCtx, stop := context.WithTimeout(state.WithCustomDomainProbe(ctx, claim), api.CustomDomainCertRetryTimeout)
				if err := r.issuer.RequestCertForWildcardDomain(jobCtx, claim.Domain); err != nil && ctx.Err() == nil {
					r.log.Warn("gateway: retry custom-domain certificate", "domain", claim.Domain, "err", err)
				}
				stop()
			}
		})
	}
enqueue:
	for _, claim := range claims {
		select {
		case jobs <- claim:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)
	workers.Wait()
	return ctx.Err()
}
