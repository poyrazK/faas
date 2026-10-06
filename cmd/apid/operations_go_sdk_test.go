// adr: 521
//go:build !no_pg

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func buildOperationsGoSDKInspector(t *testing.T) string {
	t.Helper()
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("Go is required for standalone Operations SDK acceptance")
	}
	directory, err := filepath.Abs("../../sdk/go")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "operations-go-sdk-inspect")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBinary, "build", "-race", "-p", "1", "-o", binary, "./testdata/operations-inspect")
	cmd.Dir = directory
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build standalone Go Operations inspector: %v\n%s", err, output)
	}
	return binary
}

func inspectOperationWithGoSDK(t *testing.T, binary, apiURL, token, foreignToken, definitionID, operationID string) {
	t.Helper()
	config, err := json.Marshal(struct {
		API, Token, ForeignToken, DefinitionID, OperationID string
	}{apiURL, token, foreignToken, definitionID, operationID})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = append(os.Environ(), "GREGALE_OPERATIONS_TEST_CONFIG="+string(config))
	output, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("Go Operations inspection passed")) {
		t.Fatalf("standalone Go Operations process acceptance failed: %v\n%s", err, output)
	}
}
