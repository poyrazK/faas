package apidgrpc_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeMatchClient struct {
	calls int
	match bool
	err   error
}

func (f *fakeMatchClient) MatchCredential(context.Context, string, []byte, []byte) (bool, error) {
	f.calls++
	return f.match, f.err
}

// adr: 20261029202403 — a gateway asks apid once per (column, stored value,
// token) it has seen match; misses and failures are asked again.
func TestCredentialMatcherRemembersMatchesOnly(t *testing.T) {
	ctx := context.Background()
	sum := sha256.Sum256([]byte("ck_secret"))
	stored := []byte("stored-digest")
	client := &fakeMatchClient{match: true}
	m := apidgrpc.NewCredentialMatcher(client)
	for i := 0; i < 2; i++ {
		if ok, err := m.Match(ctx, "consumer_keys.hashed_secret", stored, sum[:]); !ok || err != nil {
			t.Fatalf("match %d = %v, %v", i, ok, err)
		}
	}
	if client.calls != 1 {
		t.Fatalf("apid asked %d times for one match", client.calls)
	}
	if ok, _ := m.Match(ctx, "app_forks.access_token_hash", stored, sum[:]); !ok || client.calls != 2 {
		t.Fatalf("another column reused a match (calls %d)", client.calls)
	}
	if ok, _ := m.Match(ctx, "consumer_keys.hashed_secret", []byte("rotated"), sum[:]); !ok || client.calls != 3 {
		t.Fatalf("a changed row reused a match (calls %d)", client.calls)
	}

	client.match = false
	other := sha256.Sum256([]byte("ck_wrong"))
	for i := 0; i < 2; i++ {
		if ok, _ := m.Match(ctx, "consumer_keys.hashed_secret", stored, other[:]); ok {
			t.Fatal("a wrong token matched")
		}
	}
	if client.calls != 5 {
		t.Fatalf("misses were remembered (calls %d)", client.calls)
	}

	client.err = errors.New("apid down")
	if ok, err := m.Match(ctx, "consumer_keys.hashed_secret", []byte("new"), sum[:]); ok || err == nil {
		t.Fatalf("an apid failure = %v, %v", ok, err)
	}
	calls := client.calls
	for _, bad := range [][2][]byte{{nil, sum[:]}, {stored, sum[:16]}} {
		if ok, err := m.Match(ctx, "consumer_keys.hashed_secret", bad[0], bad[1]); ok || err != nil {
			t.Fatalf("malformed input = %v, %v", ok, err)
		}
	}
	if client.calls != calls {
		t.Fatal("malformed input reached apid")
	}
}

type fakeResolver struct{ err error }

func (f fakeResolver) AuthenticateCredential(context.Context, apidpb.CredentialKind, []byte) (state.Account, state.APIKey, error) {
	if f.err != nil {
		return state.Account{}, state.APIKey{}, f.err
	}
	return state.Account{ID: "from-apid"}, state.APIKey{}, nil
}

type fakeCredentialStore struct{ calls int }

func (f *fakeCredentialStore) AuthenticateKey(context.Context, []byte) (state.Account, state.APIKey, error) {
	f.calls++
	return state.Account{ID: "from-store"}, state.APIKey{}, nil
}

func (f *fakeCredentialStore) AuthenticateOIDCBearer(context.Context, []byte) (state.Account, state.APIKey, error) {
	f.calls++
	return state.Account{ID: "from-store"}, state.APIKey{}, nil
}

// adr: 20261029202403 — the gateway reads the store only while apid lacks
// the RPC; any other apid failure, and every refusal, fails closed.
func TestCredentialLookupFallsBackOnlyToAnOlderApid(t *testing.T) {
	sum := sha256.Sum256([]byte("fp_token"))
	cases := []struct {
		name    string
		err     error
		account string
		store   int
		wantErr error
	}{
		{name: "apid answers", account: "from-apid"},
		{name: "unknown credential", err: state.ErrNotFound, wantErr: state.ErrNotFound},
		{name: "revoked credential", err: state.ErrAPIKeyRevoked, wantErr: state.ErrAPIKeyRevoked},
		{name: "apid before the RPC", err: fmt.Errorf("apidgrpc: AuthenticateCredential: %w", status.Error(codes.Unimplemented, "unknown method")), account: "from-store", store: 1},
		{name: "apid unreachable", err: fmt.Errorf("apidgrpc: AuthenticateCredential: %w", status.Error(codes.Unavailable, "connection refused"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeCredentialStore{}
			lookup := apidgrpc.CredentialLookup{Apid: fakeResolver{err: tc.err}, Fallback: store}
			for _, resolve := range []func(context.Context, []byte) (state.Account, state.APIKey, error){lookup.AuthenticateKey, lookup.AuthenticateOIDCBearer} {
				acct, _, err := resolve(context.Background(), sum[:])
				if tc.account != "" {
					if err != nil || acct.ID != tc.account {
						t.Fatalf("account = %q, %v; want %q", acct.ID, err, tc.account)
					}
					continue
				}
				if err == nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			}
			if store.calls != 2*tc.store {
				t.Fatalf("store read %d times", store.calls)
			}
		})
	}
}
