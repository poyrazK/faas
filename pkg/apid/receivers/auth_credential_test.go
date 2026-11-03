package receivers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261029202403 — a gateway sends the presented token's SHA-256 and
// apid, which holds the key ring, resolves it; misses, expiry and
// revocation are outcomes, a store failure is an error.
func TestAuthReceiver_AuthenticateCredential(t *testing.T) {
	sum := sha256.Sum256([]byte("fp_live"))
	acct := state.Account{ID: "acct-1", Plan: api.PlanPro}
	key := state.APIKey{ID: "key-1", AccountID: acct.ID, Hash: sum[:], Scopes: []string{api.ScopeAdmin}}
	cases := []struct {
		name    string
		kind    apidpb.CredentialKind
		sum     []byte
		err     error
		outcome apidpb.CredentialOutcome
		code    codes.Code
	}{
		{name: "api key", kind: apidpb.CredentialKind_CREDENTIAL_KIND_API_KEY, sum: sum[:], outcome: apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_OK},
		{name: "oidc bearer", kind: apidpb.CredentialKind_CREDENTIAL_KIND_OIDC_BEARER, sum: sum[:], outcome: apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_OK},
		{name: "unknown", kind: apidpb.CredentialKind_CREDENTIAL_KIND_API_KEY, sum: sum[:], err: state.ErrNotFound, outcome: apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_NOT_FOUND},
		{name: "expired", kind: apidpb.CredentialKind_CREDENTIAL_KIND_API_KEY, sum: sum[:], err: state.ErrAPIKeyExpired, outcome: apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_EXPIRED},
		{name: "revoked", kind: apidpb.CredentialKind_CREDENTIAL_KIND_API_KEY, sum: sum[:], err: state.ErrAPIKeyRevoked, outcome: apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_REVOKED},
		{name: "store failure", kind: apidpb.CredentialKind_CREDENTIAL_KIND_API_KEY, sum: sum[:], err: errors.New("postgres down"), code: codes.Internal},
		{name: "short hash", kind: apidpb.CredentialKind_CREDENTIAL_KIND_API_KEY, sum: sum[:16], code: codes.InvalidArgument},
		{name: "unknown kind", sum: sum[:], code: codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotKey, gotOIDC []byte
			store := &fakeAuthStore{
				lookup: func(hash []byte) (state.Account, state.APIKey, error) {
					gotKey = hash
					return acct, key, tc.err
				},
				oidc: func(hash []byte) (state.Account, state.APIKey, error) {
					gotOIDC = hash
					return acct, key, tc.err
				},
			}
			resp, err := dialAuthBufconn(t, store).AuthenticateCredential(context.Background(), &apidpb.AuthenticateCredentialRequest{Kind: tc.kind, Sha256: tc.sum})
			if tc.code != codes.OK {
				if status.Code(err) != tc.code {
					t.Fatalf("err = %v, want %v", err, tc.code)
				}
				return
			}
			if err != nil || resp.GetOutcome() != tc.outcome {
				t.Fatalf("resp = %v, %v; want %v", resp, err, tc.outcome)
			}
			looked := gotKey
			if tc.kind == apidpb.CredentialKind_CREDENTIAL_KIND_OIDC_BEARER {
				looked = gotOIDC
			}
			if !bytes.Equal(looked, sum[:]) {
				t.Fatalf("store looked up %x, want the presented SHA-256", looked)
			}
			if tc.outcome != apidpb.CredentialOutcome_CREDENTIAL_OUTCOME_OK {
				if len(resp.GetAccountJson()) != 0 || len(resp.GetKeyJson()) != 0 {
					t.Fatal("a refused credential returned its account or key")
				}
				return
			}
			var gotAcct state.Account
			if err := json.Unmarshal(resp.GetAccountJson(), &gotAcct); err != nil || gotAcct.ID != acct.ID || gotAcct.Plan != acct.Plan {
				t.Fatalf("account = %+v, %v", gotAcct, err)
			}
			gotK, err := state.UnmarshalAPIKeyWire(resp.GetKeyJson())
			if err != nil || gotK.ID != key.ID || !bytes.Equal(gotK.Hash, key.Hash) {
				t.Fatalf("key = %+v, %v", gotK, err)
			}
		})
	}
}

// adr: 20261029202403 — MatchCredential answers only for the columns a
// gateway checks, under apid's policy, and never across columns.
func TestAuthReceiver_MatchCredential(t *testing.T) {
	line, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	credhash.SetAccount(&credhash.Policy{Ring: ring, KeyedWrites: true, PlainLookup: true})
	t.Cleanup(func() { credhash.SetAccount(nil) })

	sum := sha256.Sum256([]byte("ck_secret"))
	keyed := state.StoredCredential(state.CredentialColumnConsumerKey, sum[:])
	other := sha256.Sum256([]byte("ck_other"))
	cli := dialAuthBufconn(t, &fakeAuthStore{})
	cases := []struct {
		name   string
		column string
		sum    []byte
		stored []byte
		match  bool
		code   codes.Code
	}{
		{name: "keyed digest", column: string(state.CredentialColumnConsumerKey), sum: sum[:], stored: keyed, match: true},
		{name: "plain hash during the rollout", column: string(state.CredentialColumnConsumerKey), sum: sum[:], stored: sum[:], match: true},
		{name: "another token", column: string(state.CredentialColumnConsumerKey), sum: other[:], stored: keyed},
		{name: "digest of another column", column: string(state.CredentialColumnForkToken), sum: sum[:], stored: keyed},
		{name: "column gateways do not check", column: "api_keys.key_sha256", sum: sum[:], stored: keyed, code: codes.InvalidArgument},
		{name: "short hash", column: string(state.CredentialColumnConsumerKey), sum: sum[:8], stored: keyed, code: codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := cli.MatchCredential(context.Background(), &apidpb.MatchCredentialRequest{Column: tc.column, Sha256: tc.sum, Stored: tc.stored})
			if tc.code != codes.OK {
				if status.Code(err) != tc.code {
					t.Fatalf("err = %v, want %v", err, tc.code)
				}
				return
			}
			if err != nil || resp.GetMatch() != tc.match {
				t.Fatalf("match = %v, %v; want %v", resp.GetMatch(), err, tc.match)
			}
		})
	}
}
