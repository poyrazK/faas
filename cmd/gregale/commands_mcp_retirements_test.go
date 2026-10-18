package main

import (
	"bytes"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestMCPCatalogRetirementRecoveryOutput(t *testing.T) {
	old := osStdout
	var output bytes.Buffer
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	writeMCPOAuthRetirement(api.MCPOAuthRetirement{ID: "record", AppSlug: "deleted-app", Role: "reader", State: "held", Reason: "replacement", RetainUntil: time.Now()})
	if !bytes.Contains(output.Bytes(), []byte("--allow-active-grant-revocation")) || !bytes.Contains(output.Bytes(), []byte("deleted-app/reader")) {
		t.Fatal("held retirement lacked recovery guidance")
	}
	output.Reset()
	writeMCPOAuthRetirement(api.MCPOAuthRetirement{State: "manual_required"})
	if !bytes.Contains(output.Bytes(), []byte("acknowledge-manual")) {
		t.Fatal("manual recovery lacked guidance")
	}
}
