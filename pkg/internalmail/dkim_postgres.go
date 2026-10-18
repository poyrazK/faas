package internalmail

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func keyPublicRow(r q.OperatorDKIMKeysRow, name string) DKIMKey {
	k := DKIMKey{ID: idString(r.ID), DomainID: idString(r.DomainID), Selector: r.Selector, State: r.State, PublicKey: r.PublicKey, CheckResult: r.CheckResult, CreatedAt: r.CreatedAt.Time, CheckedAt: timePointer(r.CheckedAt), FirstVerifiedAt: timePointer(r.FirstVerifiedAt), ActivatedAt: timePointer(r.ActivatedAt), DeactivatedAt: timePointer(r.DeactivatedAt), Record: DNSRecord{Name: r.Selector + "._domainkey." + name, Type: "TXT", Value: "v=DKIM1; k=rsa; p=" + r.PublicKey}}
	if k.FirstVerifiedAt != nil {
		after := k.FirstVerifiedAt.Add(api.InternalMailDKIMPropagationWait)
		k.ActivateAfter = &after
	}
	if k.DeactivatedAt != nil {
		after := k.DeactivatedAt.Add(api.InternalMailDKIMOverlap)
		k.RetireAfter = &after
	}
	return k
}
func keyRow(r q.InternalMailDkimKey, name string) DKIMKey {
	return keyPublicRow(q.OperatorDKIMKeysRow{ID: r.ID, DomainID: r.DomainID, Selector: r.Selector, PublicKey: r.PublicKey, Legacy: r.Legacy, State: r.State, CheckResult: r.CheckResult, CheckedAt: r.CheckedAt, FirstVerifiedAt: r.FirstVerifiedAt, CreatedAt: r.CreatedAt, ActivatedAt: r.ActivatedAt, DeactivatedAt: r.DeactivatedAt}, name)
}

func (s *PGStore) operatorDKIMKeys(ctx context.Context, id string, o OperatorList) (OperatorPage[DKIMKey], error) {
	if err := o.validate(true); err != nil {
		return OperatorPage[DKIMKey]{}, err
	}
	d, err := s.operatorDomain(ctx, id)
	if err != nil {
		return OperatorPage[DKIMKey]{}, err
	}
	rows, err := s.queries.OperatorDKIMKeys(ctx, s.pool, q.OperatorDKIMKeysParams{Namespace: s.namespace, DomainID: pgID(id), AfterID: pgID(operatorAfterID(o.After)), PageLimit: int32(o.Limit + 1)})
	items := make([]DKIMKey, 0, len(rows))
	for _, r := range rows {
		items = append(items, keyPublicRow(r, d.Name))
	}
	return operatorPage(items, o.Limit, func(k DKIMKey) string { return k.ID }), err
}

func (s *PGStore) stageDKIM(ctx context.Context, id string, input OperatorAuditInput, makeKey func() (DKIMKey, []byte, error), enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input, "dkim_stage", id, input, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.shareEncryption(ctx, tx); err != nil {
			return nil, err
		}
		d, err := s.queries.LockMailDomain(ctx, tx, q.LockMailDomainParams{Namespace: s.namespace, ID: pgID(id)})
		if err != nil {
			return nil, storeError(err)
		}
		if d.Status != "verified" {
			return nil, ErrUnverified
		}
		count, err := s.queries.LiveDKIMKeys(ctx, tx, q.LiveDKIMKeysParams{Namespace: s.namespace, DomainID: d.ID})
		if err != nil {
			return nil, err
		}
		if count >= api.InternalMailMaxLiveDKIMKeys {
			return nil, ErrQuota
		}
		key, sealed, err := makeKey()
		if err != nil {
			return nil, err
		}
		if err := s.checkEncryptionWrite(ctx, tx, sealed, nil, true); err != nil {
			return nil, err
		}
		r, err := s.queries.CreateDKIMKey(ctx, tx, q.CreateDKIMKeyParams{ID: pgID(key.ID), Namespace: s.namespace, DomainID: d.ID, Selector: key.Selector, PublicKey: key.PublicKey, SealedKey: sealed})
		return keyRow(r, d.Name), storeError(err)
	})
}

