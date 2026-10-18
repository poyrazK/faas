package internalmail

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) encryptionState(ctx context.Context, db q.DBTX, legacyID string) (q.InternalMailEncryptionState, error) {
	state, err := s.queries.EncryptionState(ctx, db, s.namespace)
	if errors.Is(err, pgx.ErrNoRows) {
		return q.InternalMailEncryptionState{Namespace: s.namespace, ActiveKeyID: legacyID, LegacyKeyID: legacyID}, nil
	}
	return state, err
}

func (s *PGStore) encryptionInventory(ctx context.Context, db q.DBTX, legacyID string) ([]q.EncryptionInventoryRow, error) {
	rows, err := s.queries.EncryptionInventory(ctx, db, q.EncryptionInventoryParams{Namespace: s.namespace, LegacyKeyID: legacyID, KeyLimit: api.InternalMailMaxEncryptionKeys + 1})
	if err != nil {
		return nil, err
	}
	if len(rows) > api.InternalMailMaxEncryptionKeys {
		return nil, ErrQuota
	}
	for _, row := range rows {
		if !validEncryptionKeyID(row.KeyID) {
			return nil, ErrEncryptionKeyUnavailable
		}
	}
	return rows, nil
}

func (s *PGStore) encryptionStatusTx(ctx context.Context, db q.DBTX, codec *KeyringCodec) (EncryptionStatus, error) {
	state, err := s.encryptionState(ctx, db, codec.legacyID)
	if err != nil {
		return EncryptionStatus{}, err
	}
	if state.LegacyKeyID != codec.legacyID {
		return EncryptionStatus{}, ErrEncryptionKeyUnavailable
	}
	rows, err := s.encryptionInventory(ctx, db, state.LegacyKeyID)
	if err != nil {
		return EncryptionStatus{}, err
	}
	keys := map[string]EncryptionKeyStatus{}
	for _, id := range codec.configuredIDs() {
		keys[id] = EncryptionKeyStatus{KeyID: id, Configured: true}
	}
	for _, r := range rows {
		keys[r.KeyID] = EncryptionKeyStatus{KeyID: r.KeyID, Configured: codec.keys[r.KeyID] != nil, Payloads: r.Payloads, DomainKeys: r.DomainKeys, DKIMKeys: r.DkimKeys, Fingerprints: r.Fingerprints, PrunedFingerprints: r.PrunedFingerprints}
	}
	active := keys[state.ActiveKeyID]
	active.KeyID, active.Active = state.ActiveKeyID, true
	keys[state.ActiveKeyID] = active
	ids := make([]string, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	retired, err := s.queries.ConfiguredEncryptionRetirements(ctx, db, q.ConfiguredEncryptionRetirementsParams{Namespace: s.namespace, KeyIds: ids})
	if err != nil {
		return EncryptionStatus{}, err
	}
	for _, r := range retired {
		key := keys[r.KeyID]
		key.RetiredAt = timePointer(r.RetiredAt)
		keys[r.KeyID] = key
	}
	status := EncryptionStatus{Revision: state.Revision, ActiveKeyID: state.ActiveKeyID, LegacyKeyID: state.LegacyKeyID, Versioned: state.Revision > 0, Keys: []EncryptionKeyStatus{}}
	for _, id := range ids {
		status.Keys = append(status.Keys, keys[id])
	}
	return status, nil
}

func (s *PGStore) encryptionStatus(ctx context.Context, codec *KeyringCodec) (EncryptionStatus, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EncryptionStatus{}, err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.queries.ShareEncryption(ctx, tx, s.namespace); err != nil {
		return EncryptionStatus{}, err
	}
	return s.encryptionStatusTx(ctx, tx, codec)
}

// ConfigureEncryption runs before the listener/workers start and after an
// operator change. Missing required keys fail readiness, including management
// rollback. No path silently substitutes the currently available identity.
func (s *PGStore) ConfigureEncryption(ctx context.Context, codec *KeyringCodec) error {
	if codec == nil || codec.namespace != s.namespace {
		return ErrInvalid
	}
	status, err := s.encryptionStatus(ctx, codec)
	if err != nil {
		return err
	}
	for _, key := range status.Keys {
		if key.required() && (!key.Configured || key.RetiredAt != nil) {
			return ErrEncryptionKeyUnavailable
		}
	}
	return codec.selectKey(status.ActiveKeyID, status.Revision)
}

func (s *PGStore) refreshEncryptionSelection(ctx context.Context, codec *KeyringCodec) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.shareEncryption(ctx, tx); err != nil {
		return err
	}
	state, err := s.encryptionState(ctx, tx, codec.legacyID)
	if err != nil {
		return err
	}
	if state.LegacyKeyID != codec.legacyID {
		return ErrEncryptionKeyUnavailable
	}
	return codec.selectKey(state.ActiveKeyID, state.Revision)
}

