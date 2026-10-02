package imaged

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/oci"
)

// ociImageSignaturePuller preserves repository-scoped credentials and keeps
// registry transport separate from publisher cryptography (ADR-429).
type ociImageSignaturePuller struct {
	oci  oci.Puller
	auth *oci.BasicAuth
}

func (p *ociImageSignaturePuller) ResolveDigest(ctx context.Context, ref string) (string, error) {
	r, err := oci.ParseReference(ref)
	if err != nil {
		return "", err
	}
	var digest string
	if resolver, ok := p.oci.(oci.ImageResolver); ok && r.Digest == "" {
		resolved, resolveErr := resolver.ResolveImage(ctx, ref, p.auth)
		digest, err = resolved.SourceDigest, resolveErr
	} else {
		digest, err = pullDigestWithAuth(ctx, p.oci, ref, p.auth)
	}
	if errors.Is(err, oci.ErrImageManifestInvalid) {
		return "", errors.Join(cosign.ErrSignatureInvalid, err)
	}
	return digest, err
}

func (p *ociImageSignaturePuller) FetchSignatureAttachments(ctx context.Context, ref, digest string) ([]cosign.ImageSignatureAttachment, error) {
	puller, ok := p.oci.(oci.ImageSignatureAttachmentPuller)
	if !ok {
		return nil, errors.New("imaged: registry puller cannot read signature attachments")
	}
	attachments, err := puller.PullImageSignatureAttachments(ctx, ref, digest, p.auth)
	switch {
	case errors.Is(err, oci.ErrImageSignatureMissing):
		return nil, errors.Join(cosign.ErrSignatureMissing, err)
	case errors.Is(err, oci.ErrImageManifestInvalid):
		return nil, errors.Join(cosign.ErrSignatureInvalid, err)
	case err != nil:
		return nil, err
	}
	result := make([]cosign.ImageSignatureAttachment, 0, len(attachments))
	for _, a := range attachments {
		result = append(result, cosign.ImageSignatureAttachment{ManifestDigest: a.ManifestDigest, PayloadDigest: a.PayloadDigest, Payload: a.Payload, Signature: a.Signature})
	}
	return result, nil
}

var _ cosign.ImageSignatureAttachmentPuller = (*ociImageSignaturePuller)(nil)
