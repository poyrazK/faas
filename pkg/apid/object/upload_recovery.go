package object

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ReconcileObjectUploads(ctx context.Context, observe func(string, string)) error {
	st, ok := s.Host.Store().(state.ObjectTrackedUploadStore)
	if !ok {
		return nil
	}
	rows, err := st.DueTrackedObjectUploads(ctx, api.ObjectUploadRecoveryBatch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return err
		}
		c, e := st.ClaimTrackedObjectUploadRecovery(ctx, row.AccountID, row.BucketID, row.ID, uuid.NewString())
		if errors.Is(e, state.ErrConflict) || errors.Is(e, state.ErrNotFound) {
			continue
		}
		if e != nil {
			return e
		}
		outcome := "failed"
		if c.WritePhase == state.ObjectUploadDispatched {
			outcome, e = s.confirmObjectUpload(ctx, st, c)
			if e != nil {
				return e
			}
		}
		if observe != nil {
			operation := "upload"
			switch c.Origin {
			case "gateway":
				operation = "gateway_put"
			case "gateway_copy":
				operation = "gateway_copy"
			}
			observe(operation, outcome)
		}
	}
	return nil
}
func (s *Service) confirmObjectUpload(ctx context.Context, st state.ObjectTrackedUploadStore, c state.ObjectUploadCompletion) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, api.ObjectUploadRecoveryProbeTimeout)
	result, err := s.ProbeObjectUpload(probeCtx, c)
	cancel()
	c.RecoveryCursor = result.Cursor
	c.RecoveryVersionsObserved = c.RecoveryVersionsObserved || result.VersionsObserved
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer finishCancel()
	if err == nil || errors.Is(err, objectstorage.ErrWriteRejected) {
		outcome := "completed"
		c.Status, c.ErrorCode = "completed", ""
		c.ETag, c.ProviderVersionID = result.ETag, result.ProviderVersionID
		c.VerifiedProtection, c.VerifiedEncryption = result.VerifiedProtection, result.Encryption
		if errors.Is(err, objectstorage.ErrWriteRejected) {
			outcome = "failed"
			c.Status, c.ErrorCode = "failed", "provider_write_rejected"
			c.ETag, c.ProviderVersionID = "", ""
			c.VerifiedProtection = ""
			c.VerifiedEncryption = api.ObjectEncryption{}
		}
		_, err = st.FinishTrackedObjectUploadRecovery(finishCtx, c)
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			return "stale", nil
		}
		return outcome, err
	}
	code := "provider_write_uncertain"
	if errors.Is(err, objectstorage.ErrConfiguration) || errors.Is(err, objectstorage.ErrUnsupported) {
		code = "configuration"
	}
	retryErr := st.RetryTrackedObjectUploadRecovery(finishCtx, c, code)
	if errors.Is(retryErr, state.ErrConflict) || errors.Is(retryErr, state.ErrNotFound) {
		return "stale", nil
	}
	return "deferred", retryErr
}
func (s *Service) ProbeObjectUpload(ctx context.Context, c state.ObjectUploadCompletion) (objectstorage.ObjectHistoryProofPage, error) {
	page := objectstorage.ObjectHistoryProofPage{Cursor: c.RecoveryCursor}
	if s.Host.ObjectStorage() == nil {
		return page, objectstorage.ErrConfiguration
	}
	buckets, ok := s.Host.Store().(state.ObjectBucketStore)
	if !ok {
		return page, objectstorage.ErrConfiguration
	}
	b, err := buckets.GetObjectBucket(ctx, c.AccountID, c.AppID, c.BucketID)
	if err != nil {
		return page, err
	}
	// New journals carry one pinned provider receipt. Old unbound journals may
	// still be probed, but their unrelated unknown receipts cannot be retired.
	if bound, ok := s.Host.Store().(state.ObjectTrackedUploadMutationStore); ok {
		receipt, readErr := bound.ReadTrackedObjectUploadMutation(ctx, c)
		if readErr == nil {
			b = receipt.Bucket
		} else if !errors.Is(readErr, state.ErrNotFound) {
			return page, readErr
		}
	}
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return page, objectstorage.ErrConfiguration
	}
	if !c.Encryption.Empty() {
		if !c.Encryption.ValidFor(c.AccountID) || backend.Encryption.VerifySnapshot(c.Encryption.AccountID, c.Encryption) != nil {
			return page, objectstorage.ErrConfiguration
		}
	}
	writer, ok := backend.Provider.(objectstorage.ObjectWriteConfirmer)
	if !ok {
		return page, objectstorage.ErrUnsupported
	}
	before := func(ctx context.Context) error {
		metrics, ok := s.Host.Store().(state.ObjectStorageProviderUsageStore)
		if !ok {
			return objectstorage.ErrConfiguration
		}
		return metrics.RecordObjectStorageProviderRequest(ctx, c.BucketID, time.Now().UTC())
	}
	ctx, err = objectstorage.WithObjectWriteProtection(ctx, backend.Provider, c.Protection, before)
	if err != nil {
		return page, err
	}
	if err = before(ctx); err != nil {
		return page, err
	}
	var proof objectstorage.UploadResult
	if c.Encryption.Empty() {
		proof, err = writer.ConfirmTrackedObject(ctx, b.PhysicalName, c.Key, c.ID, c.Bytes)
	} else {
		encrypted, capable := backend.Provider.(objectstorage.ObjectEncryptionProvider)
		if !capable || !c.Encryption.ValidFor(c.AccountID) {
			return page, objectstorage.ErrConfiguration
		}
		proof, err = encrypted.ConfirmEncryptedObject(ctx, b.PhysicalName, c.Key, c.ID, c.Bytes, c.Encryption)
	}
	if err == nil {
		page.UploadResult = proof
		page.Cursor = ""
		page.VersionsObserved = proof.ProviderVersionID != "" && proof.ProviderVersionID != "null"
		return page, nil
	}
	if !errors.Is(err, objectstorage.ErrNotFound) && !errors.Is(err, objectstorage.ErrConflict) {
		return page, err
	}
	history, ok := backend.Provider.(objectstorage.HistoricalObjectWriteConfirmer)
	if !ok {
		return page, err
	}
	request := objectstorage.ObjectHistoryProofRequest{Key: c.Key, Receipt: c.ID, SizeBytes: c.Bytes, Cursor: c.RecoveryCursor, BeforeRequest: before}
	var historical objectstorage.ObjectHistoryProofPage
	var historyErr error
	if c.Encryption.Empty() {
		historical, historyErr = history.ConfirmTrackedObjectHistory(ctx, b.PhysicalName, request)
	} else {
		historical, historyErr = backend.Provider.(objectstorage.ObjectEncryptionProvider).ConfirmEncryptedObjectHistory(ctx, b.PhysicalName, request, c.Encryption)
	}
	if errors.Is(historyErr, objectstorage.ErrUnsupported) {
		return page, err
	}
	return historical, historyErr
}
