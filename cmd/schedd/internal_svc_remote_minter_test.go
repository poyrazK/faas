package main

// adr: 20261110210000

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
)

type fakeTokenRPC struct {
	calls int
	err   error
	now   *time.Time
}

func (f *fakeTokenRPC) MintInternalServiceToken(_ context.Context, in *secretdpb.MintInternalServiceTokenRequest, _ ...grpc.CallOption) (*secretdpb.MintInternalServiceTokenResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &secretdpb.MintInternalServiceTokenResponse{Token: "tok-" + in.GetAppId(),
		ExpiresAt: f.now.Add(30 * time.Second).UTC().Format(time.RFC3339)}, nil
}

// Slice 3k: a token is reused until 10 s before it expires, per app.
func TestRemoteInternalSvcMinterCaches(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	rpc := &fakeTokenRPC{now: &now}
	m := newRemoteInternalSvcMinter(rpc)
	m.now = func() time.Time { return now }
	for range 3 {
		if tok, err := m.Mint("app-a"); err != nil || tok != "tok-app-a" {
			t.Fatalf("mint = %q, %v", tok, err)
		}
	}
	if rpc.calls != 1 {
		t.Fatalf("secretd calls = %d, want 1", rpc.calls)
	}
	if _, err := m.Mint("app-b"); err != nil || rpc.calls != 2 {
		t.Fatalf("another app: calls=%d err=%v", rpc.calls, err)
	}
	now = now.Add(21 * time.Second)
	if _, err := m.Mint("app-a"); err != nil || rpc.calls != 3 {
		t.Fatalf("near expiry: calls=%d err=%v, want a renewal", rpc.calls, err)
	}
	rpc.err = errors.New("permission denied")
	now = now.Add(time.Minute)
	if _, err := m.Mint("app-a"); err == nil {
		t.Fatal("a refused mint returned a token")
	}
	if _, err := m.Mint(""); err == nil {
		t.Fatal("a mint without an app succeeded")
	}
}

func TestInternalSvcMinterFromEnv(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for name, tc := range map[string]struct {
		v       string
		secretd bool
		want    bool
		wantErr bool
	}{
		"unset":           {v: "", secretd: true},
		"local":           {v: "local", secretd: true},
		"secretd":         {v: "secretd", secretd: true, want: true},
		"secretd unwired": {v: "secretd", wantErr: true},
		"unknown":         {v: "remote", secretd: true, wantErr: true},
	} {
		got, err := internalSvcMinterFromEnv(env(tc.v), tc.secretd)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("%s = %v, %v", name, got, err)
		}
	}
}
