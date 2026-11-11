package receivers

import (
	"context"
	"crypto/sha256"
	"testing"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261029202403 amendment 1 decision 2 — apid matches each column a
// compute-node daemon asks about under that column's own family ring, and a
// digest under one family's ring does not match under another's.
func TestAuthReceiver_MatchCredentialUsesTheColumnsFamily(t *testing.T) {
	policies := map[credhash.Family]*credhash.Policy{}
	for i, family := range []credhash.Family{credhash.FamilyScheduler, credhash.FamilyDevBridge, credhash.FamilyDataAPI} {
		line, err := credhash.NewKeyLine(i + 1)
		if err != nil {
			t.Fatal(err)
		}
		ring, err := credhash.ParseRing([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		policies[family] = &credhash.Policy{Ring: ring, KeyedWrites: true}
		credhash.Set(family, policies[family])
	}
	t.Cleanup(func() {
		for family := range policies {
			credhash.Set(family, nil)
		}
	})
	cli := dialAuthBufconn(t, &fakeAuthStore{})
	sum := sha256.Sum256([]byte("token"))
	for _, tc := range []struct {
		column string
		family credhash.Family
	}{
		{string(state.CredentialColumnOperationCapability), credhash.FamilyScheduler},
		{string(state.CredentialColumnDevBridgeAttachment), credhash.FamilyDevBridge},
		{string(state.CredentialColumnDevBridgeRequest), credhash.FamilyDevBridge},
		{api.DataAPIServerKeyCredentialTag, credhash.FamilyDataAPI},
	} {
		stored := policies[tc.family].Store(credhash.Tag(tc.column), sum[:])
		resp, err := cli.MatchCredential(context.Background(), &apidpb.MatchCredentialRequest{Column: tc.column, Sha256: sum[:], Stored: stored})
		if err != nil || !resp.GetMatch() {
			t.Fatalf("%s: match = %v, %v", tc.column, resp.GetMatch(), err)
		}
		for family, other := range policies {
			if family == tc.family {
				continue
			}
			foreign := other.Store(credhash.Tag(tc.column), sum[:])
			resp, err := cli.MatchCredential(context.Background(), &apidpb.MatchCredentialRequest{Column: tc.column, Sha256: sum[:], Stored: foreign})
			if err != nil || resp.GetMatch() {
				t.Fatalf("%s matched a digest under the %s ring", tc.column, family)
			}
		}
	}
}
