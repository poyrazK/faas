// adr: 981 — durable database admission barrier for coordinated schema updates.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIRefreshVerification struct {
	token  string
	paused bool
}
type dataAPISyncCoordination struct {
	SetupCheck          *dataAPISyncCommand        `json:"setup_check,omitempty"`
	Pause               dataAPISyncCommand         `json:"pause"`
	Resume              dataAPISyncCommand         `json:"resume"`
	Authorization       dataAPISyncCommand         `json:"authorization,omitempty"`
	AuthorizationReplay *dataAPIHostedReplayConfig `json:"authorization_replay,omitempty"`
	Baseline            string                     `json:"baseline"`
	Approval            string                     `json:"approval,omitempty"`
	TokenEnv            string                     `json:"token_env,omitempty"`
	token               string
	baseline            dataAPIContract
	approval            *dataAPIApproval
	recoveryOnly        bool
}

func prepareDataAPICoordination(base string, c *dataAPISyncCoordination) error {
	steps := []*dataAPISyncCommand{&c.Pause, &c.Resume}
	if c.AuthorizationReplay != nil {
		if c.AuthorizationReplay.RefreshTokenEnv != "" && c.AuthorizationReplay.RefreshTokenEnv != "GREGALE_DATA_API_REFRESH_TOKEN" {
			return errors.New("authorization_replay uses the coordinator token_env; omit refresh_token_env")
		}
		if len(c.Authorization.Command) > 0 || c.Authorization.Directory != "" {
			return errors.New("choose authorization or authorization_replay, not both")
		}
		if err := prepareDataAPIHostedReplay(base, c.AuthorizationReplay, c.TokenEnv); err != nil {
			return err
		}
	} else {
		steps = append(steps, &c.Authorization)
	}
	for _, step := range steps {
		if err := prepareDataAPISyncCommand(base, step); err != nil {
			return fmt.Errorf("coordination command: %w", err)
		}
	}
	if c.SetupCheck != nil {
		if err := prepareDataAPISyncCommand(base, c.SetupCheck); err != nil {
			return fmt.Errorf("coordination setup_check: %w", err)
		}
	}
	if c.Baseline == "" {
		return errors.New("coordination requires a saved baseline contract")
	}
	var err error
	c.Baseline = dataAPISyncPath(base, c.Baseline)
	c.baseline, err = readDataAPIContractFile(c.Baseline)
	if err != nil {
		return fmt.Errorf("coordination baseline: %w", err)
	}
	if c.Approval != "" {
		c.Approval = dataAPISyncPath(base, c.Approval)
		c.approval, err = readDataAPIApprovalFile(c.Approval)
		if err != nil {
			return err
		}
	}
	if c.TokenEnv == "" {
		c.TokenEnv = "GREGALE_DATA_API_REFRESH_TOKEN"
	}
	if !dataAPITokenEnvName.MatchString(c.TokenEnv) {
		return errors.New("invalid coordination token_env")
	}
	c.token = os.Getenv(c.TokenEnv)
	if !dataAPIApprovalFingerprint(c.token) {
		return errors.New("coordination requires a private 64-character lowercase hexadecimal refresh token in token_env")
	}
	return nil
}

