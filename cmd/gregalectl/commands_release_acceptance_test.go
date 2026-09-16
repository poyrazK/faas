package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReleaseAcceptanceCredentialLifecycle(t *testing.T) {
	store := state.NewMemStore()
	oldOpener := releaseAcceptanceStoreOpener
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	t.Cleanup(func() {
		releaseAcceptanceStoreOpener = oldOpener
		osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON
	})
	releaseAcceptanceStoreOpener = func() (state.Store, func(), error) { return store, func() {}, nil }
	var out, stderr bytes.Buffer
	osStdout, osStderr, jsonOutput = &out, &stderr, true

	if code := cmdReleaseAcceptanceMintToken([]string{"--yes", "--reason=" + releaseAcceptanceReason, "--ttl=30m"}); code != 0 {
		t.Fatalf("mint code=%d stderr=%s", code, stderr.String())
	}
	var credential releaseAcceptanceCredential
	if err := json.Unmarshal(out.Bytes(), &credential); err != nil {
		t.Fatal(err)
	}
	if credential.KeyID == "" || credential.AccountID == "" || credential.Token == "" || time.Until(credential.ExpiresAt) <= 0 {
		t.Fatalf("credential = %+v", credential)
	}
	account, key, err := store.AuthenticateKey(context.Background(), api.HashAPIKey(credential.Token))
	if err != nil || account.ID != credential.AccountID || key.ID != credential.KeyID {
		t.Fatalf("AuthenticateKey = account=%s key=%s err=%v", account.ID, key.ID, err)
	}

	out.Reset()
	if code := cmdReleaseAcceptanceRevokeToken([]string{"--key-id", credential.KeyID, "--yes", "--reason=" + releaseAcceptanceReason}); code != 0 {
		t.Fatalf("revoke code=%d stderr=%s", code, stderr.String())
	}
	if _, _, err := store.AuthenticateKey(context.Background(), api.HashAPIKey(credential.Token)); !errors.Is(err, state.ErrAPIKeyRevoked) {
		t.Fatalf("post-revoke auth error = %v, want ErrAPIKeyRevoked", err)
	}
}

func TestReleaseAcceptanceRequiresExplicitMutationAcknowledgement(t *testing.T) {
	if code := cmdReleaseAcceptanceMintToken(nil); code != 2 {
		t.Fatalf("mint without acknowledgement code=%d, want 2", code)
	}
	if code := cmdReleaseAcceptanceRevokeToken([]string{"--key-id", "key"}); code != 2 {
		t.Fatalf("revoke without acknowledgement code=%d, want 2", code)
	}
}

func TestReleaseAcceptanceVerifyPlacementRequiresBothShapesOnEveryNode(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := ensureReleaseAcceptanceAccount(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.UpsertComputeNode(ctx, state.ComputeNode{ID: "node-b", Name: "node-b", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	local, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	slugs := []string{"app-a", "fn-a", "app-b", "fn-b"}
	fixtures := []state.App{
		{AccountID: account.ID, Slug: slugs[0], Type: state.AppTypeApp, NodeID: local.ID},
		{AccountID: account.ID, Slug: slugs[1], Type: state.AppTypeFunction, NodeID: local.ID},
		{AccountID: account.ID, Slug: slugs[2], Type: state.AppTypeApp, NodeID: node.ID},
		{AccountID: account.ID, Slug: slugs[3], Type: state.AppTypeFunction, NodeID: node.ID},
	}
	for _, fixture := range fixtures {
		if _, err := store.CreateApp(ctx, fixture); err != nil {
			t.Fatal(err)
		}
	}

	oldOpener := releaseAcceptanceStoreOpener
	oldOut, oldErr := osStdout, osStderr
	t.Cleanup(func() {
		releaseAcceptanceStoreOpener = oldOpener
		osStdout, osStderr = oldOut, oldErr
	})
	releaseAcceptanceStoreOpener = func() (state.Store, func(), error) { return store, func() {}, nil }
	var out, stderr bytes.Buffer
	osStdout, osStderr = &out, &stderr
	if code := cmdReleaseAcceptanceVerifyPlacement([]string{"--slugs", strings.Join(slugs, ",")}); code != 0 {
		t.Fatalf("verify code=%d stderr=%s", code, stderr.String())
	}
	var report releaseAcceptancePlacementReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Ready || len(report.Nodes) != 2 {
		t.Fatalf("report = %+v", report)
	}
}
