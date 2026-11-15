package main_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestMCPManagedPublicationOwnerCLI(t *testing.T) {
	main.SetupConnectionProfiles(t)
	m := granttest.NewManagedPublication(t, state.NewMemStore())
	p := m.Publication
	t.Setenv("FAAS_API", p.Native.URL)
	t.Setenv("FAAS_TOKEN", p.Owner.Token())
	v, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request)
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"--json", "mcp", "server", "tool-publication", "approve", "--id", p.Publication.ID, "--request-sha256", v.RequestSHA256}
	for _, flags := range [][]string{nil, {"--credential-connection-id", "not-a-uuid"}, {"--credential-connection-id", m.Connection.ID, "--credential-secret-ref", "secret:MCP_PUBLICATION_TEST"}} {
		output, restore := main.CaptureStdout(t)
		code := main.Run(append(append([]string{}, base...), flags...))
		restore()
		if code == 0 {
			t.Fatalf("invalid credential selection succeeded: %s", output.String())
		}
	}
	output, restore := main.CaptureStdout(t)
	code := main.Run(append(base, "--credential-connection-id", m.Connection.ID))
	restore()
	if code != 0 || !json.Valid([]byte(output.String())) || strings.Contains(output.String(), m.Connection.ID) || m.Exchanges.Load() != 0 || p.Calls.Load() != 0 {
		t.Fatalf("managed approval: code=%d %s", code, output.String())
	}
	record, err := p.Owner.GetToolPublication(t.Context(), p.Publication.ID)
	if err != nil || record.Verification == nil || record.Verification.Status != "approved" {
		t.Fatal("CLI failed to bind the managed approval", err)
	}
}

func TestMCPManagedPublicationReconnectHint(t *testing.T) {
	m := granttest.NewManagedPublication(t, state.NewMemStore())
	m.Mode.Store(1)
	m.Approve(t)
	p := m.Publication
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	v, err := main.WaitPrivatePublicationVerificationForTest(ctx, p.Publisher, p.Publication.ID, p.Request)
	if err == nil || !strings.Contains(err.Error(), "reconnecting OAuth") || strings.Contains(err.Error(), m.Connection.ID) || v.Outcome == nil || v.Outcome.Code != "reconnect_required" || m.Exchanges.Load() != 1 || p.Calls.Load() != 0 {
		t.Fatal("managed publication omitted safe reconnect guidance", err)
	}
}
