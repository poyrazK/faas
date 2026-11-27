package storagegrant

// adr: 20261117190000

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	gcs "cloud.google.com/go/storage"
)

// GCSSigner signs V4 URLs for one bucket with the control plane's
// credentials (signBlob through the client, or an explicit key).
type GCSSigner struct {
	bucket *gcs.BucketHandle
	// base carries the identity options; nil lets the client detect them.
	base gcs.SignedURLOptions
}

// NewGCSSigner signs for bucket through client's credentials.
func NewGCSSigner(client *gcs.Client, bucket string) (*GCSSigner, error) {
	if client == nil || bucket == "" {
		return nil, errors.New("storagegrant: GCS signer needs a client and a bucket")
	}
	return &GCSSigner{bucket: client.Bucket(bucket)}, nil
}

// NewGCSKeySigner signs with an explicit service account key (tests and
// key-file deployments).
func NewGCSKeySigner(client *gcs.Client, bucket, accessID string, privateKeyPEM []byte) (*GCSSigner, error) {
	s, err := NewGCSSigner(client, bucket)
	if err != nil {
		return nil, err
	}
	if accessID == "" || len(privateKeyPEM) == 0 {
		return nil, errors.New("storagegrant: GCS key signer needs an access id and a key")
	}
	s.base = gcs.SignedURLOptions{GoogleAccessID: accessID, PrivateKey: privateKeyPEM}
	return s, nil
}

// Sign implements Signer.
func (s *GCSSigner) Sign(_ context.Context, r Request, expires time.Time) (Grant, error) {
	if err := r.Validate(); err != nil {
		return Grant{}, err
	}
	switch {
	case r.Op == OpCreateMultipart, r.Op == OpUploadPart, r.Op == OpCompleteMultipart, r.Op == OpAbortMultipart:
		return Grant{}, fmt.Errorf("%w: %s on GCS", ErrUnsupported, r.Op)
	case r.Op == OpReplace && r.ETag != "":
		return Grant{}, fmt.Errorf("%w: an ETag-conditioned replace on GCS", ErrUnsupported)
	}
	headers := map[string]string{}
	query := url.Values{}
	switch r.Op {
	case OpGetGeneration:
		query.Set("generation", itoa(r.Generation))
	case OpCreate:
		headers["x-goog-if-generation-match"] = "0"
	case OpReplace:
		headers["x-goog-if-generation-match"] = itoa(r.Generation)
	}
	if r.Op.Writes() {
		for k, v := range r.Metadata {
			headers["x-goog-meta-"+strings.ToLower(k)] = v
		}
		if r.ContentType != "" {
			headers["content-type"] = r.ContentType
		}
	}
	opts := s.base
	opts.Scheme = gcs.SigningSchemeV4
	opts.Method = method(r.Op)
	opts.Expires = expires
	opts.QueryParameters = query
	// content-type is signed through ContentType, the rest as headers.
	signed := map[string]string{}
	for k, v := range headers {
		if k == "content-type" {
			opts.ContentType = v
			continue
		}
		signed[k] = v
	}
	opts.Headers = sortedHeaderList(signed)
	u, err := s.bucket.SignedURL(r.Key, &opts)
	if err != nil {
		return Grant{}, fmt.Errorf("storagegrant: sign %s %q: %w", r.Op, r.Key, err)
	}
	return Grant{Method: opts.Method, URL: u, Headers: headers, Expires: expires}, nil
}
