package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func withJourneyFixtureStore(t *testing.T, store state.Store) *bytes.Buffer {
	t.Helper()
	oldOpener, oldOut, oldErr := journeyFixtureStoreOpener, osStdout, osStderr
	t.Cleanup(func() { journeyFixtureStoreOpener, osStdout, osStderr = oldOpener, oldOut, oldErr })
	journeyFixtureStoreOpener = func() (state.Store, func(), error) { return store, func() {}, nil }
	var stderr bytes.Buffer
	osStderr = &stderr
	return &stderr
}

func mintJourneyFixture(t *testing.T, stderr *bytes.Buffer, args ...string) (journeyFixtureCredential, int) {
	t.Helper()
	var out bytes.Buffer
	osStdout = &out
	code := cmdJourneyFixtureDispatch(append([]string{"mint-token", "--yes", "--reason=" + journeyFixtureReason}, args...))
	var c journeyFixtureCredential
	if code == 0 {
		if err := json.Unmarshal(out.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
	}
	return c, code
}

// Each plan has one fixture account, created on the first mint, and every
// mint is a new short-lived key that authenticates as that account.
func TestJourneyFixtureMintTokenOneAccountPerPlan(t *testing.T) {
	store := state.NewMemStore()
	stderr := withJourneyFixtureStore(t, store)
	for _, tt := range []struct {
		flag string
		plan api.Plan
	}{{"free", api.PlanFree}, {"hobby", api.PlanHobby}} {
		first, code := mintJourneyFixture(t, stderr, "--plan="+tt.flag)
		if code != 0 {
			t.Fatalf("%s: mint code=%d stderr=%s", tt.flag, code, stderr)
		}
		second, _ := mintJourneyFixture(t, stderr, "--plan="+tt.flag, "--ttl=30m")
		if first.AccountID == "" || first.AccountID != second.AccountID || first.KeyID == second.KeyID {
			t.Fatalf("%s: credentials %+v %+v, want two keys of one account", tt.flag, first, second)
		}
		if time.Until(second.ExpiresAt) > 31*time.Minute {
			t.Errorf("%s: expires %v, want the 30m ttl", tt.flag, second.ExpiresAt)
		}
		account, _, err := store.AuthenticateKey(context.Background(), api.HashAPIKey(first.Token))
		if err != nil || account.ID != first.AccountID || account.Plan != tt.plan || account.Email != journeyFixtureEmail(tt.plan) {
			t.Fatalf("%s: AuthenticateKey = %+v, %v", tt.flag, account, err)
		}
	}
}

// A fixture address on another plan would test the wrong limits.
func TestJourneyFixtureRefusesAnAccountOnAnotherPlan(t *testing.T) {
	store := state.NewMemStore()
	stderr := withJourneyFixtureStore(t, store)
	if _, err := store.CreateAccount(context.Background(), journeyFixtureEmail(api.PlanFree), api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if _, code := mintJourneyFixture(t, stderr, "--plan=free"); code != 1 || !strings.Contains(stderr.String(), "not free") {
		t.Fatalf("code=%d stderr=%s, want a refusal", code, stderr)
	}
}

func TestJourneyFixtureRevokeToken(t *testing.T) {
	store := state.NewMemStore()
	stderr := withJourneyFixtureStore(t, store)
	c, code := mintJourneyFixture(t, stderr, "--plan=free")
	if code != 0 {
		t.Fatalf("mint code=%d stderr=%s", code, stderr)
	}
	osStdout = &bytes.Buffer{}
	if code := cmdJourneyFixtureDispatch([]string{"revoke-token", "--plan=free", "--key-id=" + c.KeyID, "--yes", "--reason=" + journeyFixtureReason}); code != 0 {
		t.Fatalf("revoke code=%d stderr=%s", code, stderr)
	}
	if _, _, err := store.AuthenticateKey(context.Background(), api.HashAPIKey(c.Token)); err == nil {
		t.Fatal("revoked key still authenticates")
	}
}

func TestJourneyFixtureRequiresPlanAndAcknowledgement(t *testing.T) {
	withJourneyFixtureStore(t, state.NewMemStore())
	ok := []string{"--plan=free", "--yes", "--reason=" + journeyFixtureReason}
	for _, args := range [][]string{
		nil,
		{"mint-token"},
		{"mint-token", "--plan=pro", "--yes", "--reason=" + journeyFixtureReason},
		{"mint-token", "--plan=free", "--yes"},
		{"mint-token", "--plan=free", "--reason=" + journeyFixtureReason},
		append([]string{"mint-token", "--ttl=7h"}, ok...),
		append([]string{"revoke-token"}, ok...),
		{"other"},
	} {
		if code := cmdJourneyFixtureDispatch(args); code != 2 {
			t.Errorf("%v code=%d, want 2", args, code)
		}
	}
}
