package state

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestToolOAuthLeaseNeverReplaysUncertainRefresh(t *testing.T) {
	now := time.Now().UTC()
	initial := ToolOAuthConnection{Record: api.ToolOAuthConnectionRecord{ID: uuid.NewString(), State: "refresh_required"}}
	claimed, err := claimToolOAuth(initial, now)
	if err != nil || !claimed.Fresh || claimed.Connection.LeaseID == "" {
		t.Fatal("initial renewal did not acquire a durable lease", err)
	}
	if _, err = claimToolOAuth(claimed.Connection, now.Add(time.Second)); !errors.Is(err, ErrToolOAuthBusy) {
		t.Fatal("concurrent renewal acquired the same credential", err)
	}
	late := now.Add(api.ToolOAuthRenewalLease)
	if _, err = finishToolOAuth(claimed.Connection, claimed.Connection.LeaseID, []byte("ciphertext"), &late, "", late); !errors.Is(err, ErrConflict) {
		t.Fatal("expired lease installed credentials", err)
	}
	expired, err := claimToolOAuth(claimed.Connection, late)
	if !errors.Is(err, ErrToolOAuthReconnect) || expired.Fresh || expired.Connection.Record.State != "reconnect_required" || expired.Connection.LeaseID != "" || expired.Connection.LeaseUntil != nil {
		t.Fatal("uncertain refresh was eligible for replay", err)
	}
	if _, err = claimToolOAuth(expired.Connection, late.Add(time.Hour)); !errors.Is(err, ErrToolOAuthReconnect) {
		t.Fatal("reconnect state became eligible for automatic exchange", err)
	}
}
