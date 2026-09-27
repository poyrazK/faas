package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/servicecaller"
)

const serviceCallerKeyRotationGrace = servicecaller.MaxTTL

// ServiceCallerKey is one node's published public half for ADR-206 caller
// assertions. Only the public key is stored; the private key never leaves the
// node that generated it.
type ServiceCallerKey struct {
	NodeID       string
	KeyID        string
	PublicKeyPEM string
}

type retiredServiceCallerKey struct {
	key      ServiceCallerKey
	retireAt time.Time
}

// ServiceCallerKeyStore is the narrow surface used to publish a node's key
// and expose the current verification set to peers and workloads.
//
// It is a separate interface rather than more methods on Store because only
// daemon key publication and verification-key discovery need these methods.
type ServiceCallerKeyStore interface {
	// PublishServiceCallerKey records this node's current public key,
	// retaining a replaced key through the assertion lifetime. Re-publishing
	// the same key is a no-op so a restart does not churn rotated_at.
	PublishServiceCallerKey(ctx context.Context, key ServiceCallerKey) error
	// ListServiceCallerKeys returns every current key and any recently retired
	// keys, including this node's. An assertion is minted by the caller's node,
	// which is frequently not the node doing the verifying.
	ListServiceCallerKeys(ctx context.Context) ([]ServiceCallerKey, error)
}
