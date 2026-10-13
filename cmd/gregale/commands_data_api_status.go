// adr: 981 — inspect durable admission state without changing refresh ownership.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIStatus struct {
	App                   string `json:"app"`
	State                 string `json:"state"`
	Fingerprint           string `json:"fingerprint"`
	RegisteredFingerprint string `json:"registered_fingerprint,omitempty"`
	BaselineMatches       *bool  `json:"baseline_matches,omitempty"`
	NextAction            string `json:"next_action"`
}

func describeDataAPIStatus(app string, contract dataAPIServingContract, baseline string) (dataAPIStatus, error) {
	result := dataAPIStatus{App: app, Fingerprint: contract.Fingerprint}
	if baseline != "" {
		matches := baseline == contract.Fingerprint
		result.BaselineMatches = &matches
	}
	if contract.RefreshProtocol == 0 && contract.RefreshPaused == nil && contract.RefreshFingerprint == "" && !contract.RefreshVerified {
		result.State = "coordination_disabled"
		result.NextAction = "Install the refresh barrier and deploy a runtime with DATA_API_COORDINATED_REFRESH=1 before coordinated sync."
		return result, nil
	}
	if contract.RefreshProtocol != 1 || contract.RefreshPaused == nil || !dataAPIApprovalFingerprint(contract.RefreshFingerprint) {
		return result, errors.New("invalid refresh status; verify barrier installation and runtime support")
	}
	result.RegisteredFingerprint = contract.RefreshFingerprint
	if *contract.RefreshPaused {
		if contract.RefreshVerified {
			result.State = "paused_owned"
			result.NextAction = "Correct failed checks, then run data-api sync " + app + " --config FILE --resume-only using the same private token and baseline."
		} else {
			result.State = "paused_unowned"
			result.NextAction = "Supply the original refresh token and rerun status; do not start a new sync or adopt this pause. If the token is lost, arrange owner-controlled recovery."
		}
	} else if contract.Fingerprint != contract.RefreshFingerprint {
		result.State = "contract_mismatch"
		result.NextAction = "The runtime differs from the registered contract; verify deployment and schema changes before resuming traffic."
	} else {
		result.State = "active"
		result.NextAction = "Traffic is active for the registered serving contract. After a lost resume acknowledgement, compare this fingerprint with the verified candidate; do not rerun migrations to recover that acknowledgement."
	}
	return result, nil
}

func cmdDataAPIStatus(args []string) int {
	fs := newFlagSet("data-api status", flag.ContinueOnError)
	accessEnv := fs.String("access-token-env", "GREGALE_DATA_API_ACCESS_TOKEN", "application JWT environment variable")
	refreshEnv := fs.String("refresh-token-env", "GREGALE_DATA_API_REFRESH_TOKEN", "optional private refresh token environment variable")
	baselinePath := fs.String("baseline", "", "optional snapshot to compare with the serving contract")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || !dataAPITokenEnvName.MatchString(*accessEnv) || !dataAPITokenEnvName.MatchString(*refreshEnv) {
		PrintUsage(os.Stderr, "usage: gregale data-api status NAME [--baseline FILE] [--access-token-env ENV] [--refresh-token-env ENV]", "data-api")
		return 1
	}
	token, refresh := os.Getenv(*accessEnv), os.Getenv(*refreshEnv)
	if !validDataAPIAccessToken(token) {
		return printErr("Invalid application JWT", errors.New("configure the application JWT in access-token-env; do not use an account key"))
	}
	if refresh != "" && !dataAPIApprovalFingerprint(refresh) {
		return printErr("Invalid refresh token", errors.New("refresh-token-env must contain 64 lowercase hexadecimal characters, or be unset"))
	}
	baseline := ""
	if *baselinePath != "" {
		c, err := readDataAPIContractFile(*baselinePath)
		if err != nil {
			return printErr("Invalid baseline", err)
		}
		baseline = c.Fingerprint
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	app, err := client.GetApp(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not inspect Data API app", err)
	}
	endpoint, err := dataAPIReadinessURL(app)
	if err != nil {
		return printErr("Could not inspect Data API status", err)
	}
	contract, err := readDataAPIServingContract(ctx, endpoint, token, refresh)
	if err != nil {
		return printErr("Could not inspect Data API status", err)
	}
	result, err := describeDataAPIStatus(fs.Arg(0), contract, baseline)
	if err != nil {
		return printErr("Could not inspect Data API status", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "%s: %s\nServing fingerprint: %s\n", result.App, result.State, result.Fingerprint)
	if result.RegisteredFingerprint != "" {
		_, _ = fmt.Fprintf(osStdout, "Registered fingerprint: %s\n", result.RegisteredFingerprint)
	}
	if result.BaselineMatches != nil {
		_, _ = fmt.Fprintf(osStdout, "Baseline matches serving contract: %t\n", *result.BaselineMatches)
	}
	_, _ = fmt.Fprintln(osStdout, result.NextAction)
	return 0
}
