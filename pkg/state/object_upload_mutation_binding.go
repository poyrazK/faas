package state

import (
	"time"

	"github.com/google/uuid"
)

func originalUploadMutationAuthority(old, c ObjectUploadCompletion, now time.Time) bool {
	if old.ID != c.ID || old.AccountID != c.AccountID || old.AppID != c.AppID || old.BucketID != c.BucketID || old.Key != c.Key || old.Bytes != c.Bytes || !old.Protection.Equal(c.Protection) || !old.Encryption.Equal(c.Encryption) || old.SubjectID != c.SubjectID || old.Origin != c.Origin || old.RouteID != c.RouteID || old.Status != "pending" || old.WritePhase != ObjectUploadPrepared && old.WritePhase != ObjectUploadDispatched || old.RecoveryToken != c.RecoveryToken {
		return false
	}
	return c.RecoveryToken == "" || validTrackedUploadRecovery(old, now)
}

func validUploadMutationScope(c ObjectUploadCompletion) bool {
	for _, id := range []string{c.ID, c.AccountID, c.AppID, c.BucketID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return false
		}
	}
	return true
}
