package secretd_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"filippo.io/age"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretd"
)

// leaseStore adds verification leases to the shared fake.
type leaseStore struct {
	*fakeStore
	leases map[string]secretd.VerificationLease
}

func (s *leaseStore) VerificationLease(_ context.Context, deploymentID string) (secretd.VerificationLease, error) {
	lease, ok := s.leases[deploymentID]
	if !ok {
		return secretd.VerificationLease{}, secretd.ErrUnknownVerificationLease
	}
	return lease, nil
}

const leaseContract = `{"version":1,"checks":[{"name":"me","http":{"path":"/me","headers":{"Authorization":"Bearer ${secret:API_TOKEN}","X-Tenant":"${secret:TENANT}"}}}]}`

// ADR-20261122220000 amendment 2: the node holding a deployment's
// verification lease opens exactly the secrets the app's contract references,
// from the deployment's scope, while the deployment is still a candidate.
func TestOpenVerificationSecrets(t *testing.T) {
	f := newFixture(t, 0)
	f.store.rows = map[rowKey][]byte{
		{"app-a", "production", "API_TOKEN"}: f.seal(t, "acct-a", secretbox.Envelope{"API_TOKEN": "tok-prod"}),
		{"app-a", "production", "DB_URL"}:    f.seal(t, "acct-a", secretbox.Envelope{"DB_URL": "postgres://x"}),
		{"app-a", "preview", "API_TOKEN"}:    f.seal(t, "acct-a", secretbox.Envelope{"API_TOKEN": "tok-preview"}),
	}
	until := f.now.Add(time.Minute)
	store := &leaseStore{fakeStore: f.store, leases: map[string]secretd.VerificationLease{
		"dep-1":    {AppID: "app-a", NodeName: "other-node", ExpiresAt: until, Status: "snapshotting", Scope: "production", Contract: []byte(leaseContract)},
		"dep-prev": {AppID: "app-a", NodeName: "other-node", ExpiresAt: until, Status: "imaging", Scope: "preview", Contract: []byte(leaseContract)},
		"dep-old":  {AppID: "app-a", NodeName: "other-node", ExpiresAt: f.now, Status: "snapshotting", Scope: "production", Contract: []byte(leaseContract)},
		"dep-live": {AppID: "app-a", NodeName: "other-node", ExpiresAt: until, Status: "live", Scope: "production", Contract: []byte(leaseContract)},
		"dep-none": {AppID: "app-a", NodeName: "other-node", ExpiresAt: until, Status: "snapshotting", Scope: "production"},
		// ADR-20261127191645: the MCP listing's probe secret opens without a
		// verification contract; other keys still need one.
		"dep-probe": {AppID: "app-a", NodeName: "other-node", ExpiresAt: until, Status: "snapshotting", Scope: "production", ProbeSecret: "API_TOKEN"},
	}}
	server := secretd.NewServer(store, []*age.X25519Identity{f.id}, 0, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithPeerNode(func(context.Context) (string, error) { return f.caller, nil }).
		WithClock(func() time.Time { return f.now })
	open := func(app, dep string, keys ...string) (*secretdpb.OpenVerificationSecretsResponse, codes.Code) {
		resp, err := server.OpenVerificationSecrets(f.ctx, &secretdpb.OpenVerificationSecretsRequest{AppId: app, DeploymentId: dep, Keys: keys})
		return resp, status.Code(err)
	}

	// The lease holder is not the app's owner and holds no grant.
	f.caller = "other-node"
	resp, code := open("app-a", "dep-1", "API_TOKEN", "TENANT")
	if code != codes.OK || resp.GetValues()["API_TOKEN"] != "tok-prod" || len(resp.GetValues()) != 1 {
		t.Fatalf("lease holder = %v, %v", resp.GetValues(), code)
	}
	if got := resp.GetMissing(); len(got) != 1 || got[0] != "TENANT" {
		t.Fatalf("missing = %v, want [TENANT]", got)
	}
	if f.store.opens[len(f.store.opens)-1] != "n-other/app-a/grant" {
		t.Fatalf("audit = %v", f.store.opens)
	}
	if resp, code := open("app-a", "dep-prev", "API_TOKEN"); code != codes.OK || resp.GetValues()["API_TOKEN"] != "tok-preview" {
		t.Fatalf("preview scope = %v, %v", resp.GetValues(), code)
	}
	if resp, code := open("app-a", "dep-probe", "API_TOKEN"); code != codes.OK || resp.GetValues()["API_TOKEN"] != "tok-prod" {
		t.Fatalf("MCP probe secret = %v, %v", resp.GetValues(), code)
	}

	for _, tc := range []struct {
		name, caller, app, dep string
		keys                   []string
		want                   codes.Code
	}{
		{"no keys", "other-node", "app-a", "dep-1", nil, codes.InvalidArgument},
		{"duplicate keys", "other-node", "app-a", "dep-1", []string{"API_TOKEN", "API_TOKEN"}, codes.InvalidArgument},
		{"key the contract does not reference", "other-node", "app-a", "dep-1", []string{"DB_URL"}, codes.PermissionDenied},
		{"another node", "owner-node", "app-a", "dep-1", []string{"API_TOKEN"}, codes.PermissionDenied},
		{"another app", "other-node", "app-b", "dep-1", []string{"API_TOKEN"}, codes.PermissionDenied},
		{"no lease", "other-node", "app-a", "dep-x", []string{"API_TOKEN"}, codes.PermissionDenied},
		{"expired lease", "other-node", "app-a", "dep-old", []string{"API_TOKEN"}, codes.PermissionDenied},
		{"live deployment", "other-node", "app-a", "dep-live", []string{"API_TOKEN"}, codes.PermissionDenied},
		{"no contract", "other-node", "app-a", "dep-none", []string{"API_TOKEN"}, codes.PermissionDenied},
		{"key beside the probe secret", "other-node", "app-a", "dep-probe", []string{"API_TOKEN", "DB_URL"}, codes.PermissionDenied},
		{"unknown node", "stranger", "app-a", "dep-1", []string{"API_TOKEN"}, codes.PermissionDenied},
	} {
		f.caller = tc.caller
		if _, code := open(tc.app, tc.dep, tc.keys...); code != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, code, tc.want)
		}
	}

	// A secretd whose store cannot read leases refuses.
	f.caller = "other-node"
	if _, err := f.server.OpenVerificationSecrets(f.ctx, &secretdpb.OpenVerificationSecretsRequest{AppId: "app-a", DeploymentId: "dep-1", Keys: []string{"API_TOKEN"}}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("store without leases = %v", status.Code(err))
	}
}
