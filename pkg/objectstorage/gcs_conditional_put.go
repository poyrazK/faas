package objectstorage

// adr: 732

import (
	"context"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ConditionalObjectPresigner = (*GCS)(nil)

type conditionalWriteRequestRecorderKey struct{}

// WithConditionalWriteRequestRecorder admits and meters the native observation
// needed to translate an XML ETag into an atomic GCS content-generation fence.
// The observation is separate from the already tracked destination PUT.
func WithConditionalWriteRequestRecorder(ctx context.Context, before func(context.Context) error) context.Context {
	return context.WithValue(ctx, conditionalWriteRequestRecorderKey{}, before)
}

func (p *GCS) PresignConditionalPut(ctx context.Context, bucket string, r SignRequest, c ObjectWriteConditions) (SignedRequest, error) {
	return p.presignGCSConditionalPut(ctx, bucket, r, c, nil)
}

func (p *GCS) presignGCSConditionalPut(ctx context.Context, bucket string, r SignRequest, c ObjectWriteConditions, private http.Header) (SignedRequest, error) {
	if err := ctx.Err(); err != nil {
		return SignedRequest{}, err
	}
	if r.Method != http.MethodPut || r.Validate(api.MaxObjectSinglePutBytes) != nil || !c.Valid() || bucket == "" {
		return SignedRequest{}, ErrInvalid
	}
	if r.Encryption != nil || r.Protection != nil {
		return SignedRequest{}, ErrUnsupported
	}
	c, err := signWriteConditions(r, c)
	if err != nil {
		return SignedRequest{}, err
	}
	headers, err := p.gcsWriteConditionHeaders(ctx, bucket, r.Key, c)
	if err != nil {
		return SignedRequest{}, err
	}
	for name, values := range private {
		headers[name] = append([]string(nil), values...)
	}
	return p.presignGCS(ctx, bucket, r, headers)
}

// GCS XML If-Match is a read predicate, not a PUT predicate. Bind a checked
// XML ETag to its content generation, then sign the provider's atomic PUT
// precondition. A writer that races the observation cannot be overwritten.
func (p *GCS) gcsWriteConditionHeaders(ctx context.Context, bucket, key string, c ObjectWriteConditions) (http.Header, error) {
	headers := make(http.Header)
	if c.IfNoneMatch == "*" {
		headers.Set("X-Goog-If-Generation-Match", "0")
	}
	if c.IfMatch == "" {
		return headers, nil
	}
	if !validCopyETagCondition(c.IfMatch) {
		return nil, ErrUnsupported
	}
	before, ok := ctx.Value(conditionalWriteRequestRecorderKey{}).(func(context.Context) error)
	if !ok || before == nil {
		return nil, ErrConfiguration
	}
	if err := before(ctx); err != nil {
		return nil, err
	}
	object, err := p.gcsXMLProofObject(ctx, bucket, key, "")
	if err != nil {
		return nil, err
	}
	if object.ETag == "*" || !validCopyETagCondition(object.ETag) {
		return nil, ErrUnavailable
	}
	if c.IfMatch != "*" && c.IfMatch != object.ETag {
		return nil, ErrPreconditionFailed
	}
	headers.Set("X-Goog-If-Generation-Match", strconv.FormatInt(object.Version, 10))
	return headers, nil
}
