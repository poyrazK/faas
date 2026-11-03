package receivers

import (
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/session"
)

// A gateway that holds no session key asks apid to open a dashboard
// cookie (ADR-20261102191500). apid answers with the envelope it sealed;
// a cookie sealed under another key, or garbage, is invalid, not an error.
func TestAuthReceiverVerifySession(t *testing.T) {
	apidKey := make([]byte, 32)
	for i := range apidKey {
		apidKey[i] = byte(i + 1)
	}
	apid, err := session.NewManager(apidKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	other, err := session.NewEphemeralManager(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	client := dialAuthBufconnWith(t, &fakeAuthStore{}, nil, apid)

	cookie, err := apid.IssueWithSession("sid-1", "acct-1", true)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.VerifySession(t.Context(), &apidpb.VerifySessionRequest{Cookie: cookie})
	if err != nil || !resp.GetValid() {
		t.Fatalf("apid's own cookie = %+v, %v; want valid", resp, err)
	}
	var env session.Envelope
	if err := json.Unmarshal(resp.GetEnvelope(), &env); err != nil {
		t.Fatal(err)
	}
	if env.AccountID != "acct-1" || env.Sid != "sid-1" || !env.MfaPending {
		t.Fatalf("envelope = %+v; want acct-1, sid-1, mfa pending", env)
	}

	forged, err := other.IssueWithSession("sid-1", "acct-1", false)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"another key": forged, "garbage": "not-a-cookie"} {
		resp, err := client.VerifySession(t.Context(), &apidpb.VerifySessionRequest{Cookie: value})
		if err != nil || resp.GetValid() || len(resp.GetEnvelope()) != 0 {
			t.Fatalf("%s = %+v, %v; want invalid with no envelope", name, resp, err)
		}
	}

	if _, err := client.VerifySession(t.Context(), &apidpb.VerifySessionRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty cookie = %v; want InvalidArgument", err)
	}
	unwired := dialAuthBufconn(t, &fakeAuthStore{})
	if _, err := unwired.VerifySession(t.Context(), &apidpb.VerifySessionRequest{Cookie: cookie}); status.Code(err) != codes.Unavailable {
		t.Fatalf("no session manager = %v; want Unavailable", err)
	}
}
