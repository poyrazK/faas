package state

import "context"

// ServiceCallerKey is one node's published public half for ADR-206 caller
// assertions. Only the public key is stored; the private key never leaves the
// node that generated it.
type ServiceCallerKey struct {
	NodeID       string
	KeyID        string
	PublicKeyPEM string
}

// ServiceCallerKeyStore is the narrow surface gatewayd-internal needs to
// publish its own key and learn every peer's.
//
// It is a separate interface rather than more methods on Store because the
// only consumer is one daemon's boot path plus its verifier refresh; widening
// the 795-method Store for two calls would make the seam harder to stub, not
// easier.
type ServiceCallerKeyStore interface {
	// PublishServiceCallerKey records this node's current public key,
	// replacing any previous one. Re-publishing the same key is a no-op so a
	// restart does not churn rotated_at.
	PublishServiceCallerKey(ctx context.Context, key ServiceCallerKey) error
	// ListServiceCallerKeys returns every published key, including this
	// node's. A verifier needs the whole set: an assertion is minted by the
	// caller's node, which is frequently not the node doing the verifying.
	ListServiceCallerKeys(ctx context.Context) ([]ServiceCallerKey, error)
}