func (s *PGStore) previewEncryption(ctx context.Context, size int, codec *KeyringCodec) (EncryptionBatch, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EncryptionBatch{}, err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.queries.ShareEncryption(ctx, tx, s.namespace); err != nil {
		return EncryptionBatch{}, err
	}
	state, err := s.encryptionState(ctx, tx, codec.legacyID)
	if err != nil {
		return EncryptionBatch{}, err
	}
	if state.Revision == 0 || state.LegacyKeyID != codec.legacyID {
		return EncryptionBatch{}, ErrConflict
	}
	rows, err := s.queries.EncryptionCandidates(ctx, tx, q.EncryptionCandidatesParams{Namespace: s.namespace, ActiveKeyID: state.ActiveKeyID, BatchSize: int32(size)})
	return encryptionBatch(state, size, true, rows), err
}

func encryptionBatch(state q.InternalMailEncryptionState, size int, dryRun bool, rows []q.EncryptionCandidatesRow) EncryptionBatch {
	result := EncryptionBatch{DryRun: dryRun, KeyID: state.ActiveKeyID, Revision: state.Revision, BatchSize: size, BatchFull: len(rows) == size}
	for _, row := range rows {
		switch row.Kind {
		case "domain":
			result.DomainKeys++
		case "dkim":
			result.DKIMKeys++
		case "payload":
			result.Payloads++
		}
	}
	return result
}

func (s *PGStore) changeEncryption(ctx context.Context, action string, input EncryptionRequest, codec *KeyringCodec, enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input.OperatorAuditInput, "encryption_"+action, input.KeyID, input, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.queries.LockEncryption(ctx, tx, s.namespace); err != nil {
			return nil, err
		}
		state, err := s.encryptionState(ctx, tx, codec.legacyID)
		if err != nil {
			return nil, err
		}
		if state.Revision != *input.ExpectedRevision {
			return nil, ErrEncryptionRevision
		}
		if state.LegacyKeyID != codec.legacyID {
			return nil, ErrEncryptionKeyUnavailable
		}
		switch action {
		case "activate":
			return s.activateEncryption(ctx, tx, state, input.KeyID, codec)
		case "retire":
			return s.retireEncryption(ctx, tx, state, input.KeyID)
		case "rewrap":
			return s.rewrapEncryption(ctx, tx, state, input, codec, enabled)
		default:
			return nil, ErrInvalid
		}
	})
}

func (s *PGStore) activateEncryption(ctx context.Context, tx pgx.Tx, state q.InternalMailEncryptionState, id string, codec *KeyringCodec) (any, error) {
	if codec.keys[id] == nil {
		return nil, ErrEncryptionKeyUnavailable
	}
	if state.Revision > 0 && id == state.ActiveKeyID {
		return nil, ErrConflict
	}
	retired, err := s.queries.EncryptionKeyRetired(ctx, tx, q.EncryptionKeyRetiredParams{Namespace: s.namespace, KeyID: id})
	if err != nil {
		return nil, err
	}
	if retired {
		return nil, ErrConflict
	}
	// Refuse activation if it would hide an already missing read dependency.
	status, err := s.encryptionStatusTx(ctx, tx, codec)
	if err != nil {
		return nil, err
	}
	for _, key := range status.Keys {
		if key.required() && (!key.Configured || key.RetiredAt != nil) {
			return nil, ErrEncryptionKeyUnavailable
		}
	}
	next, err := s.queries.SelectEncryptionKey(ctx, tx, q.SelectEncryptionKeyParams{Namespace: s.namespace, ActiveKeyID: id, LegacyKeyID: codec.legacyID})
	return encryptionSelection(next), err
}