func runCoordinatedDataAPISync(ctx context.Context, client *api.Client, slug string, config dataAPISyncConfig) (dataAPISyncReceipt, error) {
	c := config.Coordination
	receipt := dataAPISyncReceipt{App: slug, Output: config.Output, RecoveryOnly: c.recoveryOnly}
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return receipt, err
	}
	healthURL, err := dataAPIReadinessURL(app)
	if err != nil {
		return receipt, err
	}
	env := map[string]string{"GREGALE_DATA_API_REFRESH_TOKEN": c.token}
	paused := &dataAPIRefreshVerification{token: c.token, paused: true}
	if config.checkOnly && c.SetupCheck == nil {
		return receipt, errors.New("--check requires coordination.setup_check; configure the read-only refresh.mjs check command")
	}
	// Validate runtime support and pause ownership before invoking any owner command.
	expected := c.baseline.Fingerprint
	if c.recoveryOnly {
		expected = ""
	}
	if err := verifyDataAPIServingContract(ctx, healthURL, config.accessToken, expected, &dataAPIRefreshVerification{token: c.token, paused: c.recoveryOnly}); err != nil {
		return receipt, err
	}

	if c.recoveryOnly {
		receipt.RefreshPaused = true
	}
	if c.SetupCheck != nil {
		dataAPISyncProgress("Checking owner privileges and refresh barrier installation")
		if err := runDataAPISyncCommandEnv(ctx, *c.SetupCheck, env); err != nil {
			return receipt, fmt.Errorf("refresh setup check: %w", err)
		}
	}
	if c.AuthorizationReplay != nil {
		if err := runDataAPIHostedReplay(ctx, c.AuthorizationReplay, healthURL, "", true, true, env); err != nil {
			return receipt, fmt.Errorf("authorization replay preflight: %w", err)
		}
	}
	if !c.recoveryOnly {
		task, err := generateDataAPIContract(ctx, client, slug)
		if err != nil {
			return receipt, err
		}
		before, err := parseDataAPIContract([]byte(task.StdoutTail))
		if err != nil || before.Fingerprint != c.baseline.Fingerprint {
			return receipt, errors.New("live schema differs from coordination baseline; refresh and review the baseline before migrating")
		}
		if err := verifyDataAPIServingContract(ctx, healthURL, config.accessToken, c.baseline.Fingerprint, &dataAPIRefreshVerification{token: c.token, paused: false}); err != nil {
			return receipt, err
		}
		if config.checkOnly {
			receipt.Status = "checked"
			receipt.Fingerprint = c.baseline.Fingerprint
			receipt.ContractVerified = true
			return receipt, nil
		}
		dataAPISyncProgress("Pausing and draining Data API transactions")
		if err := runDataAPISyncCommandEnv(ctx, c.Pause, env); err != nil {
			return receipt, fmt.Errorf("pause command (barrier may already be paused): %w", err)
		}
	}
	// Recovery proves ownership before any refresh or release operation.
	if err := verifyDataAPIServingContract(ctx, healthURL, config.accessToken, "", paused); err != nil {
		return receipt, err
	}
	receipt.RefreshPaused = true
	if config.checkOnly {
		receipt.Status = "checked"
		return receipt, nil
	}
	if !c.recoveryOnly {
		if err := runDataAPISyncCommandEnv(ctx, config.Migrate, env); err != nil {
			return receipt, fmt.Errorf("migration command: %w", err)
		}
		if config.Permissions != nil {
			if err := runDataAPISyncCommandEnv(ctx, *config.Permissions, env); err != nil {
				return receipt, fmt.Errorf("permission setup: %w", err)
			}
		}
	}
	dataAPISyncProgress("Refreshing the engine while the durable barrier remains paused")
	restart, err := client.RestartAppFresh(ctx, slug)
	receipt.WakeID = restart.WakeID
	if err != nil {
		return receipt, err
	}
	if err := waitDataAPIRefresh(ctx, client, slug, receipt.WakeID, healthURL, time.Second); err != nil {
		return receipt, err
	}
	task, err := generateDataAPIContract(ctx, client, slug)
	receipt.TaskID, receipt.DeploymentID = task.ID, task.DeploymentID
	if err != nil {
		return receipt, err
	}
	current, err := parseDataAPIContract([]byte(task.StdoutTail))
	if err != nil {
		return receipt, err
	}
	if err := checkDataAPIRefreshCompatibility(c, current); err != nil {
		return receipt, err
	}
	types, err := generateDataAPITypes(ctx, client, slug)
	receipt.TaskID, receipt.DeploymentID = types.ID, types.DeploymentID
	if err != nil {
		return receipt, err
	}
	receipt.Fingerprint = dataAPIFingerprint.FindStringSubmatch(types.StdoutTail)[1]
	if receipt.Fingerprint != current.Fingerprint {
		return receipt, errors.New("schema changed during refresh verification")
	}
	if err := verifyDataAPIServingContract(ctx, healthURL, config.accessToken, receipt.Fingerprint, paused); err != nil {
		return receipt, err
	}
	receipt.ContractVerified = true
	if err := writeDataAPITypes(config.Output, false, types.StdoutTail); err != nil {
		return receipt, err
	}
	env["GREGALE_DATA_API_REFRESH_FINGERPRINT"] = receipt.Fingerprint
	checks := []dataAPISyncCommand{config.Check}
	if c.AuthorizationReplay == nil {
		checks = append(checks, c.Authorization)
	}
	for _, step := range checks {
		if err := runDataAPISyncCommandEnv(ctx, step, env); err != nil {
			return receipt, fmt.Errorf("refresh verification command: %w", err)
		}
	}
	if c.AuthorizationReplay != nil {
		dataAPISyncProgress("Replaying required hosted authorization scenarios")
		if err := runDataAPIHostedReplay(ctx, c.AuthorizationReplay, healthURL, receipt.Fingerprint, false, true, env); err != nil {
			return receipt, fmt.Errorf("authorization replay: %w", err)
		}
	}
	// Recheck the exact immutable serving catalog and ownership after application checks.
	if err := verifyDataAPIServingContract(ctx, healthURL, config.accessToken, receipt.Fingerprint, paused); err != nil {
		return receipt, err
	}
	dataAPISyncProgress("Releasing the verified refresh barrier")
	receipt.ResumeRequested = true
	if err := runDataAPISyncCommandEnv(ctx, c.Resume, env); err != nil {
		return receipt, fmt.Errorf("resume command: %w", err)
	}
	if err := verifyDataAPIServingContract(ctx, healthURL, config.accessToken, receipt.Fingerprint, &dataAPIRefreshVerification{token: c.token, paused: false}); err != nil {
		return receipt, err
	}
	receipt.RefreshPaused = false
	receipt.Status = "completed"
	return receipt, nil
}

func checkDataAPIRefreshCompatibility(c *dataAPISyncCoordination, current dataAPIContract) error {
	changes := diffDataAPISnapshots(c.baseline.Parsed, current.Parsed)
	if err := applyDataAPIApproval(c.approval, c.baseline.Fingerprint, current.Fingerprint, changes); err != nil {
		return err
	}
	for _, change := range changes {
		if change.Breaking && !change.Approved {
			return errors.New("schema has unapproved breaking changes; review the diff before --resume-only recovery")
		}
	}
	return nil
}
