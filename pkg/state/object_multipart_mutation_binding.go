package state

import (
	"slices"
	"time"

	"github.com/google/uuid"
)

func validMultipartMutationScope(u ObjectMultipartUpload) bool {
	for _, id := range []string{u.ID, u.AccountID, u.AppID, u.BucketID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return false
		}
	}
	return true
}

func originalMultipartMutationAuthority(old, u ObjectMultipartUpload, now time.Time) bool {
	if old.ID != u.ID || old.AccountID != u.AccountID || old.AppID != u.AppID || old.BucketID != u.BucketID || old.Key != u.Key || old.ProviderUploadID != u.ProviderUploadID || old.State != u.State || u.LeaseToken == "" || old.LeaseToken != u.LeaseToken || !old.LeaseUntil.After(now) || !old.Protection.Equal(u.Protection) || !old.Encryption.Equal(u.Encryption) || old.EncryptionDefaultRevision != u.EncryptionDefaultRevision || old.ContentType != u.ContentType || !equalObjectMultipartMetadata(old.Metadata, u.Metadata) {
		return false
	}
	if old.State == ObjectMultipartInitiating {
		return old.ProviderUploadID == "" && old.SizeBytes == u.SizeBytes && old.PartSizeBytes == u.PartSizeBytes && old.PartCount == u.PartCount && old.FixedAdmission == u.FixedAdmission
	}
	if old.ProviderUploadID == "" {
		return false
	}
	if ObjectMultipartIsCompleting(old.State) {
		return old.SizeBytes == u.SizeBytes && old.PartRevision == u.PartRevision && old.CompletionConditions == u.CompletionConditions && slices.Equal(old.Parts, u.Parts)
	}
	return old.State == ObjectMultipartAborting
}
