package internalmail

import (
	"context"

	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) checkProducerAuthentication(ctx context.Context, auth producerAuthentication) error {
	return s.checkProducerCredential(ctx, s.pool, auth)
}

func (s *PGStore) checkProducerCredential(ctx context.Context, db q.DBTX, auth producerAuthentication) error {
	if !validProducerID(auth.ProducerID) || auth.CredentialID != "" && !validCredentialID(auth.CredentialID) {
		return ErrInvalid
	}
	if auth.Enabled != nil && !auth.Enabled() {
		return ErrProducerCredential
	}
	allowed, err := s.queries.RecoveryPermitted(ctx, db, s.namespace)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRecoveryHeld
	}
	r, err := s.queries.CheckProducerAccess(ctx, db, q.CheckProducerAccessParams{Namespace: s.namespace, ProducerID: auth.ProducerID, CredentialID: auth.CredentialID, PrimaryID: auth.PrimaryID})
	if err != nil {
		return err
	}
	if r.Suspended {
		return ErrProducerSuspended
	}
	if !r.CredentialAllowed {
		return ErrProducerCredential
	}
	return nil
}

func (s *PGStore) checkProducerAccess(ctx context.Context, db q.DBTX, producer string) error {
	if producer == "" {
		return nil
	}
	auth, ok := ctx.Value(producerAuthenticationKey{}).(producerAuthentication)
	if !ok {
		auth.ProducerID = producer
	} else if auth.ProducerID != producer {
		return ErrProducerCredential
	}
	return s.checkProducerCredential(ctx, db, auth)
}

func (s *PGStore) shareProducerAccess(ctx context.Context, db q.DBTX, producer string) error {
	if producer == "" {
		return nil
	}
	if err := s.queries.ShareProducerAccess(ctx, db, q.ShareProducerAccessParams{Namespace: s.namespace, ProducerID: producer}); err != nil {
		return err
	}
	return s.checkProducerAccess(ctx, db, producer)
}

// Holds and revocations are fenced by the existing shared lock. Only a
// credential's clock deadline or process gate can change during later waits.
func (s *PGStore) recheckProducerCredential(ctx context.Context, db q.DBTX, producer string) error {
	if _, ok := ctx.Value(producerAuthenticationKey{}).(producerAuthentication); !ok {
		return nil
	}
	return s.checkProducerAccess(ctx, db, producer)
}

func (s *PGStore) shareRecipientProducer(ctx context.Context, db q.DBTX, id string) error {
	producer, err := s.queries.RecipientProducer(ctx, db, q.RecipientProducerParams{Namespace: s.namespace, ID: pgID(id)})
	if err != nil {
		return storeError(err)
	}
	return s.shareProducerAccess(ctx, db, producer)
}
