package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/state"
)

// familyPolicy installs a keyed policy for family; contract turns plain
// lookup off. The policy is process-wide, so callers must not run in
// parallel.
func familyPolicy(t *testing.T, family credhash.Family, contract bool) *credhash.Policy {
	t.Helper()
	line, err := credhash.NewKeyLine(2)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	p := &credhash.Policy{Ring: ring, KeyedWrites: true, PlainLookup: !contract}
	credhash.Set(family, p)
	t.Cleanup(func() { credhash.Set(family, nil); credhash.SetRemote(family, nil) })
	return p
}

// adr: 20261029202403 amendment 1 — an event acknowledgement's digest is
// stored keyed under the scheduler ring, whole: a keyed digest is 33 bytes
// and used to be cut to 32 on read.
func TestEventAcknowledgmentDigestsAreKeyed(t *testing.T) {
	familyPolicy(t, credhash.FamilyScheduler, true)
	token, stored, err := state.AcknowledgmentToken()
	if err != nil {
		t.Fatal(err)
	}
	if !credhash.Keyed(stored) {
		t.Fatalf("stored digest %x is not keyed", stored)
	}
	digest, err := state.AcknowledgmentDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	if !state.AcknowledgmentMatches(context.Background(), digest, stored) {
		t.Fatal("a keyed acknowledgement refused its token")
	}
	if state.AcknowledgmentMatches(context.Background(), digest, stored[:32]) {
		t.Fatal("a truncated digest matched")
	}
	plain := sha256.Sum256([]byte(token))
	if state.AcknowledgmentMatches(context.Background(), digest, plain[:]) {
		t.Fatal("contract step accepted a plain digest a database writer could insert")
	}
}

// adr: 20261029202403 amendment 1 — gatewayd-internal holds no scheduler
// ring: it asks apid to match an operation's capability digest, and an
// apid failure refuses the capability.
func TestOperationCapabilityDigestIsMatchedRemotely(t *testing.T) {
	policy := familyPolicy(t, credhash.FamilyScheduler, true)
	capability := hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	sum, err := hex.DecodeString(state.OperationCapabilityDigest(capability))
	if err != nil {
		t.Fatal(err)
	}
	stored := hex.EncodeToString(policy.Store(state.CredentialColumnOperationCapability, sum))
	now := time.Now()
	lease := now.Add(time.Minute)
	op := state.Operation{OperationResponse: api.OperationResponse{State: api.OperationRunning}, AccountID: "a", AppID: "app", CurrentInvocationID: "inv", ExecutionAttempt: 1, ExecutionCapabilityDigest: stored}
	inv := state.Invocation{ID: "inv", InstanceID: "i", Attempts: 1, State: state.InvocationDispatching, LeaseExpiresAt: &lease, DeadlineAt: &lease}
	authority := state.OperationExecutionAuthority{AccountID: "a", AppID: "app", InstanceID: "i", InvocationID: "inv", Attempt: 1, Capability: capability}
	if err := state.ValidateOperationExecutionAuthority(op, inv, authority, now); err != nil {
		t.Fatalf("apid's own ring refused the capability: %v", err)
	}

	ctx := context.Background()
	credhash.Set(credhash.FamilyScheduler, nil)
	if err := state.ValidateOperationExecutionAuthorityContext(ctx, op, inv, authority, now); err == nil {
		t.Fatal("a process without the ring or a remote accepted a keyed digest")
	}
	asked := 0
	credhash.SetRemote(credhash.FamilyScheduler, func(_ context.Context, tag credhash.Tag, s, sum []byte) (bool, error) {
		asked++
		return tag == state.CredentialColumnOperationCapability && policy.Matches(tag, sum, s), nil
	})
	if err := state.ValidateOperationExecutionAuthorityContext(ctx, op, inv, authority, now); err != nil || asked != 1 {
		t.Fatalf("remote match = %v after %d asks", err, asked)
	}
	authority.Capability = hex.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))
	if err := state.ValidateOperationExecutionAuthorityContext(ctx, op, inv, authority, now); err == nil {
		t.Fatal("another capability was accepted")
	}
}

// adr: 20261029202403 amendment 1 — upload grants are found by their keyed
// hex digest under the uploads ring, and by the plain one only while plain
// lookup is on.
func TestObjectUploadGrantLookupsFollowTheRollout(t *testing.T) {
	sum := sha256.Sum256([]byte("grant-token"))
	plain := hex.EncodeToString(sum[:])
	policy := familyPolicy(t, credhash.FamilyUploads, false)
	keyed := hex.EncodeToString(policy.Store("object_storage_upload_grants.token_hash", sum[:]))
	lookups := state.ObjectUploadGrantLookups(plain)
	if len(lookups) != 2 || lookups[0] != keyed || lookups[1] != plain {
		t.Fatalf("rollout lookups = %v", lookups)
	}
	familyPolicy(t, credhash.FamilyUploads, true)
	if slices.Contains(state.ObjectUploadGrantLookups(plain), plain) {
		t.Fatal("contract step still looks a plain grant hash up")
	}
}

// adr: 20261029202403 amendment 1 decision 2 — apid answers MatchCredential
// for each column a compute-node daemon checks, under that column's family.
func TestGatewayCheckedCredentialFamilies(t *testing.T) {
	for column, want := range map[string]credhash.Family{
		string(state.CredentialColumnConsumerKey):         credhash.FamilyAccount,
		string(state.CredentialColumnForkToken):           credhash.FamilyAccount,
		string(state.CredentialColumnOperationCapability): credhash.FamilyScheduler,
		string(state.CredentialColumnDevBridgeAttachment): credhash.FamilyDevBridge,
		string(state.CredentialColumnDevBridgeRequest):    credhash.FamilyDevBridge,
		api.DataAPIServerKeyCredentialTag:                 credhash.FamilyDataAPI,
	} {
		tag, family, ok := state.GatewayCheckedCredential(column)
		if !ok || string(tag) != column || family != want {
			t.Fatalf("%s = %s %s %v, want %s", column, tag, family, ok, want)
		}
	}
	if _, _, ok := state.GatewayCheckedCredential("api_keys.key_sha256"); ok {
		t.Fatal("a gateway may ask apid to match an API key hash")
	}
}
