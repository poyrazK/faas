// adr: 628
package s3gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// checksumlessVersionProvider mirrors the GCS adapter: it can presign a
// version read but not one that promises an S3 checksum.
type checksumlessVersionProvider struct {
	*gatewayTestProvider
	calls []bool
}

func (p *checksumlessVersionProvider) PresignVersionRead(_ context.Context, _, method, _, _ string, checksum bool, _ int64) (objectstorage.SignedRequest, error) {
	p.calls = append(p.calls, checksum)
	if checksum {
		return objectstorage.SignedRequest{}, objectstorage.ErrUnsupported
	}
	return objectstorage.SignedRequest{URL: "https://provider.example/b/k?generation=7", Method: method}, nil
}

// TestVersionReadFallsBackWithoutChecksum pins H9-143: AWS SDKs send
// x-amz-checksum-mode: ENABLED on every GET by default, and a GCS-backed
// version read answered 501 instead of serving the version without a checksum
// as current-version reads (and S3) do.
func TestVersionReadFallsBackWithoutChecksum(t *testing.T) {
	_, _, base := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, func(*http.Request) (*http.Response, error) {
		t.Fatal("presign must not dispatch")
		return nil, nil
	})
	provider := &checksumlessVersionProvider{gatewayTestProvider: base}
	h := &Handler{}
	r := httptest.NewRequest(http.MethodGet, "https://s3.gregale.dev/assets/k?versionId=v1", nil)
	r.Header.Set("X-Amz-Checksum-Mode", "ENABLED")
	signed, err := h.presignGatewayRead(r, requestContext{provider: provider, bucket: state.ObjectBucket{PhysicalName: "phys"}}, "k", "7")
	if err != nil || signed.URL == "" {
		t.Fatalf("presignGatewayRead = %+v, %v; want an unchecksummed version read", signed, err)
	}
	if len(provider.calls) != 2 || !provider.calls[0] || provider.calls[1] {
		t.Fatalf("presign calls (checksum flags) = %v, want [true false]", provider.calls)
	}

	// Without checksum mode there is exactly one plain presign.
	provider.calls = nil
	r.Header.Del("X-Amz-Checksum-Mode")
	if _, err := h.presignGatewayRead(r, requestContext{provider: provider, bucket: state.ObjectBucket{PhysicalName: "phys"}}, "k", "7"); err != nil || len(provider.calls) != 1 || provider.calls[0] {
		t.Fatalf("plain version read err=%v calls=%v", err, provider.calls)
	}

	// Other presign failures are not masked by the fallback.
	failing := &failingVersionProvider{gatewayTestProvider: base}
	r.Header.Set("X-Amz-Checksum-Mode", "ENABLED")
	if _, err := h.presignGatewayRead(r, requestContext{provider: failing, bucket: state.ObjectBucket{PhysicalName: "phys"}}, "k", "7"); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatalf("unavailable provider err = %v, want ErrUnavailable", err)
	}
}

type failingVersionProvider struct{ *gatewayTestProvider }

func (p *failingVersionProvider) PresignVersionRead(context.Context, string, string, string, string, bool, int64) (objectstorage.SignedRequest, error) {
	return objectstorage.SignedRequest{}, objectstorage.ErrUnavailable
}
