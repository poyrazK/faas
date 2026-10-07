package objectstorage

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// JSON ETags identify mutable metadata, while XML ETags follow the interoperable
// object contract. Recovery must return the same ETag as an S3-facing read.
func (p *GCS) gcsProofObject(ctx context.Context, bucket, key string) (gcsObjectState, error) {
	if _, native := p.store.(*googleGCSStore); !native {
		return p.store.ObjectState(ctx, bucket, key)
	}
	return p.gcsXMLProofObject(ctx, bucket, key, "")
}

func (p *GCS) gcsXMLProofObject(ctx context.Context, bucket, key, generation string) (gcsObjectState, error) {
	q := url.Values{}
	if generation != "" {
		if _, err := gcsGeneration(generation); err != nil {
			return gcsObjectState{}, err
		}
		q.Set("generation", generation)
	}
	response, err := p.gcsStreamRequest(ctx, http.MethodHead, bucket, key, q, nil, nil, 0)
	if err != nil {
		return gcsObjectState{}, err
	}
	defer func() { _ = response.Body.Close() }()
	ack, err := p.verifyWriteAcknowledgment(response.Header)
	if err != nil || response.StatusCode != http.StatusOK || response.Uncompressed || response.ContentLength < 0 || generation != "" && ack.ProviderVersionID != generation {
		return gcsObjectState{}, ErrUnavailable
	}
	version, _ := strconv.ParseInt(ack.ProviderVersionID, 10, 64)
	out := gcsObjectState{Key: key, ETag: ack.ETag, Size: response.ContentLength, Version: version, Metadata: map[string]string{}}
	for name, values := range response.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-goog-meta-") {
			if len(values) != 1 {
				return gcsObjectState{}, ErrUnavailable
			}
			out.Metadata[strings.TrimPrefix(lower, "x-goog-meta-")] = values[0]
		}
	}
	kms := encryptionHeaderValues(response.Header, "X-Goog-Encryption-Kms-Key-Name")
	if len(kms) > 1 || len(kms) == 1 && kms[0] == "" {
		return gcsObjectState{}, ErrUnavailable
	}
	if len(kms) == 1 {
		out.KMSKeyName = kms[0]
	}
	for name := range response.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-goog-encryption-") && lower != "x-goog-encryption-kms-key-name" {
			out.KMSKeyName = "unhandled-customer-key"
		}
	}
	return out, nil
}
