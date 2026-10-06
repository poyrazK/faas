package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrObjectBucketWriteFenced = errors.New("object bucket writes are paused for checkpoint capture")

const (
	ObjectBucketMutationRequest     = "request"
	ObjectBucketMutationNativeGrant = "native_grant"
)

// A receipt pins the placement used by the caller before provider IO. Native
// grants are not synchronous requests: URL expiry, caller cancellation and
// signing completion do not prove that the provider has drained their writes.
type ObjectBucketMutation struct {
	ID        string
	Bucket    ObjectBucket
	Kind      string
	CreatedAt time.Time
}

type ObjectBucketWriteFence struct {
	Bucket                 ObjectBucket
	BucketID, Token        string
	CloneOperationID       string
	Requests, NativeGrants int64
	// Prepared and dispatched deletion intents retain their own recovery
	// journal. They drain only through an authenticated terminal transition.
	Deletions int64
	// Waiting/applying retention and legal-hold journals remain busy even
	// after worker expiry; only original terminal settlement drains them.
	Protections int64
}

// This is a private data-plane seam. A zero count covers only instrumented
// writers; it does not attest legacy URLs, external writers or a cross-store
// application checkpoint. The clone coordinator must establish those too.
type ObjectBucketMutationStore interface {
	BeginObjectBucketMutation(context.Context, ObjectBucket, string) (ObjectBucketMutation, error)
	FinishObjectBucketMutation(context.Context, ObjectBucketMutation) error
}

type ObjectBucketWriteFenceStore interface {
	ObjectBucketMutationStore
	AcquireObjectBucketWriteFence(context.Context, ObjectBucket, string) (ObjectBucketWriteFence, error)
	ReadObjectBucketWriteFence(context.Context, ObjectBucket, string) (ObjectBucketWriteFence, error)
	ReleaseObjectBucketWriteFence(context.Context, ObjectBucket, string) error
}

func validObjectMutationBucket(b ObjectBucket) bool {
	for _, id := range []string{b.ID, b.AccountID, b.AppID} {
		if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
			return false
		}
	}
	return b.BackendID != "" && b.BackendFingerprint != "" && b.PhysicalName != ""
}

func sameObjectMutationBucket(want, actual ObjectBucket) bool {
	return actual.State == "ready" && actual.ID == want.ID && actual.AccountID == want.AccountID && actual.AppID == want.AppID &&
		actual.BackendID == want.BackendID && actual.BackendFingerprint == want.BackendFingerprint && actual.PhysicalName == want.PhysicalName
}

func validObjectMutationKind(kind string) bool {
	return kind == ObjectBucketMutationRequest || kind == ObjectBucketMutationNativeGrant
}

func validObjectMutationToken(token string) bool {
	id, err := uuid.Parse(token)
	return err == nil && id != uuid.Nil && id.String() == token
}
