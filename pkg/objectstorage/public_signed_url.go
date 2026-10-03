package objectstorage

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/net/http/httpguts"
)

// NormalizePublicSignRequest freezes defaults and canonical metadata before
// its authority is persisted. Public URLs never contain native placement.
func NormalizePublicSignRequest(r SignRequest, maxBytes int64) (SignRequest, error) {
	if err := r.Validate(maxBytes); err != nil {
		return r, err
	}
	if r.ExpiresIn == 0 {
		r.ExpiresIn = int64(api.DefaultObjectSignedURLTTL / time.Second)
	}
	if r.Method == http.MethodPut && r.ContentType == "" {
		r.ContentType = "application/octet-stream"
	}
	metadata := make(map[string]string, len(r.Metadata))
	for key, value := range r.Metadata {
		lower := strings.ToLower(key)
		if _, exists := metadata[lower]; exists {
			return r, ErrInvalid
		}
		metadata[lower] = value
	}
	r.Metadata = metadata
	data, err := json.Marshal(r)
	if err != nil || len(data)+2*bytes.Count(data, []byte("\":")) > api.MaxObjectURLRequestBytes {
		return r, ErrInvalid
	}
	return r, nil
}

// PublicSignedObjectHeaders is also used by the gateway to reject unsigned
// metadata, copy directives and other additions to a URL's fixed request.
func PublicSignedObjectHeaders(r SignRequest) (http.Header, error) {
	if err := r.Validate(api.MaxObjectSinglePutBytes); err != nil {
		return nil, err
	}
	h := http.Header{}
	if r.Method != http.MethodPut {
		return h, nil
	}
	for name, value := range map[string]string{"Content-Type": r.ContentType, "Cache-Control": r.CacheControl, "Content-Disposition": r.ContentDisposition, "Content-Encoding": r.ContentEncoding, "Content-Language": r.ContentLanguage} {
		if value != "" {
			h.Set(name, value)
		}
	}
	for name, value := range r.Metadata {
		if !httpguts.ValidHeaderFieldName("X-Amz-Meta-"+name) || !httpguts.ValidHeaderFieldValue(value) {
			return nil, ErrInvalid
		}
		h.Set("X-Amz-Meta-"+name, value)
	}
	tags, err := EncodeObjectTags(r.Tags)
	if err != nil {
		return nil, err
	}
	if tags != "" {
		h.Set("X-Amz-Tagging", tags)
	}
	if r.Encryption != nil {
		e := r.Encryption
		h.Set("X-Amz-Server-Side-Encryption", e.Algorithm)
		if e.KeyID != "" {
			h.Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", e.KeyID)
		}
		if e.Context != "" {
			h.Set("X-Amz-Server-Side-Encryption-Context", e.Context)
		}
		if e.BucketKeyEnabled != nil {
			h.Set("X-Amz-Server-Side-Encryption-Bucket-Key-Enabled", strconv.FormatBool(*e.BucketKeyEnabled))
		}
	}
	return h, nil
}

// PresignPublicObject performs only local signing with a sealed ephemeral
// credential. The caller must commit its capability before returning the URL.
func PresignPublicObject(ctx context.Context, endpoint, region, bucket, access, secret string, r SignRequest, now time.Time) (SignedRequest, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" && u.Path != "/" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || region == "" || bucket == "" || strings.ContainsAny(bucket, "/\\") || access == "" || secret == "" {
		return SignedRequest{}, ErrConfiguration
	}
	h, err := PublicSignedObjectHeaders(r)
	if err != nil || r.ExpiresIn < 1 {
		return SignedRequest{}, ErrInvalid
	}
	u.Path = "/" + bucket + "/" + r.Key
	query := url.Values{"X-Amz-Expires": []string{strconv.FormatInt(r.ExpiresIn, 10)}}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), nil)
	if err != nil {
		return SignedRequest{}, ErrInvalid
	}
	req.Header = h
	if r.SizeBytes != nil {
		req.ContentLength = *r.SizeBytes
	}
	signer := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true; o.DisableHeaderHoisting = true })
	signed, _, err := signer.PresignHTTP(ctx, aws.Credentials{AccessKeyID: access, SecretAccessKey: secret}, req, "UNSIGNED-PAYLOAD", "s3", region, now)
	if err != nil {
		return SignedRequest{}, ErrUnavailable
	}
	out := SignedRequest{URL: signed, Method: r.Method, Headers: map[string]string{}, ExpiresAt: now.UTC().Truncate(time.Second).Add(time.Duration(r.ExpiresIn) * time.Second)}
	for name, values := range h {
		out.Headers[name] = strings.Join(values, ",")
	}
	if r.SizeBytes != nil {
		out.Headers["Content-Length"] = strconv.FormatInt(*r.SizeBytes, 10)
	}
	return out, nil
}
