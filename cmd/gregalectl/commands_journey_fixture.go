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

// Journeys that depend on a plan's limits (tests/journeys; for example the
// Free plan's 256 MB app layer, ADR-20261124160000) run against an internal
// fixture account on that plan, never a person's or a customer's. Like the
// release-acceptance account, it is created through the state store, as
// apid creates accounts and keys, by an operator on the control plane, and
// its keys are short-lived.
const (
	dispatchJourneyFixture = "journey-fixture"
	journeyFixtureKeyLabel = "journey-fixture"
	journeyFixtureReason   = "journey_fixture"
)

// journeyFixturePlans are the plans a fixture account can have: the ones a
// person's own (paid) login cannot stand in for.
var journeyFixturePlans = map[string]api.Plan{"free": api.PlanFree, "hobby": api.PlanHobby}

var journeyFixtureStoreOpener = openComputeNodesStore

type journeyFixtureCredential struct {
	AccountID string    `json:"account_id"`
	Email     string    `json:"email"`
	Plan      api.Plan  `json:"plan"`
	KeyID     string    `json:"key_id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

const journeyFixtureUsage = "usage: gregalectl journey-fixture mint-token --plan free|hobby [--ttl 2h] --yes --reason=" + journeyFixtureReason + "\n" +
	"       gregalectl journey-fixture revoke-token --plan free|hobby --key-id ID --yes --reason=" + journeyFixtureReason

func cmdJourneyFixtureDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(osStderr, journeyFixtureUsage)
		return 2
	}
	switch args[0] {
	case "mint-token":
		return cmdJourneyFixtureMintToken(args[1:])
	case "revoke-token":
		return cmdJourneyFixtureRevokeToken(args[1:])
	default:
		fmt.Fprintln(osStderr, journeyFixtureUsage)
		return 2
	}
}

// journeyFixtureEmail is the fixture account of plan.
func journeyFixtureEmail(plan api.Plan) string {
	return "journey-" + string(plan) + "@gregale.invalid"
}

// journeyFixtureFlags parses the flags every subcommand shares and checks
// the plan and the acknowledgement.
func journeyFixtureFlags(name string, args []string, extra func(*flag.FlagSet)) (api.Plan, bool) {
	fs := flag.NewFlagSet("journey-fixture "+name, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	planName := fs.String("plan", "", "fixture plan: free or hobby")
	yes := fs.Bool("yes", false, "acknowledge the direct production database mutation")
	reason := fs.String("reason", "", "required fixed audit reason")
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(osStderr, journeyFixtureUsage)
		return "", false
	}
	plan, ok := journeyFixturePlans[*planName]
	if !ok {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture %s: --plan must be free or hobby\n", name)
		return "", false
	}
	if !*yes || strings.TrimSpace(*reason) != journeyFixtureReason {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture %s: --yes and --reason=%s are required\n", name, journeyFixtureReason)
		return "", false
	}
	return plan, true
}

// cmdJourneyFixtureMintToken creates the plan's fixture account once and
// mints a short-lived admin key for the journey suite, printed once as JSON.
func cmdJourneyFixtureMintToken(args []string) int {
	var ttl *time.Duration
	plan, ok := journeyFixtureFlags("mint-token", args, func(fs *flag.FlagSet) {
		ttl = fs.Duration("ttl", 2*time.Hour, "key lifetime (15m..6h)")
	})
	if !ok {
		return 2
	}
	if *ttl < 15*time.Minute || *ttl > 6*time.Hour {
		fmt.Fprintln(osStderr, "gregalectl journey-fixture mint-token: --ttl must be between 15m and 6h")
		return 2
	}
	// Written as apid would write it, under apid's credential configuration
	// when the environment carries it (ADR 20261029202403).
	policy, err := credhash.LoadAccountPolicy(os.Getenv)
	if err != nil {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture mint-token: %v\n", err)
		return 1
	}
	credhash.SetAccount(policy)
	st, closeFn, err := journeyFixtureStoreOpener()
	if err != nil {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture mint-token: %v\n", err)
		return 1
	}
	defer closeFn()
	ctx := context.Background()
	account, err := ensureJourneyFixtureAccount(ctx, st, plan)
	if err != nil {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture mint-token: account: %v\n", err)
		return 1
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture mint-token: generate key: %v\n", err)
		return 1
	}
	expiresAt := time.Now().UTC().Add(*ttl)
	key, err := st.CreateAPIKeyWithExpiry(ctx, account.ID, hash, journeyFixtureKeyLabel, []string{api.ScopeAdmin}, &expiresAt)
	if err != nil {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture mint-token: persist key: %v\n", err)
		return 1
	}
	credential := journeyFixtureCredential{AccountID: account.ID, Email: account.Email, Plan: account.Plan, KeyID: key.ID, Token: plaintext, ExpiresAt: expiresAt}
	if err := json.NewEncoder(osStdout).Encode(credential); err != nil {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture mint-token: encode: %v\n", err)
		return 1
	}
	return 0
}

// ensureJourneyFixtureAccount returns the plan's fixture account, creating
// it the first time. An account at that address on another plan is refused:
// the journeys would test the wrong limits.
func ensureJourneyFixtureAccount(ctx context.Context, st state.Store, plan api.Plan) (state.Account, error) {
	email := journeyFixtureEmail(plan)
	account, err := st.AccountByEmail(ctx, email)
	if errors.Is(err, state.ErrNotFound) {
		account, err = st.CreateAccount(ctx, email, plan)
		if errors.Is(err, state.ErrConflict) {
			account, err = st.AccountByEmail(ctx, email)
		}
	}
	if err != nil {
		return state.Account{}, err
	}
	if account.Plan != plan {
		return state.Account{}, fmt.Errorf("%s is on the %s plan, not %s", email, account.Plan, plan)
	}
	return account, nil
}

func cmdJourneyFixtureRevokeToken(args []string) int {
	var keyID *string
	plan, ok := journeyFixtureFlags("revoke-token", args, func(fs *flag.FlagSet) {
		keyID = fs.String("key-id", "", "fixture API key id")
	})
	if !ok {
		return 2
	}
	if strings.TrimSpace(*keyID) == "" {
		fmt.Fprintln(osStderr, "gregalectl journey-fixture revoke-token: --key-id is required")
		return 2
	}
	st, closeFn, err := journeyFixtureStoreOpener()
	if err != nil {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture revoke-token: %v\n", err)
		return 1
	}
	defer closeFn()
	ctx := context.Background()
	account, err := st.AccountByEmail(ctx, journeyFixtureEmail(plan))
	if err != nil {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture revoke-token: account: %v\n", err)
		return 1
	}
	if _, err := st.MarkAPIKeyRevoked(ctx, account.ID, *keyID); err != nil {
		fmt.Fprintf(osStderr, "gregalectl journey-fixture revoke-token: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(osStdout, "revoked journey fixture key %s\n", *keyID); err != nil {
		return 1
	}
	return 0
}
