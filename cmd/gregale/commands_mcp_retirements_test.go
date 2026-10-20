package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestMCPCatalogRetirementRecoveryOutput(t *testing.T) {
	old := (*main.OsStdout)
	var output bytes.Buffer
	(*main.OsStdout) = &output
	t.Cleanup(func() { (*main.OsStdout) = old })
	main.WriteMCPOAuthRetirement(api.MCPOAuthRetirement{ID: "record", AppSlug: "deleted-app", Role: "reader", State: "held", Reason: "replacement", RetainUntil: time.Now()})
	if !bytes.Contains(output.Bytes(), []byte("--allow-active-grant-revocation")) || !bytes.Contains(output.Bytes(), []byte("deleted-app/reader")) {
		t.Fatal("held retirement lacked recovery guidance")
	}
	output.Reset()
	main.WriteMCPOAuthRetirement(api.MCPOAuthRetirement{State: "manual_required"})
	if !bytes.Contains(output.Bytes(), []byte("acknowledge-manual")) {
		t.Fatal("manual recovery lacked guidance")
	}
}
