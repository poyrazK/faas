package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretscan"
)

// Preview types deliberately have no value, ciphertext, or finding snippet.
type envPushPreviewChange struct {
	Key    string `json:"key"`
	Action string `json:"action"`
}

type envPushPreviewFinding struct {
	Key      string `json:"key"`
	Pair     int    `json:"pair"`
	Provider string `json:"provider"`
	Severity string `json:"severity"`
}

type envPushPreview struct {
	App            string                  `json:"app_slug"`
	Scope          string                  `json:"scope"`
	DryRun         bool                    `json:"dry_run"`
	CanPush        bool                    `json:"can_push"`
	Changes        []envPushPreviewChange  `json:"changes"`
	Findings       []envPushPreviewFinding `json:"scan_findings"`
	Blockers       []string                `json:"blockers"`
	CurrentCount   int                     `json:"current_secret_count"`
	ProjectedCount int                     `json:"projected_secret_count"`
	Quota          int                     `json:"secret_quota"`
	ApplyMode      string                  `json:"apply_mode"`
}

func previewEnvPush(app, scope string, pairs []secretsPair, origin string, mode secretScanMode, restart bool) int {
	if !api.ValidAppSlug(app) {
		return printErr("Invalid app", errors.New("pass a valid app slug"))
	}
	scope = scopeOrDefault(scope)
	if problem := api.ValidateScope(scope); problem != nil {
		return printErr("Invalid scope", errors.New(problem.Detail))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	account, err := client.Whoami(ctx)
	if err != nil {
		return printErr("Could not read account plan", err)
	}
	limits, ok := api.LimitsFor(api.Plan(account.Plan))
	if !ok {
		return printErr("Unknown account plan", errors.New("cannot calculate secret limits for this plan"))
	}
	list, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return printErr("Could not read current secret metadata", err)
	}
	preview := envPushPreview{App: app, Scope: scope, DryRun: true,
		Changes: []envPushPreviewChange{}, Findings: []envPushPreviewFinding{}, Blockers: []string{},
		CurrentCount: list.Count, ProjectedCount: list.Count, Quota: limits.SecretCountMax,
		ApplyMode: "next_cold_wake"}
	if restart {
		preview.ApplyMode = "fresh_restart_after_upload"
	}
	existing := map[string]bool{}
	for _, secret := range list.Secrets {
		if scopeOrDefault(secret.Scope) == scope {
			existing[secret.Key] = true
		}
	}
	dropped := map[int]bool{}
	if mode.isScanEnabled() {
		scanPairs := make([]secretscan.Pair, len(pairs))
		for i, pair := range pairs {
			scanPairs[i] = secretscan.Pair{Key: pair.Key, Value: pair.Value}
		}
		findings := secretscan.ScanEnvPairs(scanPairs, origin)
		for _, finding := range findings {
			key := finding.Key
			if api.ValidateSecretKey(key) != nil {
				key = "(invalid key)"
			}
			preview.Findings = append(preview.Findings, envPushPreviewFinding{Key: key, Pair: finding.Line, Provider: finding.Provider, Severity: finding.Severity.String()})
			if finding.Line >= 1 && finding.Line <= len(pairs) {
				dropped[finding.Line-1] = true
			}
		}
		if mode.isStrict() && len(findings) > 0 {
			preview.Blockers = append(preview.Blockers, "Strict secret scan rejects this upload.")
		}
	}
	kept := 0
	for i, pair := range pairs {
		if dropped[i] {
			key := pair.Key
			if api.ValidateSecretKey(key) != nil {
				key = "(invalid key)"
			}
			preview.Changes = append(preview.Changes, envPushPreviewChange{Key: key, Action: "skipped_by_scan"})
			continue
		}
		kept++
		if api.ValidateSecretKey(pair.Key) != nil {
			preview.Blockers = append(preview.Blockers, fmt.Sprintf("Input pair %d has an invalid secret key.", i+1))
			continue
		}
		if len(pair.Value) > limits.SecretValueMaxBytes {
			preview.Blockers = append(preview.Blockers, fmt.Sprintf("%s exceeds the plan's per-secret value limit.", pair.Key))
		}
		action := "update"
		if !existing[pair.Key] {
			action = "add"
			preview.ProjectedCount++
			existing[pair.Key] = true
		}
		preview.Changes = append(preview.Changes, envPushPreviewChange{Key: pair.Key, Action: action})
	}
	if kept == 0 {
		preview.Blockers = append(preview.Blockers, "All input pairs are skipped by secret scan; nothing would be uploaded.")
	}
	if preview.ProjectedCount > preview.CurrentCount && preview.ProjectedCount > preview.Quota {
		preview.Blockers = append(preview.Blockers, "Projected secret count exceeds the app quota across all scopes.")
	}
	preview.CanPush = len(preview.Blockers) == 0
	if jsonOutput {
		if code := jsonOut(writeJSON(preview)); code != 0 {
			return code
		}
	} else {
		PrintProgress(osStdout, "Env push preview: app=%s; scope=%s (values hidden)", app, scope)
		for _, change := range preview.Changes {
			_, _ = fmt.Fprintf(osStdout, "  %s  %s\n", change.Action, change.Key)
		}
		for _, finding := range preview.Findings {
			_, _ = fmt.Fprintf(osStdout, "  Scan: pair %d, %s (%s, %s)\n", finding.Pair, finding.Key, finding.Provider, finding.Severity)
		}
		PrintProgress(osStdout, "Secrets across all scopes: %d -> %d / %d", preview.CurrentCount, preview.ProjectedCount, preview.Quota)
		if restart {
			PrintProgress(osStdout, "Apply after upload: fresh app restart.")
		} else {
			PrintProgress(osStdout, "Apply after upload: next cold wake; use --restart to apply to running instances.")
		}
		for _, blocker := range preview.Blockers {
			PrintFail(osStdout, "%s", blocker)
		}
		PrintProgress(osStdout, "No secrets uploaded or restart requested. This preview uses current metadata; uploading rechecks server rules.")
	}
	if !preview.CanPush {
		return 1
	}
	return 0
}
