package webhooksig_test

import (
	"testing"
	"time"

	stripe "github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/billing/stripe/webhooksig"
)

func TestHeaderMatchesStripeSchemeAndVerifier(t *testing.T) {
	payload := []byte(`{"id":"evt_1"}`)
	// HMAC-SHA256("whsec_test", "1700000000.{\"id\":\"evt_1\"}"), computed
	// outside Go.
	want := "t=1700000000,v1=c89214b5b5da833daed6f0b8c5bb6bd58cea9022bd80ccc78230f3942d632925"
	if got := webhooksig.Header(payload, "whsec_test", time.Unix(1700000000, 0)); got != want {
		t.Fatalf("Header = %q, want %q", got, want)
	}

	now := time.Now()
	header := webhooksig.Header(payload, "whsec_test", now)
	if err := stripe.VerifySignature(payload, header, "whsec_test", time.Minute); err != nil {
		t.Fatalf("verifier rejected a signed payload: %v", err)
	}
	for name, tc := range map[string]struct {
		payload []byte
		secret  string
	}{
		"payload": {[]byte(`{"id":"evt_2"}`), "whsec_test"},
		"secret":  {payload, "whsec_other"},
	} {
		if err := stripe.VerifySignature(tc.payload, header, tc.secret, time.Minute); err == nil {
			t.Errorf("verifier accepted a header signed over a different %s", name)
		}
	}
}
