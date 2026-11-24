package receivers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeOAuthConsumers struct {
	in       state.APIConsumerEnrollment
	consumer state.APIConsumer
	created  bool
	err      error
}

func (f *fakeOAuthConsumers) EnrollOAuthConsumer(_ context.Context, in state.APIConsumerEnrollment) (state.APIConsumer, bool, error) {
	f.in = in
	return f.consumer, f.created, f.err
}

// ADR-20261124094745: the Auth service enrolls a gateway's verified OAuth
// caller through apid, reports a revoked consumer inactive, and maps each
// refusal to its own code so the gateway can answer it.
func TestAuthReceiver_EnrollOAuthConsumer(t *testing.T) {
	req := &apidpb.EnrollOAuthConsumerRequest{AccountId: "acct", AppId: "app", Issuer: "https://login.example", Subject: "u1", Email: "a@example.com"}
	if _, err := newAuthReceiver(nil, nil, nil).EnrollOAuthConsumer(t.Context(), req); status.Code(err) != codes.Unavailable {
		t.Fatalf("without consumers = %v; want Unavailable", err)
	}
	fake := &fakeOAuthConsumers{consumer: state.APIConsumer{ID: "c1", Status: state.APIConsumerStatusActive}, created: true}
	r := newAuthReceiver(nil, nil, nil, WithOAuthConsumers(fake))
	for name, bad := range map[string]*apidpb.EnrollOAuthConsumerRequest{
		"no subject":   {AccountId: "acct", AppId: "app", Issuer: "https://login.example"},
		"long subject": {AccountId: "acct", AppId: "app", Issuer: "https://login.example", Subject: strings.Repeat("s", state.MaxAPIConsumerIdentitySubjectLen+1)},
		"bad utf8":     {AccountId: "acct", AppId: "app", Issuer: "https://login.example", Subject: "\xff"},
	} {
		if _, err := r.EnrollOAuthConsumer(t.Context(), bad); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s = %v; want InvalidArgument", name, err)
		}
	}
	resp, err := r.EnrollOAuthConsumer(t.Context(), req)
	if err != nil || resp.GetConsumerId() != "c1" || !resp.GetActive() || !resp.GetCreated() {
		t.Fatalf("enroll = %+v %v", resp, err)
	}
	if fake.in.AppID != "app" || fake.in.Issuer != req.Issuer || fake.in.Subject != "u1" || fake.in.Email != "a@example.com" {
		t.Errorf("enrollment = %+v", fake.in)
	}
	revoked := time.Now()
	*fake = fakeOAuthConsumers{consumer: state.APIConsumer{ID: "c1", Status: state.APIConsumerStatusActive, RevokedAt: &revoked}}
	if resp, err := r.EnrollOAuthConsumer(t.Context(), req); err != nil || resp.GetActive() {
		t.Fatalf("revoked = %+v %v; want inactive", resp, err)
	}
	for cause, want := range map[error]codes.Code{
		state.ErrAPIConsumerOAuthOff:    codes.FailedPrecondition,
		state.ErrAPIConsumerSignupLimit: codes.ResourceExhausted,
		state.ErrNotFound:               codes.NotFound,
		errors.New("db down"):           codes.Unavailable,
	} {
		*fake = fakeOAuthConsumers{err: cause}
		if _, err := r.EnrollOAuthConsumer(t.Context(), req); status.Code(err) != want {
			t.Errorf("%v = %v; want %v", cause, err, want)
		}
	}
}
