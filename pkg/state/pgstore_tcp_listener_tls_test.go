package state_test

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
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

func TestPgStoreTCPListenerTLSRejectsInvalidIntentWithoutMutation(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app, _ := seedLiveDeploy(t, s, ctx, "-tcp-tls-invalid")
	created, err := s.CreateTCPListener(ctx, state.TCPListener{AccountID: account, AppID: app, ListenerName: "tls", GuestPort: 9000, PublicPort: 40138, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []api.TCPListenerTLSConfig{
		{Mode: "unknown"},
		{Mode: api.TCPListenerTLSPassthrough, Hostname: "echo.example"},
		{Mode: api.TCPListenerTLSTerminate, Hostname: "*.example"},
		{Mode: api.TCPListenerTLSTerminate, Hostname: "127.0.0.1"},
		{Mode: api.TCPListenerTLSTerminate, Hostname: strings.Repeat("x", 64) + ".example"},
	} {
		if _, err := s.SetTCPListenerTLS(ctx, created.ID, policy); !errors.Is(err, state.ErrInvalidTCPListener) {
			t.Fatalf("policy %+v: error = %v", policy, err)
		}
		_, err := pool.Exec(ctx, "UPDATE app_tcp_listeners SET tls_mode=$2, tls_hostname=$3 WHERE id=$1", created.ID, policy.Mode, policy.Hostname)
		var constraint *pgconn.PgError
		if !errors.As(err, &constraint) || constraint.Code != "23514" {
			t.Fatalf("database accepted policy %+v or returned unexpected error: %v", policy, err)
		}
	}
	loaded, err := s.TCPListenerByID(ctx, created.ID)
	if err != nil || loaded != created {
		t.Fatalf("invalid intent changed listener: loaded=%+v created=%+v err=%v", loaded, created, err)
	}
	if _, err := s.SetTCPListenerTLS(ctx, "00000000-0000-0000-0000-000000000001", api.TCPListenerTLSConfig{}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing listener error = %v", err)
	}
}
