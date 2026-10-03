package s3gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) executeMultipartResult(w http.ResponseWriter, r *http.Request, req requestContext, store state.ObjectMultipartUploadStore, u state.ObjectMultipartUpload) {
	results, ok := store.(state.ObjectMultipartCompletionStore)
	if !ok {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, u.Key)
		return
	}
	callCtx, cancel := context.WithTimeout(r.Context(), api.ObjectMultipartOperationTimeout)
	defer cancel()
	if err := results.DispatchObjectMultipartCompletion(callCtx, u); err != nil {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
		return
	}
	var encryption *objectstorage.ResolvedObjectEncryption
	if !u.Encryption.Empty() {
		snapshot := u.Encryption.Clone()
		encryption = &snapshot
	}
	result, err := objectstorage.CompleteMultipartWithResult(callCtx, req.provider, req.bucket.PhysicalName, objectstorage.MultipartCompleteRequest{
		Encryption: encryption, SessionID: u.ID, Key: u.Key, ProviderUploadID: u.ProviderUploadID, SizeBytes: u.SizeBytes, Parts: toProviderParts(u.Parts), Recovering: u.CompletionDispatched, RecoveryCursor: u.CompletionRecoveryCursor,
		BeforeRequest: func(ctx context.Context) error {
			if h.requestMetrics == nil {
				return nil
			}
			return h.requestMetrics.RecordObjectStorageProviderRequest(ctx, req.bucket.ID, h.now().UTC())
		},
	}, u.CompletionConditions)
	proof := state.ObjectMultipartCompletionResult{ETag: result.ETag, ProviderVersionID: result.ProviderVersionID, RecoveryCursor: result.RecoveryCursor, VersionsObserved: result.VersionsObserved, VerifiedEncryption: result.Encryption}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(r.Context()), api.ObjectUploadSettlementTimeout)
	defer finishCancel()
	if err != nil {
		h.deferMultipartResult(w, r, req, finishCtx, results, u, proof, err)
		return
	}
	completed, err := results.FinishObjectMultipartCompletion(finishCtx, u, proof)
	if err != nil {
		// If settlement rolls back, retain any observation before recovery.
		_ = results.RetryObjectMultipartCompletion(finishCtx, u, proof, "temporary", api.ObjectUploadRecoveryRetry)
		h.writeMultipartError(w, r, req, err, "OperationAborted")
		return
	}
	writeMultipartCompleted(w, req, completed)
}

func (h *Handler) deferMultipartResult(w http.ResponseWriter, r *http.Request, req requestContext, ctx context.Context, store state.ObjectMultipartCompletionStore, u state.ObjectMultipartUpload, result state.ObjectMultipartCompletionResult, cause error) {
	code := objectstorage.MultipartCompletionFailureCode(cause)
	var err error
	if code != "" && u.State == state.ObjectMultipartCompletingConditional {
		err = store.RejectObjectMultipartCompletionResult(ctx, u, result, code)
	} else {
		err = store.RetryObjectMultipartCompletion(ctx, u, result, "temporary", api.ObjectUploadRecoveryRetry)
	}
	if err != nil {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
	} else if !h.writeMultipartCompletionFailure(w, r, req, code) {
		h.providerError(w, r, req, cause, u.Key)
	}
}
