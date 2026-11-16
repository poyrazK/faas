package main_test

import (
	"encoding/json"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestMCPPrivatePublicationReadinessOwnerCLI(t *testing.T) {
	main.SetupConnectionProfiles(t)
	f := granttest.NewPublication(t, state.NewMemStore())
	granttest.ConfigurePublicationReadiness(t, f, mcphosting.ProtocolVersion)
	t.Setenv("FAAS_API", f.Native.URL)
	t.Setenv("FAAS_TOKEN", f.Owner.Token())
	v, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--json", "mcp", "server", "tool-publication", "approve", "--id", f.Publication.ID, "--request-sha256", v.RequestSHA256, "--credential-secret-ref", "secret:MCP_PUBLICATION_TEST"}
	output, restore := main.CaptureStdout(t)
	code := main.Run(args)
	restore()
	if code == 0 {
		t.Fatal("CLI approved role credentials without acknowledgement")
	}
	output, restore = main.CaptureStdout(t)
	code = main.Run(append(args, "--approve-readiness-roles"))
	restore()
	if code != 0 || !json.Valid([]byte(output.String())) {
		t.Fatalf("approval: %d %s", code, output.String())
	}
	for _, value := range []string{f.Token, "MCP_PUBLICATION_TEST", "MCP_READINESS_", "readiness-reader-token"} {
		if strings.Contains(output.String(), value) {
			t.Fatal("owner CLI receipt exposed credential authority")
		}
	}
	if f.Calls.Load() != 0 || f.ReadinessCalls.Load() != 0 {
		t.Fatal("owner approval ran verification")
	}
	_, err = f.Owner.DecideToolPublicationVerification(t.Context(), f.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: v.RequestSHA256, Revoke: true})
	if err != nil {
		t.Fatal(err)
	}
}
