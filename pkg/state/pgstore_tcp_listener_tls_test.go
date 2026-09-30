package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreTCPListenerTLSRoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	account, app, _ := seedLiveDeploy(t, s, ctx, "-tcp-tls")
	created, err := s.CreateTCPListener(ctx, state.TCPListener{AccountID: account, AppID: app, ListenerName: "tls", GuestPort: 9000, PublicPort: 40138, TLSMode: api.TCPListenerTLSTerminate, TLSHostname: " Echo.Example "})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.TCPListenerByID(ctx, created.ID)
	if err != nil || loaded.TLSMode != api.TCPListenerTLSTerminate || loaded.TLSHostname != "echo.example" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if _, err := s.SetTCPListenerEnabled(ctx, created.ID, true); err != nil {
		t.Fatal(err)
	}
	updated, err := s.SetTCPListenerTLS(ctx, created.ID, api.TCPListenerTLSConfig{})
	if err != nil || updated.Enabled || updated.TLSMode != api.TCPListenerTLSPassthrough || updated.TLSHostname != "" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	legacy, err := s.CreateTCPListener(ctx, state.TCPListener{AccountID: account, AppID: app, ListenerName: "legacy", GuestPort: 9001, PublicPort: 40139})
	if err != nil || legacy.TLSMode != api.TCPListenerTLSPassthrough || legacy.TLSHostname != "" {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
}
