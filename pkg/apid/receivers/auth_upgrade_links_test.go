package receivers

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeUpgradeLinks struct {
	url     string
	expires time.Time
	err     error
}

func (f fakeUpgradeLinks) UpgradeLink(context.Context, string, string, string) (string, time.Time, error) {
	return f.url, f.expires, f.err
}

// ADR-20261124094745: the Auth service mints a consumer's upgrade link for
// a gateway; an app without upgrades answers an empty url.
func TestAuthReceiver_ConsumerUpgradeLink(t *testing.T) {
	req := &apidpb.ConsumerUpgradeLinkRequest{AccountId: "acct", AppId: "app", ConsumerId: "c1"}
	if _, err := newAuthReceiver(nil, nil, nil).ConsumerUpgradeLink(t.Context(), req); status.Code(err) != codes.Unavailable {
		t.Fatalf("without links = %v; want Unavailable", err)
	}
	expires := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	r := newAuthReceiver(nil, nil, nil, WithUpgradeLinks(fakeUpgradeLinks{url: "https://x/upgrade?token=t", expires: expires}))
	if _, err := r.ConsumerUpgradeLink(t.Context(), &apidpb.ConsumerUpgradeLinkRequest{AccountId: "acct"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing fields = %v; want InvalidArgument", err)
	}
	resp, err := r.ConsumerUpgradeLink(t.Context(), req)
	if err != nil || resp.GetUrl() != "https://x/upgrade?token=t" || resp.GetExpiresAtUnixMs() != expires.UnixMilli() {
		t.Fatalf("link = %+v %v", resp, err)
	}
	none := newAuthReceiver(nil, nil, nil, WithUpgradeLinks(fakeUpgradeLinks{}))
	if resp, err := none.ConsumerUpgradeLink(t.Context(), req); err != nil || resp.GetUrl() != "" || resp.GetExpiresAtUnixMs() != 0 {
		t.Fatalf("no upgrade = %+v %v", resp, err)
	}
	for cause, want := range map[error]codes.Code{state.ErrNotFound: codes.NotFound, errors.New("db down"): codes.Unavailable} {
		r := newAuthReceiver(nil, nil, nil, WithUpgradeLinks(fakeUpgradeLinks{err: cause}))
		if _, err := r.ConsumerUpgradeLink(t.Context(), req); status.Code(err) != want {
			t.Errorf("%v = %v; want %v", cause, err, want)
		}
	}
}
