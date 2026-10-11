package main

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCommitConnectionDoesNotExposeCredentials(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	stdout, restore := swapStdout(t)
	defer restore()
	file := filepath.Join(t.TempDir(), "connection")
	secret := "postgres://relay:secret-password@db.example/customer?sslmode=verify-full"
	if err := os.WriteFile(file, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	source := uuid.NewString()
	if code := cmdCommit([]string{"connection", source, "--file", file}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodPut || f.sawPath != "/v1/commit-sources/"+source+"/connection" {
		t.Fatalf("route=%s %s", f.sawMethod, f.sawPath)
	}
	if strings.Contains(stdout.String(), "secret-password") || strings.Contains(stdout.String(), "postgres://") {
		t.Fatal("credential leaked to output")
	}
	if err := os.WriteFile(file, []byte(strings.Repeat("x", 8193)), 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdCommit([]string{"connection", source, "--file", file}); code == 0 {
		t.Fatal("oversized credential file accepted")
	}
}

func TestCommitConnectionRejectsSymlinkBeforeUpload(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secret")
	if err := os.WriteFile(secretFile, []byte("postgres://relay:private-password@db.example/customer?sslmode=verify-full"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "connection")
	if err := os.Symlink(secretFile, link); err != nil {
		t.Fatal(err)
	}
	if code := cmdCommit([]string{"connection", uuid.NewString(), "--file", link}); code == 0 {
		t.Fatal("symlink credential file accepted")
	}
	if f.sawMethod != "" {
		t.Fatalf("credential upload reached API: %s %s", f.sawMethod, f.sawPath)
	}
}

func TestCommitSourceRoutingFlags(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"source","contract_version":2,"allow_tenant_selection":true}`, http.StatusCreated)
	_, restore := swapStdout(t)
	defer restore()
	if code := cmdCommit([]string{"add", "orders", "--name", "customer-orders", "--operation-policy", "customer-orders", "--contract-version", "2", "--allow-tenant-selection"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	var req api.CreateCommitSourceRequest
	if err := json.Unmarshal(f.sawBody, &req); err != nil || req.ContractVersion != 2 || !req.AllowTenantSelection || req.Name != "customer-orders" || req.OperationPolicy != "customer-orders" {
		t.Fatalf("routing grant lost: %+v %v", req, err)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/orders/commit-sources" {
		t.Fatalf("route=%s %s", f.sawMethod, f.sawPath)
	}
}

func TestCommitSourceRoutingFlagsRejectInvalidCombinations(t *testing.T) {
	for _, args := range [][]string{
		{"add", "orders", "--name", "orders", "--operation-policy", "orders", "--allow-tenant-selection"},
		{"add", "orders", "--name", "orders", "--operation-policy", "orders", "--contract-version", "3"},
		{"info", uuid.NewString(), "--contract-version", "1"},
		{"pause", uuid.NewString(), "--allow-tenant-selection=false"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := authedFakeAPI(t, "{}", http.StatusOK)
			if code := cmdCommit(args); code == 0 || f.sawMethod != "" {
				t.Fatalf("invalid routing flags reached API: code=%d method=%s", code, f.sawMethod)
			}
		})
	}
}