func encryptionSelection(state q.InternalMailEncryptionState) map[string]any {
	return map[string]any{"active_key_id": state.ActiveKeyID, "legacy_key_id": state.LegacyKeyID, "revision": state.Revision}
}

func (s *PGStore) retireEncryption(ctx context.Context, tx pgx.Tx, state q.InternalMailEncryptionState, id string) (any, error) {
	if state.Revision == 0 || state.ActiveKeyID == id {
		return nil, ErrEncryptionKeyRequired
	}
	rows, err := s.encryptionInventory(ctx, tx, state.LegacyKeyID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.KeyID == id && row.Payloads+row.DomainKeys+row.DkimKeys+row.Fingerprints > 0 {
			return nil, ErrEncryptionKeyRequired
		}
	}
	if err := s.queries.RetireEncryptionKey(ctx, tx, q.RetireEncryptionKeyParams{Namespace: s.namespace, KeyID: id}); err != nil {
		return nil, err
	}
	next, err := s.queries.AdvanceEncryptionRevision(ctx, tx, s.namespace)
	return map[string]any{"key_id": id, "retired": true, "revision": next.Revision}, err
}

func (s *PGStore) rewrapEncryption(ctx context.Context, tx pgx.Tx, state q.InternalMailEncryptionState, input EncryptionRequest, codec *KeyringCodec, enabled func() bool) (EncryptionBatch, error) {
	if state.Revision == 0 || input.KeyID != state.ActiveKeyID {
		return EncryptionBatch{}, ErrEncryptionRevision
	}
	if codec.keys[state.ActiveKeyID] == nil {
		return EncryptionBatch{}, ErrEncryptionKeyUnavailable
	}
	// Freeze the target for this batch. A late local refresh from an earlier
	// configuration read cannot change a batch's encryption or HMAC key.
	batchCodec := &KeyringCodec{namespace: codec.namespace, legacyID: codec.legacyID, keys: codec.keys, activeID: state.ActiveKeyID, versioned: true}
	rows, err := s.queries.EncryptionCandidates(ctx, tx, q.EncryptionCandidatesParams{Namespace: s.namespace, ActiveKeyID: state.ActiveKeyID, BatchSize: int32(input.BatchSize)})
	if err != nil {
		return EncryptionBatch{}, err
	}
	result := encryptionBatch(state, input.BatchSize, false, rows)
	for _, row := range rows {
		if enabled == nil || !enabled() {
			return EncryptionBatch{}, ErrDisabled
		}
		if err := s.rewrapEncryptedRow(ctx, tx, batchCodec, row); err != nil {
			return EncryptionBatch{}, err
		}
		result.Rewrapped++
	}
	return result, nil
}

func (s *PGStore) rewrapEncryptedRow(ctx context.Context, tx pgx.Tx, codec *KeyringCodec, row q.EncryptionCandidatesRow) error {
	id, domainID := idString(row.ID), idString(row.DomainID)
	resource := "domain:" + domainID
	if row.Kind == "payload" {
		resource = "message:" + id
	} else if !row.Legacy {
		resource = dkimKeyResource(domainID, idString(row.ResourceID))
	}
	plain, err := codec.Open(resource, row.Sealed)
	if err != nil {
		return ErrEncryptionKeyUnavailable
	}
	defer clear(plain)
	if row.Kind == "payload" && len(row.Fingerprint) != 0 {
		if err := codec.VerifyFingerprint(resource, plain, row.Fingerprint); err != nil {
			return err
		}
	}
	sealed, err := codec.Seal(resource, plain)
	if err != nil {
		return err
	}
	var n int64
	switch row.Kind {
	case "domain":
		n, err = s.queries.RewrapDomainKey(ctx, tx, q.RewrapDomainKeyParams{Namespace: s.namespace, ID: row.ID, SealedKey: sealed})
	case "dkim":
		n, err = s.queries.RewrapDKIMKey(ctx, tx, q.RewrapDKIMKeyParams{Namespace: s.namespace, ID: row.ID, SealedKey: sealed})
	case "payload":
		fingerprint, prepareErr := codec.Fingerprint(resource, plain)
		if prepareErr != nil {
			return prepareErr
		}
		n, err = s.queries.RewrapMessage(ctx, tx, q.RewrapMessageParams{Namespace: s.namespace, ID: row.ID, SealedPayload: sealed, PayloadFingerprint: fingerprint})
	default:
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
