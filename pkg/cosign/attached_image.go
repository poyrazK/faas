package cosign

// adr: 429
// The pure verifier lives below storage and state in the dependency graph.
import (
	"context"
	"crypto/ecdsa"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type ImageSignatureAttachment = imagepublisher.ImageSignatureAttachment
type ImageSignatureAttachmentPuller = imagepublisher.ImageSignatureAttachmentPuller
type ImageSignatureProof = imagepublisher.ImageSignatureProof
type ImageSignatureEvidence = imagepublisher.ImageSignatureEvidence

func VerifyImageSignatureAttachments(ctx context.Context, puller ImageSignatureAttachmentPuller, ref string, publishers []TrustedPublisher) (ImageSignatureProof, error) {
	return imagepublisher.VerifyImageSignatureAttachments(ctx, puller, ref, publishers)
}
func ReverifyImageSignatureProof(proof ImageSignatureProof, key []byte) error {
	return imagepublisher.ReverifyImageSignatureProof(proof, key)
}
func PublisherKeySHA256(key *ecdsa.PublicKey) (string, error) {
	return imagepublisher.PublisherKeySHA256(key)
}
