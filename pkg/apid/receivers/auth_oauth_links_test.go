package receivers

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeOAuthLinks struct {
	got     state.APIConsumerIdentityLink
	key     string
	revoked string
	err     error
}

func (f *fakeOAuthLinks) LinkOAuthConsumer(_ context.Context, in state.APIConsumerIdentityLink, key string) (state.APIConsumerIdentity, string, error) {
	f.got, f.key = in, key
	if f.err != nil {
		return state.APIConsumerIdentity{}, "", f.err
	}
	return state.APIConsumerIdentity{ID: "i1", ConsumerID: "keyed"}, f.revoked, nil
}

// ADR-20261124094745: a gateway links a verified OAuth caller to the
// consumer whose key the caller proved; apid checks the key.
func TestAuthReceiver_LinkOAuthConsumer(t *testing.T) {
	req := &apidpb.LinkOAuthConsumerRequest{AccountId: "acct", AppId: "app", Issuer: "https://login.example", Subject: "u1",
		Email: "ada@example.com", Key: "ck_x"}
	if _, err := newAuthReceiver(nil, nil, nil).LinkOAuthConsumer(t.Context(), req); status.Code(err) != codes.Unavailable {
		t.Fatalf("without links = %v; want Unavailable", err)
	}
	links := &fakeOAuthLinks{revoked: "enrolled"}
	r := newAuthReceiver(nil, nil, nil, WithOAuthLinks(links))
	if _, err := r.LinkOAuthConsumer(t.Context(), &apidpb.LinkOAuthConsumerRequest{AccountId: "acct", AppId: "app", Issuer: "i",
		Subject: "s"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no key = %v; want InvalidArgument", err)
	}
	resp, err := r.LinkOAuthConsumer(t.Context(), req)
	if err != nil || resp.GetConsumerId() != "keyed" || resp.GetRevokedConsumerId() != "enrolled" {
		t.Fatalf("link = %+v %v", resp, err)
	}
	if links.key != "ck_x" || links.got.Subject != "u1" || links.got.Email != "ada@example.com" || links.got.ConsumerID != "" {
		t.Fatalf("passed %+v %q", links.got, links.key)
	}
	for cause, want := range map[error]codes.Code{
		state.ErrAPIConsumerKeyRejected: codes.PermissionDenied,
		state.ErrAPIConsumerOAuthOff:    codes.FailedPrecondition,
		state.ErrConflict:               codes.AlreadyExists,
		state.ErrNotFound:               codes.NotFound,
		errors.New("db down"):           codes.Unavailable,
	} {
		r := newAuthReceiver(nil, nil, nil, WithOAuthLinks(&fakeOAuthLinks{err: cause}))
		if _, err := r.LinkOAuthConsumer(t.Context(), req); status.Code(err) != want {
			t.Errorf("%v = %v; want %v", cause, err, want)
		}
	}
}
