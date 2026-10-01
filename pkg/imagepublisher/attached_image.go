package imagepublisher

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// ImageSignatureAttachment is detached simple-signing data, not a rootfs
// signature. Transport verifies the manifest and payload content chain;
// verification below authenticates the exact payload bytes and image claim.
type ImageSignatureAttachment struct {
	ManifestDigest string
	PayloadDigest  string
	Payload        []byte
	Signature      []byte
}

type ImageSignatureAttachmentPuller interface {
	ResolveDigest(context.Context, string) (string, error)
	FetchSignatureAttachments(context.Context, string, string) ([]ImageSignatureAttachment, error)
}

// ImageSignatureProof identifies a cryptographically verified registry
// subject and approved key. It does not by itself attest a converted rootfs,
// provide an expiry lease or satisfy application-standard runtime admission.
type ImageSignatureProof struct {
	SubjectDigest            string
	PublisherName            string
	PublisherKeySHA256       string
	AttachmentManifestDigest string
	PayloadDigest            string
	SignatureDigest          string
	Evidence                 *ImageSignatureEvidence `json:"-"`
}

// Evidence retains the exact signed bytes for storage-time verification
// against current approved keys. It contains no registry credentials.
type ImageSignatureEvidence struct {
	Payload   []byte
	Signature []byte
}

// ReverifyImageSignatureProof does not trust claimed fingerprints or digests.
// It authenticates the retained bytes using a current, externally approved key.
func ReverifyImageSignatureProof(proof ImageSignatureProof, publicKeyDER []byte) error {
	if _, err := digestBytesFromDigest(proof.SubjectDigest); err != nil {
		return ErrSignatureInvalid
	}
	if proof.Evidence == nil {
		return ErrSignatureInvalid
	}
	parsed, err := x509.ParsePKIXPublicKey(publicKeyDER)
	if err != nil {
		return ErrSignatureInvalid
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return ErrSignatureInvalid
	}
	actual, ok := verifyImageSignatureAttachment(ImageSignatureAttachment{
		ManifestDigest: proof.AttachmentManifestDigest, PayloadDigest: proof.PayloadDigest,
		Payload: proof.Evidence.Payload, Signature: proof.Evidence.Signature,
	}, proof.SubjectDigest, []TrustedPublisher{{Name: proof.PublisherName, PublicKey: key}})
	if !ok || actual.PublisherKeySHA256 != proof.PublisherKeySHA256 || actual.SignatureDigest != proof.SignatureDigest {
		return ErrSignatureInvalid
	}
	return nil
}

// VerifyImageSignatureAttachments verifies keyed ECDSA-P256 simple-signing
// attachments. Certificates, Rekor annotations and attachment-provided keys
// cannot add trust: every successful signature must match the supplied key set.
func VerifyImageSignatureAttachments(ctx context.Context, puller ImageSignatureAttachmentPuller, ref string, publishers []TrustedPublisher) (ImageSignatureProof, error) {
	if puller == nil || ref == "" {
		return ImageSignatureProof{}, errors.New("cosign: signature attachment verifier unavailable or empty reference")
	}
	if len(publishers) == 0 {
		return ImageSignatureProof{}, ErrSignatureInvalid
	}
	if err := ctx.Err(); err != nil {
		return ImageSignatureProof{}, err
	}
	digest, err := puller.ResolveDigest(ctx, ref)
	if err != nil {
		return ImageSignatureProof{}, fmt.Errorf("cosign: resolve signature subject: %w", err)
	}
	if _, err := digestBytesFromDigest(digest); err != nil {
		return ImageSignatureProof{}, fmt.Errorf("%w: noncanonical signature subject", ErrSignatureInvalid)
	}
	attachments, err := puller.FetchSignatureAttachments(ctx, ref, digest)
	if err != nil {
		return ImageSignatureProof{}, err
	}
	if len(attachments) == 0 {
		return ImageSignatureProof{}, ErrSignatureMissing
	}
	if len(attachments) > api.ImageSignatureMaxEntries {
		return ImageSignatureProof{}, ErrSignatureInvalid
	}
	for _, attachment := range attachments {
		if err := ctx.Err(); err != nil {
			return ImageSignatureProof{}, err
		}
		if proof, ok := verifyImageSignatureAttachment(attachment, digest, publishers); ok {
			if err := ctx.Err(); err != nil {
				return ImageSignatureProof{}, err
			}
			return proof, nil
		}
	}
	return ImageSignatureProof{}, ErrSignatureInvalid
}

func verifyImageSignatureAttachment(attachment ImageSignatureAttachment, digest string, publishers []TrustedPublisher) (ImageSignatureProof, bool) {
	if len(attachment.Payload) == 0 || int64(len(attachment.Payload)) > api.ImageSignatureMaxPayloadBytes ||
		len(attachment.Signature) == 0 || len(attachment.Signature) > api.ImageSignatureMaxDERBytes {
		return ImageSignatureProof{}, false
	}
	if _, err := digestBytesFromDigest(attachment.ManifestDigest); err != nil {
		return ImageSignatureProof{}, false
	}
	payloadHash := sha256.Sum256(attachment.Payload)
	if fmt.Sprintf("sha256:%x", payloadHash) != attachment.PayloadDigest || !validSimpleImageClaim(attachment.Payload, digest) {
		return ImageSignatureProof{}, false
	}
	for _, publisher := range publishers {
		fingerprint, err := PublisherKeySHA256(publisher.PublicKey)
		if err != nil || publisher.Name == "" || !ecdsa.VerifyASN1(publisher.PublicKey, payloadHash[:], attachment.Signature) {
			continue
		}
		return ImageSignatureProof{
			SubjectDigest: digest, PublisherName: publisher.Name, PublisherKeySHA256: fingerprint,
			AttachmentManifestDigest: attachment.ManifestDigest, PayloadDigest: attachment.PayloadDigest,
			SignatureDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(attachment.Signature)),
			Evidence:        &ImageSignatureEvidence{Payload: append([]byte(nil), attachment.Payload...), Signature: append([]byte(nil), attachment.Signature...)},
		}, true
	}
	return ImageSignatureProof{}, false
}

// PublisherKeySHA256 hashes canonical SPKI DER. Names and mutable PEM spelling
// are never key identity. The current publisher resource vocabulary is P256.
func PublisherKeySHA256(pub *ecdsa.PublicKey) (string, error) {
	if pub == nil || pub.Curve != elliptic.P256() || pub.X == nil || pub.Y == nil || !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return "", errors.New("cosign: publisher key is not valid ECDSA P256")
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("cosign: encode publisher key: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(der)), nil
}
