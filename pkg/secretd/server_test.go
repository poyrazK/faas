// adr: 20261110210000
package secretd_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"filippo.io/age"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/crashcrypt"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretd"
)

type grantKey struct{ app, node string }

type fakeStore struct {
	nodes    map[string]string // name -> id (active only)
	apps     map[string]secretd.AppTarget
	grants   map[grantKey]time.Time
	opens    []string
	failRec  bool
	captures map[string]fakeCapture
}

type fakeCapture struct {
	app    string
	sealed []byte
}

func (f *fakeStore) CaptureKey(_ context.Context, id string) (string, []byte, error) {
	c, ok := f.captures[id]
	if !ok {
		return "", nil, secretd.ErrUnknownCapture
	}
	return c.app, c.sealed, nil
}

func (f *fakeStore) NodeIDByName(_ context.Context, name string) (string, error) {
	if id, ok := f.nodes[name]; ok {
		return id, nil
	}
	return "", secretd.ErrUnknownNode
}

func (f *fakeStore) AppTarget(_ context.Context, appID string) (secretd.AppTarget, error) {
	if t, ok := f.apps[appID]; ok {
		return t, nil
	}
	return secretd.AppTarget{}, errors.New("no app")
}

func (f *fakeStore) GrantValid(_ context.Context, appID, nodeID string, at time.Time) (bool, error) {
	exp, ok := f.grants[grantKey{appID, nodeID}]
	return ok && exp.After(at), nil
}

func (f *fakeStore) UpsertGrant(_ context.Context, appID, nodeID, _ string, expiresAt time.Time) (time.Time, error) {
	f.grants[grantKey{appID, nodeID}] = expiresAt
	return expiresAt, nil
}

func (f *fakeStore) RecordOpen(_ context.Context, nodeID, appID, via string, _ int) error {
	if f.failRec {
		return errors.New("db down")
	}
	f.opens = append(f.opens, nodeID+"/"+appID+"/"+via)
	return nil
}

type fixture struct {
	store  *fakeStore
	id     *age.X25519Identity
	caller string
	now    time.Time
	server *secretd.Server
	ctx    context.Context
	seal   func(t *testing.T, account string, env secretbox.Envelope) []byte
}

