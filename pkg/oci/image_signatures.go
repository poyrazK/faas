package oci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const SimpleSigningMediaType = "application/vnd.dev.cosign.simplesigning.v1+json"
const imageSignatureAnnotation = "dev.cosignproject.cosign/signature"

var ErrImageSignatureMissing = errors.New("oci: image signature attachment missing")

// ImageSignatureAttachment contains independently content-verified registry
// data. The caller must verify the detached signature and signed image claim;
// registry annotations alone never establish a trusted publisher.
type ImageSignatureAttachment struct {
	ManifestDigest string
	PayloadDigest  string
	Payload        []byte
	Signature      []byte
}

// Additive to Puller: image/rootfs readers cannot be used as signature readers.
// Auth is scoped to the same repository as the resolved image subject.
type ImageSignatureAttachmentPuller interface {
	PullImageSignatureAttachments(context.Context, string, string, *BasicAuth) ([]ImageSignatureAttachment, error)
}

type imageSignatureManifest struct {
	SchemaVersion int        `json:"schemaVersion"`
	MediaType     string     `json:"mediaType"`
	Config        Descriptor `json:"config"`
	Layers        []struct {
		Descriptor
		Annotations map[string]string `json:"annotations"`
	} `json:"layers"`
}

// PullImageSignatureAttachments reads the subject's sha256-<hex>.sig manifest,
// then each simple-signing payload by its OWN descriptor digest. It never asks
// for signature bytes at the image manifest's blob URL. The tag is read once;
// its exact manifest digest accompanies every returned candidate.
func (c *RegistryClient) PullImageSignatureAttachments(ctx context.Context, ref, digest string, auth *BasicAuth) (result []ImageSignatureAttachment, err error) {
	defer func() { err = scrubAuthFromError(err, auth) }()
	r, err := ParseReference(ref)
	if err != nil {
		return nil, err
	}
	if err := validateDigest(digest); err != nil {
		return nil, fmt.Errorf("%w: invalid signature subject digest", ErrImageManifestInvalid)
	}
	if r.Digest != "" && r.Digest != digest {
		return nil, fmt.Errorf("%w: signature subject differs from pinned image", ErrImageManifestInvalid)
	}
	r.Tag, r.Digest = "sha256-"+strings.TrimPrefix(digest, "sha256:")+".sig", ""
	body, ct, err := c.fetchManifestJSONWithAuthLimit(ctx, c.baseURL(r)+"/v2/"+r.Repository+"/manifests/"+r.Tag, auth, api.ImageSignatureMaxManifestBytes)
	if err != nil {
		if errors.Is(err, ErrImageNotFound) {
			return nil, ErrImageSignatureMissing
		}
		return nil, err
	}
	manifest, err := parseImageSignatureManifest(body, ct)
	if err != nil {
		return nil, err
	}
	manifestDigest := imageContentDigest(body)
	for _, layer := range manifest.Layers {
		sig, err := decodeImageSignature(layer.Annotations[imageSignatureAnnotation])
		if err != nil {
			return nil, err
		}
		payload, err := c.readImageSignaturePayload(ctx, r, layer.Descriptor, auth)
		if err != nil {
			return nil, err
		}
		result = append(result, ImageSignatureAttachment{ManifestDigest: manifestDigest, PayloadDigest: layer.Digest, Payload: payload, Signature: sig})
	}
	return result, nil
}

func parseImageSignatureManifest(body []byte, contentType string) (imageSignatureManifest, error) {
	var manifest imageSignatureManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return manifest, fmt.Errorf("%w: invalid signature manifest JSON", ErrImageManifestInvalid)
	}
	mt, _, ctErr := mime.ParseMediaType(contentType)
	if manifest.SchemaVersion != 2 || ctErr != nil || !isImageManifest(mt, "") ||
		(manifest.MediaType != "" && manifest.MediaType != mt) {
		return manifest, fmt.Errorf("%w: unsupported signature manifest format", ErrImageManifestInvalid)
	}
	if err := validateDigest(manifest.Config.Digest); err != nil || manifest.Config.Size < 0 || manifest.Config.Size > api.ImageSignatureMaxManifestBytes {
		return manifest, fmt.Errorf("%w: invalid signature config descriptor", ErrImageManifestInvalid)
	}
	if len(manifest.Layers) == 0 || len(manifest.Layers) > api.ImageSignatureMaxEntries {
		return manifest, fmt.Errorf("%w: signature attachment count outside limit", ErrImageManifestInvalid)
	}
	for _, layer := range manifest.Layers {
		if layer.MediaType != SimpleSigningMediaType || validateDigest(layer.Digest) != nil || layer.Size <= 0 || layer.Size > api.ImageSignatureMaxPayloadBytes {
			return manifest, fmt.Errorf("%w: unsupported signature payload descriptor", ErrImageManifestInvalid)
		}
	}
	return manifest, nil
}

var _ ImageSignatureAttachmentPuller = (*RegistryClient)(nil)

func decodeImageSignature(encoded string) ([]byte, error) {
	if len(encoded) == 0 || len(encoded) > base64.StdEncoding.EncodedLen(api.ImageSignatureMaxDERBytes) {
		return nil, fmt.Errorf("%w: signature annotation absent or exceeds size limit", ErrImageManifestInvalid)
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(sig) == 0 || len(sig) > api.ImageSignatureMaxDERBytes {
		return nil, fmt.Errorf("%w: invalid signature annotation encoding", ErrImageManifestInvalid)
	}
	return sig, nil
}

func (c *RegistryClient) readImageSignaturePayload(ctx context.Context, ref Reference, desc Descriptor, auth *BasicAuth) ([]byte, error) {
	_, rc, err := c.openBlobWithAuth(ctx, ref, desc.Digest, auth)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(rc, api.ImageSignatureMaxPayloadBytes+1))
	if err := errors.Join(readErr, rc.Close()); err != nil {
		return nil, fmt.Errorf("oci: read signature payload: %w", err)
	}
	if int64(len(body)) != desc.Size || imageContentDigest(body) != desc.Digest {
		return nil, fmt.Errorf("%w: signature payload does not match its descriptor", ErrImageManifestInvalid)
	}
	return body, nil
}
