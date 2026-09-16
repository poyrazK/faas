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
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dispatchReleaseAcceptance     = "release-acceptance"
	releaseAcceptanceAccountEmail = "release-acceptance@gregale.invalid"
	releaseAcceptanceKeyLabel     = "production-release-acceptance"
	releaseAcceptanceReason       = "production_release_acceptance"
)

var releaseAcceptanceStoreOpener = openComputeNodesStore

type releaseAcceptanceCredential struct {
	AccountID string    `json:"account_id"`
	KeyID     string    `json:"key_id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func cmdReleaseAcceptanceDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl release-acceptance: missing subcommand; want mint-token|revoke-token")
		return 2
	}
	switch args[0] {
	case "mint-token":
		return cmdReleaseAcceptanceMintToken(args[1:])
	case "revoke-token":
		return cmdReleaseAcceptanceRevokeToken(args[1:])
	case "verify-placement":
		return cmdReleaseAcceptanceVerifyPlacement(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance: unknown subcommand %q\n", args[0])
		return 2
	}
}

type releaseAcceptancePlacementNode struct {
	Name    string `json:"name"`
	NodeID  string `json:"node_id"`
	HasApp  bool   `json:"has_app"`
	HasFunc bool   `json:"has_function"`
}

type releaseAcceptancePlacementReport struct {
	Ready bool                             `json:"ready"`
	Nodes []releaseAcceptancePlacementNode `json:"nodes"`
}

func cmdReleaseAcceptanceVerifyPlacement(args []string) int {
	fs := flag.NewFlagSet("release-acceptance verify-placement", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	slugsCSV := fs.String("slugs", "", "comma-separated acceptance app slugs")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*slugsCSV) == "" {
		fmt.Fprintln(os.Stderr, "usage: gregalectl release-acceptance verify-placement --slugs SLUG[,SLUG...]")
		return 2
	}
	st, closeFn, err := releaseAcceptanceStoreOpener()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance verify-placement: %v\n", err)
		return 1
	}
	defer closeFn()
	ctx := context.Background()
	account, err := st.AccountByEmail(ctx, releaseAcceptanceAccountEmail)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance verify-placement: account: %v\n", err)
		return 1
	}
	nodes, err := st.ListComputeNodes(ctx, false)
	if err != nil || len(nodes) == 0 {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance verify-placement: active nodes: %v\n", err)
		return 1
	}
	type coverage struct{ app, function bool }
	byNode := make(map[string]*coverage, len(nodes))
	for _, node := range nodes {
		byNode[node.ID] = &coverage{}
	}
	seen := make(map[string]struct{})
	for _, rawSlug := range strings.Split(*slugsCSV, ",") {
		slug := strings.TrimSpace(rawSlug)
		if slug == "" {
			continue
		}
		if _, duplicate := seen[slug]; duplicate {
			fmt.Fprintf(os.Stderr, "gregalectl release-acceptance verify-placement: duplicate slug %q\n", slug)
			return 1
		}
		seen[slug] = struct{}{}
		app, appErr := st.AppBySlug(ctx, slug)
		if appErr != nil || app.AccountID != account.ID {
			fmt.Fprintf(os.Stderr, "gregalectl release-acceptance verify-placement: app %q is unavailable or not acceptance-owned: %v\n", slug, appErr)
			return 1
		}
		covered, ok := byNode[app.NodeID]
		if !ok {
			fmt.Fprintf(os.Stderr, "gregalectl release-acceptance verify-placement: app %q is assigned to inactive node %q\n", slug, app.NodeID)
			return 1
		}
		switch app.Type {
		case state.AppTypeApp:
			covered.app = true
		case state.AppTypeFunction:
			covered.function = true
		default:
			fmt.Fprintf(os.Stderr, "gregalectl release-acceptance verify-placement: app %q has unexpected type %q\n", slug, app.Type)
			return 1
		}
	}
	report := releaseAcceptancePlacementReport{Ready: true, Nodes: make([]releaseAcceptancePlacementNode, 0, len(nodes))}
	for _, node := range nodes {
		covered := byNode[node.ID]
		row := releaseAcceptancePlacementNode{Name: node.Name, NodeID: node.ID, HasApp: covered.app, HasFunc: covered.function}
		report.Nodes = append(report.Nodes, row)
		if !row.HasApp || !row.HasFunc {
			report.Ready = false
		}
	}
	if err := json.NewEncoder(osStdout).Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance verify-placement: encode: %v\n", err)
		return 1
	}
	if !report.Ready {
		fmt.Fprintln(os.Stderr, "gregalectl release-acceptance verify-placement: every active node must host both an app and a function acceptance deployment")
		return 3
	}
	return 0
}

func releaseAcceptanceMutationFlags(fs *flag.FlagSet) (*bool, *string) {
	yes := fs.Bool("yes", false, "acknowledge the direct production database mutation")
	reason := fs.String("reason", "", "required fixed audit reason")
	return yes, reason
}

func validateReleaseAcceptanceMutation(yes bool, reason string) error {
	if !yes || strings.TrimSpace(reason) != releaseAcceptanceReason {
		return fmt.Errorf("--yes and --reason=%s are required", releaseAcceptanceReason)
	}
	return nil
}

func cmdReleaseAcceptanceMintToken(args []string) int {
	fs := flag.NewFlagSet("release-acceptance mint-token", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	yes, reason := releaseAcceptanceMutationFlags(fs)
	ttl := fs.Duration("ttl", 2*time.Hour, "short-lived credential lifetime")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *ttl < 15*time.Minute || *ttl > 6*time.Hour {
		fmt.Fprintln(os.Stderr, "usage: gregalectl release-acceptance mint-token --yes --reason=production_release_acceptance [--ttl=2h]")
		return 2
	}
	if err := validateReleaseAcceptanceMutation(*yes, *reason); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance mint-token: %v\n", err)
		return 2
	}

	st, closeFn, err := releaseAcceptanceStoreOpener()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance mint-token: %v\n", err)
		return 1
	}
	defer closeFn()
	ctx := context.Background()
	account, err := ensureReleaseAcceptanceAccount(ctx, st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance mint-token: account: %v\n", err)
		return 1
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance mint-token: generate token: %v\n", err)
		return 1
	}
	expiresAt := time.Now().UTC().Add(*ttl)
	key, err := st.CreateAPIKeyWithExpiry(ctx, account.ID, hash, releaseAcceptanceKeyLabel, []string{api.ScopeAdmin}, &expiresAt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance mint-token: persist token: %v\n", err)
		return 1
	}
	credential := releaseAcceptanceCredential{AccountID: account.ID, KeyID: key.ID, Token: plaintext, ExpiresAt: expiresAt}
	if err := json.NewEncoder(osStdout).Encode(credential); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance mint-token: encode: %v\n", err)
		return 1
	}
	return 0
}

func ensureReleaseAcceptanceAccount(ctx context.Context, st state.Store) (state.Account, error) {
	account, err := st.AccountByEmail(ctx, releaseAcceptanceAccountEmail)
	if err == nil {
		return account, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.Account{}, err
	}
	account, err = st.CreateAccount(ctx, releaseAcceptanceAccountEmail, api.PlanScale)
	if errors.Is(err, state.ErrConflict) {
		return st.AccountByEmail(ctx, releaseAcceptanceAccountEmail)
	}
	return account, err
}

func cmdReleaseAcceptanceRevokeToken(args []string) int {
	fs := flag.NewFlagSet("release-acceptance revoke-token", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	yes, reason := releaseAcceptanceMutationFlags(fs)
	keyID := fs.String("key-id", "", "acceptance API key id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*keyID) == "" {
		fmt.Fprintln(os.Stderr, "usage: gregalectl release-acceptance revoke-token --key-id ID --yes --reason=production_release_acceptance")
		return 2
	}
	if err := validateReleaseAcceptanceMutation(*yes, *reason); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance revoke-token: %v\n", err)
		return 2
	}

	st, closeFn, err := releaseAcceptanceStoreOpener()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance revoke-token: %v\n", err)
		return 1
	}
	defer closeFn()
	ctx := context.Background()
	account, err := st.AccountByEmail(ctx, releaseAcceptanceAccountEmail)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance revoke-token: account: %v\n", err)
		return 1
	}
	if _, err := st.MarkAPIKeyRevoked(ctx, account.ID, *keyID); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl release-acceptance revoke-token: %v\n", err)
		return 1
	}
	if jsonOutput {
		_ = json.NewEncoder(osStdout).Encode(map[string]any{"key_id": *keyID, "revoked": true})
	} else {
		fmt.Fprintf(osStdout, "revoked acceptance key %s\n", *keyID)
	}
	return 0
}
