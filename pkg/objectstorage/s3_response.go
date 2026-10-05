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
type s3ResponseTransport struct{ base http.RoundTripper }

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