type keyAuditRequest struct {
	Audit OperatorAuditInput
	KeyID string
}

func (s *PGStore) verifyDKIM(ctx context.Context, id, keyID string, input OperatorAuditInput, observe func(context.Context, DKIMKey) (string, error), enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input, "dkim_verify", id, keyAuditRequest{input, keyID}, enabled, func(tx pgx.Tx) (any, error) {
		d, err := s.queries.LockMailDomain(ctx, tx, q.LockMailDomainParams{Namespace: s.namespace, ID: pgID(id)})
		if err != nil {
			return nil, storeError(err)
		}
		key, err := s.queries.DKIMKey(ctx, tx, q.DKIMKeyParams{Namespace: s.namespace, DomainID: d.ID, ID: pgID(keyID)})
		if err != nil {
			return nil, storeError(err)
		}
		if key.State != "staged" || d.Status != "verified" {
			return nil, ErrConflict
		}
		outcome, err := observe(ctx, keyRow(key, d.Name))
		if err != nil {
			return nil, err
		}
		r, err := s.queries.RecordDKIMVerification(ctx, tx, q.RecordDKIMVerificationParams{Namespace: s.namespace, DomainID: d.ID, KeyID: key.ID, Outcome: outcome})
		return keyRow(r, d.Name), storeError(err)
	})
}

func (s *PGStore) activateDKIM(ctx context.Context, id, keyID string, input OperatorAuditInput, observe func(context.Context, Domain, []DKIMKey) (string, error), ip string, enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input, "dkim_activate", id, keyAuditRequest{input, keyID}, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.shareEncryption(ctx, tx); err != nil {
			return nil, err
		}
		row, err := s.queries.LockMailDomain(ctx, tx, q.LockMailDomainParams{Namespace: s.namespace, ID: pgID(id)})
		if err != nil {
			return nil, storeError(err)
		}
		d := domainRow(row)
		if d.Status != "verified" {
			return nil, ErrUnverified
		}
		key, err := s.queries.DKIMKey(ctx, tx, q.DKIMKeyParams{Namespace: s.namespace, DomainID: row.ID, ID: pgID(keyID)})
		if err != nil {
			return nil, storeError(err)
		}
		if key.State != "staged" {
			return nil, ErrConflict
		}
		if err := s.checkEncryptionWrite(ctx, tx, key.SealedKey, nil, false); err != nil {
			return nil, err
		}
		if !key.FirstVerifiedAt.Valid || key.CheckResult != "valid" || time.Since(key.FirstVerifiedAt.Time) < api.InternalMailDKIMPropagationWait {
			return nil, ErrKeyPropagation
		}
		old, err := s.requiredKeys(ctx, tx, d)
		if err != nil {
			return nil, err
		}
		old = append(old, DKIMKey{Record: s.domainResponseDKIM(d)})
		d.Selector, d.PublicKey = key.Selector, key.PublicKey
		outcome, err := observe(ctx, d, old)
		if err != nil {
			return nil, err
		}
		if outcome != "valid" {
			failed, err := s.queries.RecordDKIMVerification(ctx, tx, q.RecordDKIMVerificationParams{Namespace: s.namespace, DomainID: row.ID, KeyID: key.ID, Outcome: outcome})
			return keyRow(failed, d.Name), storeError(err)
		}
		if _, err := s.queries.DeactivateDKIMKey(ctx, tx, q.DeactivateDKIMKeyParams{Namespace: s.namespace, DomainID: row.ID}); err != nil {
			return nil, err
		}
		active, err := s.queries.ActivateDKIMKey(ctx, tx, q.ActivateDKIMKeyParams{Namespace: s.namespace, DomainID: row.ID, KeyID: key.ID, PropagationSeconds: int32(api.InternalMailDKIMPropagationWait / time.Second)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrKeyPropagation
		}
		if err != nil {
			return nil, err
		}
		_, err = s.queries.SetDomainSigner(ctx, tx, q.SetDomainSignerParams{Namespace: s.namespace, ID: row.ID, Selector: key.Selector, PublicKey: key.PublicKey, SealedKey: key.SealedKey, DkimKeyID: key.ID, VerifiedSendingIp: ip})
		return keyRow(active, d.Name), storeError(err)
	})
}

