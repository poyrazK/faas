package internalmail

import (
	"context"
	"strings"
	"time"

	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) reserveDomainPacing(ctx context.Context, db q.DBTX, address string, policy PacingPolicy) error {
	domain, err := s.lockDomainPacing(ctx, db, address)
	if err != nil {
		return err
	}
	return s.reserveLockedDomainPacing(ctx, db, domain, policy)
}

func (s *PGStore) lockDomainPacing(ctx context.Context, db q.DBTX, address string) (string, error) {
	_, domain, ok := strings.Cut(address, "@")
	if !ok || !validDomain(domain) || domain != strings.ToLower(domain) {
		return "", ErrInvalid
	}
	if err := s.queries.EnsureDomainPacing(ctx, db, q.EnsureDomainPacingParams{Namespace: s.namespace, RecipientDomain: domain}); err != nil {
		return "", err
	}
	if err := s.queries.LockDomainPacing(ctx, db, q.LockDomainPacingParams{Namespace: s.namespace, RecipientDomain: domain}); err != nil {
		return "", err
	}
	return domain, nil
}

func (s *PGStore) reserveLockedDomainPacing(ctx context.Context, db q.DBTX, domain string, policy PacingPolicy) error {
	n, err := s.queries.ReserveDomainPacing(ctx, db, q.ReserveDomainPacingParams{Namespace: s.namespace, RecipientDomain: domain, IntervalSeconds: int32(policy.Interval / time.Second)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrDomainPaced
	}
	return nil
}
