package objectstorage

import (
	"bytes"
	"io"
	"net/http"

	"github.com/aws/smithy-go/middleware"
	"github.com/onebox-faas/faas/pkg/api"
)

// Bound wire bytes before the SDK allocates decoded metadata. Successful object
// GETs stay streaming; their error responses use the metadata budget as well.
type s3ResponseTransport struct {
	base http.RoundTripper
	// unversioned drops the version-ID headers of a backend whose buckets
	// Gregale never versions (BackendVersioningUnsupported): the provider's
	// IDs there name no retained history, and every caller then sees an
	// unversioned S3 bucket.
	unversioned bool
}

// VersionlessProvider is a provider whose backend Gregale never versions
// (BackendVersioningUnsupported): the version IDs it returns name no
// retained history (ADR-20261121150000).
type VersionlessProvider interface {
	Versionless() bool
}

// Versionless reports a BackendVersioningUnsupported backend.
func (p *S3) Versionless() bool { return p != nil && p.unversioned }

// NormalizeProviderResponse applies to a response fetched with a presigned
// request (the S3 gateway's dispatch paths) what s3ResponseTransport applies
// to SDK responses: a versionless backend's version-ID headers are dropped,
// so a gateway write never records a provider version and never latches
// recovery_versions_observed on an unversioned bucket.
func NormalizeProviderResponse(p Provider, response *http.Response) {
	if response == nil || response.Header == nil {
		return
	}
	if v, ok := p.(VersionlessProvider); ok && v.Versionless() {
		dropProviderVersionHeaders(response.Header)
	}
}

func dropProviderVersionHeaders(h http.Header) {
	h.Del("X-Amz-Version-Id")
	h.Del("X-Amz-Copy-Source-Version-Id")
}

type s3MetadataResponseError struct{ versionsObserved bool }

func (s3MetadataResponseError) Error() string { return ErrUnavailable.Error() }
func (s3MetadataResponseError) Unwrap() error { return ErrUnavailable }

func (t s3ResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return response, err
	}
	if response == nil || response.Body == nil {
		return nil, ErrUnavailable
	}
	if t.unversioned {
		dropProviderVersionHeaders(response.Header)
	}
	if middleware.GetOperationName(request.Context()) == "GetObject" && response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer func(body io.Closer) { _ = body.Close() }(response.Body)
	unknown := s3MetadataResponseError{versionsObserved: multipartVersionsObserved(response.Header)}
	// HEAD Content-Length describes the object, not this response body.
	if request.Method != http.MethodHead && response.ContentLength > api.MaxObjectProviderMetadataResponseBytes {
		return nil, unknown
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.MaxObjectProviderMetadataResponseBytes+1))
	if err != nil || int64(len(body)) > api.MaxObjectProviderMetadataResponseBytes {
		return nil, unknown
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}
