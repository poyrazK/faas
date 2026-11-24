package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/state"
)

// The unit lane's critical smoke checks (scripts/ci/unit_smoke.py,
// docs/adr/20261122170000-continuous-unit-lane.md) run against a dedicated
// internal account, never a person's or a customer's. Like release
// acceptance, it is created through the state store, as apid creates
// accounts and keys, by an operator on the control plane.
const (
	dispatchUnitSmoke     = "unit-smoke"
	unitSmokeAccountEmail = "unit-smoke@gregale.invalid"
	unitSmokeKeyLabel     = "unit-lane-smoke"
	unitSmokeReason       = "unit_lane_smoke"
)

// unitSmokeScopes are what unit_smoke.py calls: the account and the app
// (read), the app's env (write), a fresh restart (deploy), usage (read).
var unitSmokeScopes = []string{api.ScopeAppsRead, api.ScopeEnvWrite, api.ScopeDeployWrite, api.ScopeUsageRead}

var unitSmokeStoreOpener = openComputeNodesStore

type unitSmokeCredential struct {
	AccountID string    `json:"account_id"`
	KeyID     string    `json:"key_id"`
	Token     string    `json:"token"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at"`
}

func cmdUnitSmokeDispatch(args []string) int {
	if len(args) == 0 || args[0] != "mint-key" {
		fmt.Fprintln(os.Stderr, "usage: gregalectl unit-smoke mint-key [--ttl 8760h] --yes --reason="+unitSmokeReason)
		return 2
	}
	return cmdUnitSmokeMintKey(args[1:])
}

// cmdUnitSmokeMintKey creates the smoke account (Hobby, once) and mints a
// key with the smoke scopes only, printed once as JSON.
func cmdUnitSmokeMintKey(args []string) int {
	fs := flag.NewFlagSet("unit-smoke mint-key", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	ttl := fs.Duration("ttl", 365*24*time.Hour, "key lifetime (1h..8760h)")
	yes := fs.Bool("yes", false, "acknowledge the direct production database mutation")
	reason := fs.String("reason", "", "required fixed audit reason")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	if !*yes || strings.TrimSpace(*reason) != unitSmokeReason {
		fmt.Fprintf(os.Stderr, "gregalectl unit-smoke mint-key: --yes and --reason=%s are required\n", unitSmokeReason)
		return 2
	}
	if *ttl < time.Hour || *ttl > 365*24*time.Hour {
		fmt.Fprintln(os.Stderr, "gregalectl unit-smoke mint-key: --ttl must be between 1h and 8760h")
		return 2
	}
	// Written as apid would write it, under apid's credential configuration
	// when the environment carries it (ADR 20261029202403).
	policy, err := credhash.LoadAccountPolicy(os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl unit-smoke mint-key: %v\n", err)
		return 1
	}
	credhash.SetAccount(policy)
	st, closeFn, err := unitSmokeStoreOpener()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl unit-smoke mint-key: %v\n", err)
		return 1
	}
	defer closeFn()
	ctx := context.Background()
	account, err := ensureUnitSmokeAccount(ctx, st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl unit-smoke mint-key: account: %v\n", err)
		return 1
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl unit-smoke mint-key: generate key: %v\n", err)
		return 1
	}
	expiresAt := time.Now().UTC().Add(*ttl)
	key, err := st.CreateAPIKeyWithExpiry(ctx, account.ID, hash, unitSmokeKeyLabel, unitSmokeScopes, &expiresAt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl unit-smoke mint-key: persist key: %v\n", err)
		return 1
	}
	credential := unitSmokeCredential{AccountID: account.ID, KeyID: key.ID, Token: plaintext, Scopes: unitSmokeScopes, ExpiresAt: expiresAt}
	if err := json.NewEncoder(osStdout).Encode(credential); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl unit-smoke mint-key: encode: %v\n", err)
		return 1
	}
	return 0
}

func ensureUnitSmokeAccount(ctx context.Context, st state.Store) (state.Account, error) {
	account, err := st.AccountByEmail(ctx, unitSmokeAccountEmail)
	if err == nil {
		return account, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.Account{}, err
	}
	account, err = st.CreateAccount(ctx, unitSmokeAccountEmail, api.PlanHobby)
	if errors.Is(err, state.ErrConflict) {
		return st.AccountByEmail(ctx, unitSmokeAccountEmail)
	}
	return account, err
}
