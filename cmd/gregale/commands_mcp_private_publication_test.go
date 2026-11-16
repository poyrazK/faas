package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestMCPPrivatePublicationOwnerCLI(t *testing.T) {
	main.SetupConnectionProfiles(t)
	f := granttest.NewPublication(t, state.NewMemStore())
	t.Setenv("FAAS_API", f.Native.URL)
	t.Setenv("FAAS_TOKEN", f.Owner.Token())
	v, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"get", "approve", "revoke"} {
		args := []string{"--json", "mcp", "server", "tool-publication", verb, "--id", f.Publication.ID}
		if verb != "get" {
			args = append(args, "--request-sha256", v.RequestSHA256)
		}
		if verb == "approve" {
			args = append(args, "--credential-secret-ref", "secret:MCP_PUBLICATION_TEST")
		}
		output, restore := main.CaptureStdout(t)
		code := main.Run(args)
		restore()
		if code != 0 || !json.Valid([]byte(output.String())) || strings.Contains(output.String(), f.Token) || strings.Contains(output.String(), "MCP_PUBLICATION_TEST") {
			t.Fatalf("%s: %d %s", verb, code, output.String())
		}
	}
	got, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
	if err != nil || got.Status != "revoked" || f.Calls.Load() != 0 {
		t.Fatal("CLI decision dispatched fixtures", err)
	}
}

func TestMCPPrivatePublicationWaitUsesNativeReceipt(t *testing.T) {
	f := granttest.NewPublication(t, state.NewMemStore())
	f.Approve(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for range 2 {
		v, err := main.WaitPrivatePublicationVerificationForTest(ctx, f.Publisher, f.Publication.ID, f.Request)
		if err != nil || v.Status != "completed" || f.Calls.Load() != 1 {
			t.Fatalf("native receipt: %+v %v calls=%d", v, err, f.Calls.Load())
		}
		data, _ := json.Marshal(v)
		if strings.Contains(string(data), f.Token) || strings.Contains(string(data), "MCP_PUBLICATION_TEST") {
			t.Fatal("CLI received a credential")
		}
	}
}

func TestMCPPrivatePublicationPendingWaitCancellation(t *testing.T) {
	f := granttest.NewPublication(t, state.NewMemStore())
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := main.WaitPrivatePublicationVerificationForTest(ctx, f.Publisher, f.Publication.ID, f.Request)
	if !errors.Is(err, context.DeadlineExceeded) || f.Calls.Load() != 0 {
		t.Fatal("pending wait executed a fixture", err)
	}
	p, err := f.Owner.GetToolPublication(t.Context(), f.Publication.ID)
	if err != nil || p.Verification == nil || p.Verification.Status != "pending" {
		t.Fatal("cancelled client lost durable pending review", err)
	}
	_, err = f.Owner.DecideToolPublicationVerification(t.Context(), f.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: p.Verification.RequestSHA256, Revoke: true})
	if err != nil {
		t.Fatal(err)
	}
}