func newFixture(t *testing.T, budget int) *fixture {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		store: &fakeStore{
			nodes: map[string]string{"owner-node": "n-owner", "peer-node": "n-peer", "other-node": "n-other"},
			apps: map[string]secretd.AppTarget{
				"app-a": {OwnerNodeID: "n-owner", AccountID: "acct-a"},
				"app-b": {OwnerNodeID: "n-other", AccountID: "acct-b"},
			},
			grants: map[grantKey]time.Time{},
		},
		id:     id,
		caller: "owner-node",
		now:    time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC),
		ctx:    context.Background(),
	}
	f.server = secretd.NewServer(f.store, []*age.X25519Identity{id}, budget, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithPeerNode(func(context.Context) (string, error) { return f.caller, nil }).
		WithClock(func() time.Time { return f.now })
	f.seal = func(t *testing.T, account string, env secretbox.Envelope) []byte {
		t.Helper()
		blob, err := secretbox.SealBound([]*age.X25519Recipient{id.Recipient()}, secretbox.Binding{AccountID: account}, env)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	return f
}

func (f *fixture) open(t *testing.T, app string, entries ...*secretdpb.SealedEntry) (map[string]string, codes.Code) {
	t.Helper()
	resp, err := f.server.OpenAppSecrets(f.ctx, &secretdpb.OpenAppSecretsRequest{AppId: app, Entries: entries})
	if err != nil {
		return nil, status.Code(err)
	}
	return resp.GetValues(), codes.OK
}

func TestOwnerOpensItsAppAndTheOpenIsRecorded(t *testing.T) {
	f := newFixture(t, 0)
	values, code := f.open(t, "app-a", &secretdpb.SealedEntry{Key: "DB_URL", Ciphertext: f.seal(t, "acct-a", secretbox.Envelope{"DB_URL": "postgres://x"})})
	if code != codes.OK || values["DB_URL"] != "postgres://x" {
		t.Fatalf("owner open = %v, %v", values, code)
	}
	if len(f.store.opens) != 1 || f.store.opens[0] != "n-owner/app-a/owner" {
		t.Fatalf("audit = %v", f.store.opens)
	}
}

func TestNodeCannotOpenAnotherNodesApp(t *testing.T) {
	f := newFixture(t, 0)
	// app-b belongs to n-other; even a valid envelope for its account is refused.
	if _, code := f.open(t, "app-b", &secretdpb.SealedEntry{Key: "K", Ciphertext: f.seal(t, "acct-b", secretbox.Envelope{"K": "v"})}); code != codes.PermissionDenied {
		t.Fatalf("cross-node open = %v, want PermissionDenied", code)
	}
	// Unknown callers and callers without identity are refused first.
	f.caller = "not-a-node"
	if _, code := f.open(t, "app-a"); code != codes.PermissionDenied {
		t.Fatalf("unknown node = %v", code)
	}
	f.caller = ""
	if _, code := f.open(t, "app-a"); code != codes.Unauthenticated {
		t.Fatalf("no identity = %v", code)
	}
	if len(f.store.opens) != 0 {
		t.Fatalf("refused opens were recorded: %v", f.store.opens)
	}
}

func TestEnvelopeForAnotherAccountOrUnboundIsRefused(t *testing.T) {
	f := newFixture(t, 0)
	// The owner of app-a submits ciphertext bound to another tenant.
	if _, code := f.open(t, "app-a", &secretdpb.SealedEntry{Key: "K", Ciphertext: f.seal(t, "acct-b", secretbox.Envelope{"K": "stolen"})}); code != codes.FailedPrecondition {
		t.Fatalf("other account's envelope = %v, want FailedPrecondition", code)
	}
	unbound, err := secretbox.Seal(f.id.Recipient(), secretbox.Envelope{"K": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, code := f.open(t, "app-a", &secretdpb.SealedEntry{Key: "K", Ciphertext: unbound}); code != codes.FailedPrecondition {
		t.Fatalf("unbound envelope = %v, want FailedPrecondition", code)
	}
	// An envelope holding more than the selected key is refused.
	if _, code := f.open(t, "app-a", &secretdpb.SealedEntry{Key: "K", Ciphertext: f.seal(t, "acct-a", secretbox.Envelope{"K": "v", "OTHER": "w"})}); code != codes.FailedPrecondition {
		t.Fatalf("multi-key envelope = %v", code)
	}
	if len(f.store.opens) != 0 {
		t.Fatalf("refused opens were recorded: %v", f.store.opens)
	}
}

func TestOnlyTheOwnerGrantsAndGrantsExpire(t *testing.T) {
	f := newFixture(t, 0)
	entry := &secretdpb.SealedEntry{Key: "K", Ciphertext: f.seal(t, "acct-a", secretbox.Envelope{"K": "v"})}

	// The peer cannot open, nor grant itself.
	f.caller = "peer-node"
	if _, code := f.open(t, "app-a", entry); code != codes.PermissionDenied {
		t.Fatalf("ungranted peer = %v", code)
	}
	if _, err := f.server.GrantPeer(f.ctx, &secretdpb.GrantPeerRequest{AppId: "app-a", NodeName: "peer-node", TtlSeconds: 600}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("self-grant by a non-owner = %v", err)
	}
	// The owner grants; the peer opens; after expiry it cannot.
	f.caller = "owner-node"
	resp, err := f.server.GrantPeer(f.ctx, &secretdpb.GrantPeerRequest{AppId: "app-a", NodeName: "peer-node", TtlSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetExpiresAt() != f.now.Add(10*time.Minute).Format(time.RFC3339) {
		t.Fatalf("expires_at = %s", resp.GetExpiresAt())
	}
	f.caller = "peer-node"
	if values, code := f.open(t, "app-a", entry); code != codes.OK || values["K"] != "v" {
		t.Fatalf("granted peer = %v, %v", values, code)
	}
	if f.store.opens[len(f.store.opens)-1] != "n-peer/app-a/grant" {
		t.Fatalf("audit = %v", f.store.opens)
	}
	f.now = f.now.Add(11 * time.Minute)
	if _, code := f.open(t, "app-a", entry); code != codes.PermissionDenied {
		t.Fatalf("expired grant = %v", code)
	}
	// TTLs are clamped to an hour.
	f.caller = "owner-node"
	resp, _ = f.server.GrantPeer(f.ctx, &secretdpb.GrantPeerRequest{AppId: "app-a", NodeName: "peer-node", TtlSeconds: 86400})
	if resp.GetExpiresAt() != f.now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("ttl not clamped: %s", resp.GetExpiresAt())
	}
}

func TestOpenThatCannotBeRecordedReturnsNothing(t *testing.T) {
	f := newFixture(t, 0)
	f.store.failRec = true
	if values, code := f.open(t, "app-a", &secretdpb.SealedEntry{Key: "K", Ciphertext: f.seal(t, "acct-a", secretbox.Envelope{"K": "v"})}); code != codes.Unavailable || values != nil {
		t.Fatalf("unrecorded open = %v, %v", values, code)
	}
}

func TestDistinctAppBudgetPerNode(t *testing.T) {
	f := newFixture(t, 2)
	for _, app := range []string{"app-1", "app-2", "app-3"} {
		f.store.apps[app] = secretd.AppTarget{OwnerNodeID: "n-owner", AccountID: "acct-a"}
	}
	entry := &secretdpb.SealedEntry{Key: "K", Ciphertext: f.seal(t, "acct-a", secretbox.Envelope{"K": "v"})}
	for _, app := range []string{"app-1", "app-2", "app-1"} {
		if _, code := f.open(t, app, entry); code != codes.OK {
			t.Fatalf("%s within budget = %v", app, code)
		}
	}
	if _, code := f.open(t, "app-3", entry); code != codes.ResourceExhausted {
		t.Fatalf("third distinct app = %v, want ResourceExhausted", code)
	}
	f.now = f.now.Add(61 * time.Minute)
	if _, code := f.open(t, "app-3", entry); code != codes.OK {
		t.Fatalf("after the window = %v", code)
	}
}

// Slice 3d: a capture key opens by reference, for the capture's own app and
// only for its owner or a granted peer.
func TestOpenCaptureKeyByReference(t *testing.T) {
	f := newFixture(t, 0)
	const captureA = "11111111-1111-1111-1111-111111111111"
	key, sealed, err := crashcrypt.NewKey(f.id.Recipient(), captureA)
	if err != nil {
		t.Fatal(err)
	}
	f.store.captures = map[string]fakeCapture{captureA: {app: "app-a", sealed: sealed}}
	open := func(app, capture string) (string, codes.Code) {
		resp, err := f.server.OpenCaptureKey(f.ctx, &secretdpb.OpenCaptureKeyRequest{AppId: app, CaptureId: capture})
		if err != nil {
			return "", status.Code(err)
		}
		return resp.GetIdentity(), codes.OK
	}
	if got, code := open("app-a", captureA); code != codes.OK || got != key.String() {
		t.Fatalf("owner open = %v", code)
	}
	if len(f.store.opens) != 1 || f.store.opens[0] != "n-owner/app-a/owner" {
		t.Fatalf("audit = %v", f.store.opens)
	}
	// The caller owns app-a but names it for another app's capture: the
	// capture's own app decides.
	f.store.apps["app-c"] = secretd.AppTarget{OwnerNodeID: "n-owner", AccountID: "acct-a"}
	if _, code := open("app-c", captureA); code != codes.PermissionDenied {
		t.Fatalf("capture under another app = %v", code)
	}
	if _, code := open("app-a", "22222222-2222-2222-2222-222222222222"); code != codes.NotFound {
		t.Fatalf("unknown capture = %v", code)
	}
	f.caller = "peer-node"
	if _, code := open("app-a", captureA); code != codes.PermissionDenied {
		t.Fatalf("ungranted peer = %v", code)
	}
	f.store.grants[grantKey{"app-a", "n-peer"}] = f.now.Add(time.Hour)
	if got, code := open("app-a", captureA); code != codes.OK || got != key.String() {
		t.Fatalf("granted peer = %v", code)
	}
}
