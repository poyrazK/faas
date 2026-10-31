package devbridge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// adr: 20261029202403 amendment 1 — a session's digests are keyed under the
// dev-bridge ring where apid and bridged hold it, and gatewayd-internal,
// which does not, asks apid through the remote it installs.
func TestSessionDigestsAreKeyed(t *testing.T) {
	line, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	policy := &credhash.Policy{Ring: ring, KeyedWrites: true}
	credhash.Set(credhash.FamilyDevBridge, policy)
	t.Cleanup(func() {
		credhash.Set(credhash.FamilyDevBridge, nil)
		credhash.SetRemote(credhash.FamilyDevBridge, nil)
	})
	now := time.Now()
	s, creds, err := NewSession(Scope{AccountID: "a", DeveloperID: "d", EnvironmentID: "e", TargetAppID: "app"}, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !credhash.Keyed(s.AttachmentDigest) || !credhash.Keyed(s.RequestDigest) {
		t.Fatalf("digests not keyed: %x %x", s.AttachmentDigest, s.RequestDigest)
	}
	if s.AuthorizeAttachment(context.Background(), now, creds.AttachmentToken) != nil || s.AuthorizeRequest(context.Background(), now, creds.RequestToken, "a", "e", "app") != nil {
		t.Fatal("a keyed session refused its own credentials")
	}
	if s.AuthorizeAttachment(context.Background(), now, creds.RequestToken) == nil {
		t.Fatal("the request token attached a tunnel")
	}

	// gatewayd-internal holds no ring: it asks apid, here a stand-in that
	// answers with the ring apid holds.
	credhash.Set(credhash.FamilyDevBridge, nil)
	if s.AuthorizeAttachment(context.Background(), now, creds.AttachmentToken) == nil {
		t.Fatal("a process without the ring or a remote accepted a keyed digest")
	}
	credhash.SetRemote(credhash.FamilyDevBridge, func(_ context.Context, tag credhash.Tag, stored, sum []byte) (bool, error) {
		return policy.Matches(tag, sum, stored), nil
	})
	if err := s.AuthorizeAttachment(context.Background(), now, creds.AttachmentToken); err != nil {
		t.Fatalf("apid's match was not used: %v", err)
	}
	credhash.SetRemote(credhash.FamilyDevBridge, func(context.Context, credhash.Tag, []byte, []byte) (bool, error) {
		return false, errors.New("apid unreachable")
	})
	if s.AuthorizeAttachment(context.Background(), now, creds.AttachmentToken) == nil {
		t.Fatal("an apid failure accepted the token")
	}
}
