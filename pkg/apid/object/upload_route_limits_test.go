package object

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// adr: 553 — a streaming route is one PUT, even when multipart objects may
// be larger. Publish a usable default and refuse impossible route policies.
func TestUploadRoutePolicyUsesSinglePutLimit(t *testing.T) {
	for _, size := range []int64{0, 4, 5} {
		st, registry, bucket, _ := receiptRecoveryFixture(t, "http://127.0.0.1:9")
		registry.MaxSinglePutBytes = 4
		svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
		route, _, err := svc.configureObjectUploadRoute(t.Context(), bucket, st, api.CreateObjectUploadRouteRequest{Name: "files", MaxBytes: size})
		if size > 4 {
			if !errors.Is(err, objectstorage.ErrInvalid) {
				t.Fatalf("oversized policy = %+v, %v", route, err)
			}
		} else if err != nil || route.MaxBytes != 4 {
			t.Fatalf("policy for %d = %+v, %v", size, route, err)
		}
	}
}
