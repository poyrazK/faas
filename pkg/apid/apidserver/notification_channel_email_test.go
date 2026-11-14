package apidserver_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261109153000 — an email channel needs the account's own address,
// verified: an unverified address may belong to someone else.
func TestEmailChannelNeedsAVerifiedAddress(t *testing.T) {
	req := api.CreateNotificationChannelRequest{Name: "me", Kind: "email", Email: "me@example.com"}
	unverified := state.Account{ID: "a", Email: "me@example.com"}
	if _, prob := apidserver.SealNotificationChannel(unverified, req); prob == nil {
		t.Fatal("an unverified address was accepted")
	}
	at := time.Now()
	verified := unverified
	verified.EmailVerifiedAt = &at
	if ch, prob := apidserver.SealNotificationChannel(verified, req); prob != nil || ch.Email != "me@example.com" {
		t.Fatalf("verified own address = %+v, %v", ch, prob)
	}
}
