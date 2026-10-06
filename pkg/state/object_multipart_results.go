package state

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func validMultipartResultOwner(old, request ObjectMultipartUpload) bool {
	return old.Protection.Equal(request.Protection) && old.EncryptionDefaultRevision == request.EncryptionDefaultRevision && old.Encryption.Equal(request.Encryption) && ObjectMultipartIsCompleting(old.State) && old.State == request.State && request.LeaseToken != "" && old.LeaseToken == request.LeaseToken && old.AccountID == request.AccountID && old.AppID == request.AppID && old.BucketID == request.BucketID && old.ID == request.ID && old.Key == request.Key && old.SizeBytes == request.SizeBytes && old.ProviderUploadID == request.ProviderUploadID && old.PartRevision == request.PartRevision && old.CompletionConditions == request.CompletionConditions && slices.Equal(old.Parts, request.Parts)
}

func validMultipartRecoveryResult(r ObjectMultipartCompletionResult) bool {
	return len(r.RecoveryCursor) <= api.ObjectUploadHistoryCursorMaxBytes && utf8.ValidString(r.RecoveryCursor) && strings.Trim(r.RecoveryCursor, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") == ""
}

func validMultipartFinalResult(u ObjectMultipartUpload, r ObjectMultipartCompletionResult) bool {
	if !u.Protection.Valid() || r.VerifiedProtection != u.Protection.Proof() || !u.Protection.Empty() && (r.ProviderVersionID == "" || r.ProviderVersionID == "null") || !u.Encryption.ValidFor(u.AccountID) || !equalEncryptionSelection(u.Encryption.Selection, r.VerifiedEncryption) || r.ETag == "" || strings.TrimSpace(r.ETag) == "" || len(r.ETag) > api.MaxObjectWriteETagBytes || !validVersionReferenceText(r.ETag, api.MaxObjectWriteETagBytes) || r.RecoveryCursor != "" {
		return false
	}
	return r.ProviderVersionID == "" || validVersionReferences([]ObjectVersionIdentity{{Key: u.Key, ProviderVersionID: r.ProviderVersionID}})
}

func emptyInitialMultipartResult(u ObjectMultipartUpload) bool {
	return (u.Encryption.Empty() && u.Protection.Requested.Empty() || u.ProviderUploadID == "" && u.LeaseToken == "" && u.LeaseUntil.IsZero()) && u.CompletionETag == "" && u.CompletionVersionID == "" && u.CompletionRecoveryCursor == "" && !u.CompletionDispatched && !u.CompletionVersionsObserved && u.PartURLUnsafeUntil.IsZero() && u.LifecycleAbort == (ObjectLifecycleMultipartBinding{})
}
