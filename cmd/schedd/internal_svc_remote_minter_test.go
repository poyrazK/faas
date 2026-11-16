package main

// adr: 20261110210000

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/outbound"
)

type fakeTokenRPC struct {
	calls    int
	err      error
	now      *time.Time
	workflow *secretdpb.MintWorkflowIdentityRequest
}

func (f *fakeTokenRPC) MintWorkflowIdentity(_ context.Context, in *secretdpb.MintWorkflowIdentityRequest, _ ...grpc.CallOption) (*secretdpb.MintWorkflowIdentityResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	f.workflow = in
	return &secretdpb.MintWorkflowIdentityResponse{Token: "wf-" + in.GetRunId()}, nil
}

// Slice 3k-2: a workflow identity is minted by secretd per attempt, with the
// body's hash and never the body.
func TestRemoteInternalSvcMinterWorkflow(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	rpc := &fakeTokenRPC{now: &now}
	m := newRemoteInternalSvcMinter(rpc)
	identity := outbound.WorkflowIdentity{AccountID: "acct", AppID: "app", RunID: "run-1", StepName: "s", Attempt: 2, AttemptToken: "at"}
	request := outbound.WorkflowOutboundRequest{IntegrationID: "int", Method: "POST", Path: "/v1/x", PathTemplate: "/v1/x", QueryTemplate: map[string]string{"q": "v"}}
	body := []byte(`{"secret":"never leaves"}`)
	for range 2 {
		if tok, err := m.MintWorkflow(identity, request, body); err != nil || tok != "wf-run-1" {
			t.Fatalf("mint = %q, %v", tok, err)
		}
	}
	if rpc.calls != 2 {
		t.Fatalf("secretd calls = %d, want one per attempt", rpc.calls)
	}
	got := rpc.workflow
	if got.GetBodySha256() != outbound.WorkflowBodySHA256(body) || got.GetAppId() != "app" || got.GetAttempt() != 2 || got.GetQueryTemplate()["q"] != "v" {
		t.Fatalf("request = %+v", got)
	}
	if strings.Contains(got.String(), "never leaves") {
		t.Fatal("the body reached secretd")
	}
	rpc.err = errors.New("permission denied")
	if _, err := m.MintWorkflow(identity, request, body); err == nil {
		t.Fatal("a refused mint returned a token")
	}
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
