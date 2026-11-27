package main

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestUnitSmokeMintKeyCreatesTheAccountOnceWithScopedKeys(t *testing.T) {
	store := state.NewMemStore()
	oldOpener, oldOut, oldErr := unitSmokeStoreOpener, osStdout, osStderr
	t.Cleanup(func() { unitSmokeStoreOpener, osStdout, osStderr = oldOpener, oldOut, oldErr })
	unitSmokeStoreOpener = func() (state.Store, func(), error) { return store, func() {}, nil }
	var stderr bytes.Buffer
	osStderr = &stderr

	mint := func() unitSmokeCredential {
		t.Helper()
		var out bytes.Buffer
		osStdout = &out
		if code := cmdUnitSmokeMintKey([]string{"--yes", "--reason=" + unitSmokeReason}); code != 0 {
			t.Fatalf("mint code=%d stderr=%s", code, stderr.String())
		}
		var c unitSmokeCredential
		if err := json.Unmarshal(out.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	first, second := mint(), mint()
	if first.AccountID == "" || first.AccountID != second.AccountID {
		t.Fatalf("accounts %q and %q: want one smoke account", first.AccountID, second.AccountID)
	}
	if first.KeyID == second.KeyID || time.Until(first.ExpiresAt) < 300*24*time.Hour {
		t.Fatalf("credentials %+v %+v", first, second)
	}
	account, key, err := store.AuthenticateKey(context.Background(), api.HashAPIKey(first.Token))
	if err != nil || account.ID != first.AccountID || account.Email != unitSmokeAccountEmail {
		t.Fatalf("AuthenticateKey = %+v, %v", account, err)
	}
	if account.Plan != api.PlanHobby {
		t.Errorf("plan = %q, want hobby", account.Plan)
	}
	got := append([]string(nil), key.Scopes...)
	want := append([]string(nil), unitSmokeScopes...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("key scopes = %v, want only %v (never admin)", got, want)
	}
}

func TestUnitSmokeMintKeyRequiresAcknowledgement(t *testing.T) {
	for _, args := range [][]string{nil, {"--yes"}, {"--yes", "--reason=other"}, {"--yes", "--reason=" + unitSmokeReason, "--ttl=9000h"}} {
		if code := cmdUnitSmokeMintKey(args); code != 2 {
			t.Errorf("mint %v code=%d, want 2", args, code)
		}
	}
	if code := cmdUnitSmokeDispatch(nil); code != 2 {
		t.Errorf("dispatch without a subcommand code=%d, want 2", code)
	}
}
