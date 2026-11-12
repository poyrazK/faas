package objectstorage

import (
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A signed URL lifetime past the limit names the limit and the requested
// value instead of a bare invalid request (11-04 prod test).
func TestNormalizePublicSignRequestNamesTheLifetimeLimit(t *testing.T) {
	_, err := NormalizePublicSignRequest(SignRequest{Method: http.MethodGet, Key: "k", ExpiresIn: api.MaxObjectSignedURLExpiresSeconds + 100}, 1<<20)
	var limit *state.ObjectStorageLimitError
	if !errors.As(err, &limit) || !errors.Is(err, ErrInvalid) || limit.Limit != api.MaxObjectSignedURLExpiresSeconds || limit.Observed != api.MaxObjectSignedURLExpiresSeconds+100 {
		t.Fatalf("err = %v", err)
	}
	if _, err = NormalizePublicSignRequest(SignRequest{Method: http.MethodGet, Key: "k", ExpiresIn: api.MaxObjectSignedURLExpiresSeconds}, 1<<20); err != nil {
		t.Fatalf("the longest allowed lifetime was refused: %v", err)
	}
}
