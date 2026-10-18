package internalmail

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) producerAccessStatus(ctx context.Context, config producerCredentialConfig) (ProducerAccessStatus, error) {
	if !validProducerID(config.ProducerID) {
		return ProducerAccessStatus{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ProducerAccessStatus{}, err
	}
	defer rollbackOperator(ctx, tx)
	status, err := s.producerAccessStatusTx(ctx, tx, config, "")
	if err != nil {
		return ProducerAccessStatus{}, err
	}
	return status, tx.Commit(ctx)
}

func (s *PGStore) producerAccessStatusTx(ctx context.Context, db q.DBTX, config producerCredentialConfig, extra string) (ProducerAccessStatus, error) {
	row, err := s.queries.ProducerAccess(ctx, db, q.ProducerAccessParams{Namespace: s.namespace, ProducerID: config.ProducerID})
	if err != nil {
		return ProducerAccessStatus{}, err
	}
	if row.Revision == 0 && config.PrimaryID == "" {
		return ProducerAccessStatus{}, ErrNotFound
	}
	active := row.ActiveCredentialID
	if active == "" {
		active = config.PrimaryID
	}
	status := ProducerAccessStatus{ProducerID: config.ProducerID, Revision: row.Revision, Suspended: row.Suspended, ActiveCredentialID: active,
		PrimaryCredentialID: config.PrimaryID, ReplacementCredentialID: config.ReplacementID, UpdatedAt: timePointer(row.UpdatedAt), Credentials: []ProducerCredentialStatus{}}
	ids := map[string]bool{}
	for _, id := range []string{active, config.PrimaryID, config.ReplacementID, extra} {
		if id != "" {
			ids[id] = true
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		credential, err := s.producerCredentialStatus(ctx, db, config, id, active)
		if err != nil {
			return ProducerAccessStatus{}, err
		}
		status.Credentials = append(status.Credentials, credential)
	}
	return status, nil
}

func (s *PGStore) producerCredentialStatus(ctx context.Context, db q.DBTX, config producerCredentialConfig, id, active string) (ProducerCredentialStatus, error) {
	status := ProducerCredentialStatus{ID: id, Configured: id == config.PrimaryID || id == config.ReplacementID, Active: id == active}
	row, err := s.queries.ProducerCredential(ctx, db, q.ProducerCredentialParams{Namespace: s.namespace, ProducerID: config.ProducerID, CredentialID: id})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ProducerCredentialStatus{}, err
	}
	if err == nil {
		status.RetiresAt, status.RevokedAt = timePointer(row.RetiresAt), timePointer(row.RevokedAt)
	}
	if status.Configured {
		err := s.checkProducerCredential(ctx, db, producerAuthentication{ProducerID: config.ProducerID, CredentialID: id, PrimaryID: config.PrimaryID})
		if err != nil && !errors.Is(err, ErrProducerCredential) && !errors.Is(err, ErrProducerSuspended) {
			return ProducerCredentialStatus{}, err
		}
		status.Usable = err == nil && (id == config.PrimaryID || config.AllowReplacement)
	}
	return status, nil
}

func (s *PGStore) changeProducerAccess(ctx context.Context, action string, input ProducerLifecycleRequest, config producerCredentialConfig, enabled func() bool) (OperationRecord, error) {
	if input.Validate(action) != nil || config.ProducerID != input.ProducerID {
		return OperationRecord{}, ErrInvalid
	}
	return s.operatorMutation(ctx, input.OperatorAuditInput, "producer_"+action, input.ProducerID, input, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.queries.LockProducerAccess(ctx, tx, q.LockProducerAccessParams{Namespace: s.namespace, ProducerID: input.ProducerID}); err != nil {
			return nil, err
		}
		state, err := s.queries.ProducerAccess(ctx, tx, q.ProducerAccessParams{Namespace: s.namespace, ProducerID: input.ProducerID})
		if err != nil {
			return nil, err
		}
		if state.Revision != *input.ExpectedRevision {
			return nil, ErrProducerRevision
		}
		if err := s.queries.EnsureProducerAccess(ctx, tx, q.EnsureProducerAccessParams{Namespace: s.namespace, ProducerID: input.ProducerID}); err != nil {
			return nil, err
		}
		if err := s.mutateProducerAccess(ctx, tx, action, input, config, state); err != nil {
			return nil, err
		}
		return s.producerAccessStatusTx(ctx, tx, config, input.CredentialID)
	})
}

func (s *PGStore) mutateProducerAccess(ctx context.Context, tx pgx.Tx, action string, input ProducerLifecycleRequest, config producerCredentialConfig, state q.ProducerAccessRow) error {
	active, suspended := state.ActiveCredentialID, state.Suspended
	if active == "" && validCredentialID(config.PrimaryID) {
		active = config.PrimaryID
	}
	switch action {
	case "rotate":
		if err := s.rotateProducerCredentials(ctx, tx, input, config, active); err != nil {
			return err
		}
		active = input.CredentialID
	case "revoke":
		if err := s.revokeProducerCredential(ctx, tx, input, config, active); err != nil {
			return err
		}
	case "suspend":
		suspended = true
	case "resume":
		suspended = false
	}
	return s.queries.SetProducerAccess(ctx, tx, q.SetProducerAccessParams{Namespace: s.namespace, ProducerID: input.ProducerID, Suspended: suspended, ActiveCredentialID: active, RequestID: pgID(input.RequestID)})
}

func (s *PGStore) rotateProducerCredentials(ctx context.Context, tx pgx.Tx, input ProducerLifecycleRequest, config producerCredentialConfig, active string) error {
	if !validCredentialID(config.PrimaryID) || !validCredentialID(active) || config.PrimaryID == config.ReplacementID || config.ReplacementID != input.CredentialID || active == input.CredentialID {
		return ErrConflict
	}
	_, err := s.queries.ProducerCredential(ctx, tx, q.ProducerCredentialParams{Namespace: s.namespace, ProducerID: input.ProducerID, CredentialID: input.CredentialID})
	if err == nil {
		return ErrConflict // A retired or revoked credential can never be recycled.
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	for _, id := range []string{active, input.CredentialID} {
		if err := s.queries.EnsureProducerCredential(ctx, tx, q.EnsureProducerCredentialParams{Namespace: s.namespace, ProducerID: input.ProducerID, CredentialID: id}); err != nil {
			return err
		}
	}
	return s.queries.RetireProducerCredential(ctx, tx, q.RetireProducerCredentialParams{Namespace: s.namespace, ProducerID: input.ProducerID, CredentialID: active, OverlapSeconds: *input.OverlapSeconds})
}

func (s *PGStore) revokeProducerCredential(ctx context.Context, tx pgx.Tx, input ProducerLifecycleRequest, config producerCredentialConfig, active string) error {
	if input.CredentialID != config.PrimaryID && input.CredentialID != config.ReplacementID && input.CredentialID != active {
		if _, err := s.queries.ProducerCredential(ctx, tx, q.ProducerCredentialParams{Namespace: s.namespace, ProducerID: input.ProducerID, CredentialID: input.CredentialID}); err != nil {
			return storeError(err)
		}
	}
	if err := s.queries.EnsureProducerCredential(ctx, tx, q.EnsureProducerCredentialParams{Namespace: s.namespace, ProducerID: input.ProducerID, CredentialID: input.CredentialID}); err != nil {
		return err
	}
	return s.queries.RevokeProducerCredential(ctx, tx, q.RevokeProducerCredentialParams{Namespace: s.namespace, ProducerID: input.ProducerID, CredentialID: input.CredentialID})
}
