package imaged

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
)

func init() {
	// Tests that fail verification on purpose must not wait out the
	// production retry backoff.
	externalVerifyRetryDelays = []time.Duration{0, 0, 0}
}

func TestTransientSignatureError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"missing signature", fmt.Errorf("verify: %w", cosign.ErrSignatureMissing), false},
		{"untrusted signature", fmt.Errorf("verify: %w", cosign.ErrSignatureInvalid), false},
		{"cancelled", context.Canceled, false},
		{"registry error", errors.New("registry returned 503"), true},
		{"publisher read", fmt.Errorf("read: %w", errTrustedPublishersUnavailable), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := transientSignatureError(tc.err); got != tc.want {
				t.Fatalf("transientSignatureError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestSignatureProblemExplainsTheFix(t *testing.T) {
	for _, untrusted := range []bool{false, true} {
		p := signatureProblem("shop", "registry.example/shop@sha256:abc", untrusted)
		if p.Code != api.CodeDeploySignatureInvalid || p.Hint == "" || p.Detail == "" {
			t.Fatalf("signatureProblem(untrusted=%v) = %+v", untrusted, p)
		}
	}
}