func (s *PGStore) domainResponseDKIM(d Domain) DNSRecord {
	return DNSRecord{Name: d.Selector + "._domainkey." + d.Name, Type: "TXT", Value: "v=DKIM1; k=rsa; p=" + d.PublicKey}
}

func (s *PGStore) retireDKIM(ctx context.Context, id, keyID string, input OperatorAuditInput, enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input, "dkim_retire", id, keyAuditRequest{input, keyID}, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.shareEncryption(ctx, tx); err != nil {
			return nil, err
		}
		d, err := s.queries.LockMailDomain(ctx, tx, q.LockMailDomainParams{Namespace: s.namespace, ID: pgID(id)})
		if err != nil {
			return nil, storeError(err)
		}
		r, err := s.queries.RetireDKIMKey(ctx, tx, q.RetireDKIMKeyParams{Namespace: s.namespace, DomainID: d.ID, KeyID: pgID(keyID), OverlapSeconds: int32(api.InternalMailDKIMOverlap / time.Second)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrKeyOverlap
		}
		if err != nil {
			return nil, storeError(err)
		}
		if err := s.queries.BumpDomainDNSRevision(ctx, tx, q.BumpDomainDNSRevisionParams{Namespace: s.namespace, ID: d.ID}); err != nil {
			return nil, err
		}
		return keyRow(r, d.Name), storeError(err)
	})
}

func (s *PGStore) requiredKeys(ctx context.Context, db q.DBTX, d Domain) ([]DKIMKey, error) {
	rows, err := s.queries.RequiredDKIMRecords(ctx, db, q.RequiredDKIMRecordsParams{Namespace: s.namespace, DomainID: pgID(d.ID), Limit: api.InternalMailMaxLiveDKIMKeys + 1})
	if err != nil {
		return nil, err
	}
	if len(rows) > api.InternalMailMaxLiveDKIMKeys {
		return nil, ErrQuota
	}
	items := make([]DKIMKey, 0, len(rows))
	for _, r := range rows {
		items = append(items, keyPublicRow(q.OperatorDKIMKeysRow(r), d.Name))
	}
	return items, nil
}
func (s *PGStore) requiredDKIMRecords(ctx context.Context, id string) ([]DKIMKey, error) {
	d, err := s.operatorDomain(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.requiredKeys(ctx, s.pool, d)
}

func (s *PGStore) domainCheckCandidates(ctx context.Context, ip string) ([]Domain, error) {
	rows, err := s.queries.DomainCheckCandidates(ctx, s.pool, q.DomainCheckCandidatesParams{Namespace: s.namespace, SendingIp: ip, FailureSeparationSeconds: int32(api.InternalMailDomainFailureSeparation / time.Second), PollSeconds: int32(api.InternalMailDomainCheckPoll / time.Second), IntervalSeconds: int32(api.InternalMailDomainCheckInterval / time.Second), BatchSize: api.InternalMailDomainCheckBatchSize})
	items := make([]Domain, 0, len(rows))
	for _, r := range rows {
		items = append(items, domainPublicRow(q.OperatorDomainRow(r)))
	}
	return items, err
}

func (s *PGStore) operatorReverify(ctx context.Context, id string, input OperatorAuditInput, observe func(context.Context, Domain, []DKIMKey) (string, error), ip string, enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input, "domain_reverify", id, input, enabled, func(tx pgx.Tx) (any, error) {
		row, err := s.queries.LockMailDomain(ctx, tx, q.LockMailDomainParams{Namespace: s.namespace, ID: pgID(id)})
		if err != nil {
			return nil, storeError(err)
		}
		d := domainRow(row)
		if d.Status != "verified" {
			return nil, ErrConflict
		}
		keys, err := s.requiredKeys(ctx, tx, d)
		if err != nil {
			return nil, err
		}
		outcome, err := observe(ctx, d, keys)
		if err != nil {
			return nil, err
		}
		return s.recordDomainCheckTx(ctx, tx, d, outcome, ip, false)
	})
}
