package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGuidedAuthorizationExpiryAndConsumption(t *testing.T) {
	now := time.Now().UTC()
	hash := strings.Repeat("a", 64)
	fresh := func() ToolOAuthConnection {
		return ToolOAuthConnection{Record: api.ToolOAuthConnectionRecord{Intent: api.ToolOAuthConnectionIntent{Guided: true}, State: "authorization_pending"}, Authorization: &ToolOAuthAuthorization{Phase: "pending", StateSHA256: hash, ExpiresAt: now.Add(api.ToolOAuthAuthorizationLifetime), Ciphertext: []byte("sealed-verifier")}}
	}
	wrong, err := consumeToolAuthorization(fresh(), strings.Repeat("b", 64), now)
	if !errors.Is(err, ErrConflict) || wrong.Authorization.Phase != "pending" || len(wrong.Authorization.Ciphertext) == 0 {
		t.Fatal("invalid callback consumed the original verifier", err)
	}
	expired, err := consumeToolAuthorization(fresh(), hash, now.Add(api.ToolOAuthAuthorizationLifetime))
	if !errors.Is(err, ErrToolOAuthReconnect) || expired.Authorization.Phase != "failed" || len(expired.Authorization.Ciphertext) != 0 {
		t.Fatal("expired callback remained usable", err)
	}
	claimed, err := consumeToolAuthorization(fresh(), hash, now)
	if err != nil || claimed.Record.State != "refreshing" || claimed.LeaseID == "" || claimed.Authorization.Phase != "exchanging" || len(claimed.Authorization.Ciphertext) != 0 {
		t.Fatal("authorization was not durably consumed before exchange", err)
	}
	if _, err = consumeToolAuthorization(claimed, hash, now); !errors.Is(err, ErrConflict) {
		t.Fatal("consumed callback was replayable", err)
	}
	expiry := now.Add(time.Hour)
	if _, err = finishToolAuthorization(claimed, claimed.LeaseID, []byte("tokens"), &expiry, "", now.Add(api.ToolOAuthRenewalLease)); !errors.Is(err, ErrConflict) {
		t.Fatal("expired issuance lease installed credentials", err)
	}
}
