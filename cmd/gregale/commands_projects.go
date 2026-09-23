package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjects(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale projects <list|info|update|environments|rm>", "projects")
		return 1
	}
	switch args[0] {
	case "list", "ls":
		return cmdProjectsList(args[1:])
	case "info":
		return cmdProjectsInfo(args[1:])
	case "update":
		return cmdProjectsUpdate(args[1:])
	case "environments", "envs":
		return cmdProjectsEnvironments(args[1:])
	case "rm", "delete":
		return cmdProjectsRemove(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown projects subcommand %q", args[0]), "projects")
		return 1
	}
}

func cmdProjectsEnvironments(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale projects environments <list|create|protect|unprotect|releases|history|config|diff|preview|promote|status|rollback>", "projects environments")
		return 1
	}
	switch args[0] {
	case "list", "ls":
		return cmdProjectsEnvironmentsList(args[1:])
	case "create":
		return cmdProjectsEnvironmentCreate(args[1:])
	case "protect":
		return cmdProjectsEnvironmentProtection(args[1:], true)
	case "unprotect":
		return cmdProjectsEnvironmentProtection(args[1:], false)
	case "releases", "release":
		return cmdProjectsEnvironmentReleases(args[1:])
	case "history":
		return cmdProjectsEnvironmentHistory(args[1:])
	case "config":
		return cmdProjectsEnvironmentConfig(args[1:])
	case "diff":
		return cmdProjectsEnvironmentConfigDiff(args[1:])
	case "preview", "promotion-preview":
		return cmdProjectsEnvironmentPromotionPreview(args[1:])
	case "promote":
		return cmdProjectsEnvironmentPromote(args[1:])
	case "status":
		return cmdProjectsEnvironmentPromotionStatus(args[1:])
	case "rollback":
		return cmdProjectsEnvironmentPromotionRollback(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown project environments subcommand %q", args[0]), "projects environments")
		return 1
	}
}

func cmdProjectsEnvironmentReleases(args []string) int {
	if len(args) != 2 || !api.ValidProjectSlug(args[0]) || !api.ValidProjectEnvironmentSlug(args[1]) {
		PrintUsage(os.Stderr, "usage: gregale projects environments releases <project-slug> <environment-slug>", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	releases, err := client.GetProjectEnvironmentReleases(context.Background(), args[0], args[1])
	if err != nil {
		return printErr("Could not load environment releases", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(releases))
	}
	_, _ = fmt.Fprintf(osStdout, "Environment releases %s/%s\n%-24s %-14s %-36s %-18s %s\n", releases.ProjectSlug, releases.Environment, "WORKLOAD", "STATUS", "DEPLOYMENT", "BUILD", "COMMIT")
	for _, workload := range releases.Workloads {
		_, _ = fmt.Fprintf(osStdout, "%-24s %-14s %-36s %-18s %s\n", workload.WorkloadSlug, workload.Status, workload.DeploymentID, workload.BuildID, workload.CommitSHA)
	}
	return 0
}

func cmdProjectsEnvironmentHistory(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("projects-environments-history", flag.ContinueOnError)
	from := fs.String("from", "", "source environment filter")
	status := fs.String("status", "", "promotion status filter: running|succeeded|failed")
	before := fs.String("before", "", "opaque cursor from a previous page")
	limit := fs.Int("limit", 50, "page size (1-100)")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) {
		PrintUsage(os.Stderr, "usage: gregale projects environments history <project-slug> <environment-slug> [--from <environment>] [--status running|succeeded|failed] [--before <CURSOR>] [--limit <N>]", "projects environments")
		return 1
	}
	if *from != "" && !api.ValidProjectEnvironmentSlug(*from) {
		return printErr("Invalid source environment", fmt.Errorf("--from must be a lowercase project environment slug"))
	}
	if *status != "" && *status != "running" && *status != "succeeded" && *status != "failed" {
		return printErr("Invalid promotion status", fmt.Errorf("--status must be running, succeeded, or failed"))
	}
	if *limit < 1 || *limit > 100 {
		return printErr("Invalid history limit", fmt.Errorf("--limit must be between 1 and 100"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	history, err := client.ListProjectEnvironmentPromotions(context.Background(), positional[0], positional[1], *before, *limit, *from, *status)
	if err != nil {
		return printErr("Could not load promotion history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(history))
	}
	_, _ = fmt.Fprintf(osStdout, "Promotion history %s/%s\n%-36s %-12s %-16s %-16s %s\n", positional[0], positional[1], "PROMOTION", "STATUS", "FROM", "TO", "CREATED")
	for _, promotion := range history.Items {
		_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %-16s %-16s %s\n", promotion.PromotionID, promotion.Status, promotion.FromEnvironment, promotion.ToEnvironment, promotion.CreatedAt)
	}
	if history.NextBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "next_before: %s\n", history.NextBefore)
	}
	return 0
}

func cmdProjectsEnvironmentConfig(args []string) int {
	if len(args) > 0 && (args[0] == "set" || args[0] == "apply") {
		return cmdProjectsEnvironmentConfigSet(args[1:])
	}
	if len(args) != 2 || !api.ValidProjectSlug(args[0]) || !api.ValidProjectEnvironmentSlug(args[1]) {
		PrintUsage(os.Stderr, "usage: gregale projects environments config <project-slug> <environment-slug> | config set <project-slug> <environment-slug> (--file PATH|--stdin) [--dry-run] [--if-hash HASH] [--yes]", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	config, err := client.GetProjectEnvironmentConfig(context.Background(), args[0], args[1])
	if err != nil {
		return printErr("Could not load environment config", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(config))
	}
	_, _ = fmt.Fprintf(osStdout, "Environment config %s/%s\n  version: %d\n  hash: %s\n  updated: %s\n  values: %s\n", config.ProjectSlug, config.Environment, config.Version, config.ConfigHash, config.UpdatedAt, config.Values)
	return 0
}

type projectEnvironmentConfigChangePreview struct {
	Key    string          `json:"key"`
	Kind   string          `json:"kind"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

type projectEnvironmentConfigApplyPreview struct {
	ProjectSlug    string                                  `json:"project_slug"`
	Environment    string                                  `json:"environment"`
	CurrentVersion int64                                   `json:"current_version"`
	CurrentHash    string                                  `json:"current_hash"`
	NextHash       string                                  `json:"next_hash"`
	Changes        []projectEnvironmentConfigChangePreview `json:"changes"`
}

func cmdProjectsEnvironmentConfigSet(args []string) int {
	flags, positional := splitArgsForFlags(args, "stdin", "dry-run", "yes")
	fs := newFlagSet("projects-environments-config-set", flag.ContinueOnError)
	file := fs.String("file", "", "JSON configuration file")
	fromStdin := fs.Bool("stdin", false, "read JSON configuration from stdin")
	dryRun := fs.Bool("dry-run", false, "preview the change without writing it")
	expectedHash := fs.String("if-hash", "", "only apply if the current config hash matches HASH")
	yes := fs.Bool("yes", false, "confirm the configuration update")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) {
		PrintUsage(os.Stderr, "usage: gregale projects environments config set <project-slug> <environment-slug> (--file PATH|--stdin) [--dry-run] [--if-hash HASH] [--yes]", "projects environments")
		return 1
	}
	if (*file == "") == !*fromStdin {
		return printErr("Invalid config source", errors.New("exactly one of --file or --stdin is required"))
	}
	raw, err := readProjectEnvironmentConfigInput(*file, *fromStdin)
	if err != nil {
		return printErr("Could not read environment config", err)
	}
	canonical, nextHash, err := api.NormalizeProjectEnvironmentConfig(raw)
	if err != nil {
		return printErr("Invalid environment config", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	projectSlug, environmentSlug := positional[0], positional[1]
	current, err := client.GetProjectEnvironmentConfig(context.Background(), projectSlug, environmentSlug)
	if err != nil {
		return printErr("Could not load environment config", err)
	}
	currentHash := current.ConfigHash
	if currentHash == "" {
		currentHash = api.EmptyProjectEnvironmentConfigHash()
	}
	if want := strings.TrimSpace(*expectedHash); want != "" && want != currentHash {
		return printErr("Environment config changed", fmt.Errorf("current hash is %s, expected %s; review the diff and retry", currentHash, want))
	}
	changes, err := projectEnvironmentConfigChanges(current.Values, canonical)
	if err != nil {
		return printErr("Could not compare environment config", err)
	}
	preview := projectEnvironmentConfigApplyPreview{
		ProjectSlug: projectSlug, Environment: environmentSlug,
		CurrentVersion: current.Version, CurrentHash: currentHash,
		NextHash: nextHash, Changes: changes,
	}
	if *dryRun {
		return renderProjectEnvironmentConfigPreview(preview)
	}
	if len(changes) == 0 {
		if jsonOutput {
			return jsonOut(writeJSON(current))
		}
		_, _ = fmt.Fprintf(osStdout, "Environment config %s/%s is already at hash %s\n", projectSlug, environmentSlug, currentHash)
		return 0
	}
	if !*yes {
		if jsonOutput {
			if code := jsonOut(writeJSON(preview)); code != 0 {
				return code
			}
			return printErr("Confirmation required", errors.New("environment config update requires --yes in JSON mode"))
		}
		if !stdoutIsTTY() || !stdinIsTTY() {
			return printErr("Confirmation required", errors.New("environment config update requires --yes when stdin or stdout is not a TTY"))
		}
		_, _ = fmt.Fprintf(osStdout, "Apply %d config change(s) to %s/%s? [y/N] ", len(changes), projectSlug, environmentSlug)
		line, readErr := readConfirmationLine(osStdin)
		if readErr != nil || (strings.ToLower(strings.TrimSpace(line)) != "y" && strings.ToLower(strings.TrimSpace(line)) != "yes") {
			return printErr("Aborted by user", errors.New("environment config update was not confirmed"))
		}
	}
	updated, err := client.UpdateProjectEnvironmentConfig(context.Background(), projectSlug, environmentSlug, api.UpdateProjectEnvironmentConfigRequest{Values: canonical})
	if err != nil {
		return printErr("Could not update environment config", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	_, _ = fmt.Fprintf(osStdout, "Updated environment config %s/%s to version %d (%s)\n", updated.ProjectSlug, updated.Environment, updated.Version, updated.ConfigHash)
	return 0
}

func readProjectEnvironmentConfigInput(path string, fromStdin bool) ([]byte, error) {
	limit := int64(api.MaxProjectEnvironmentConfigBytes) + 1
	if fromStdin {
		return io.ReadAll(io.LimitReader(osStdin, limit))
	}
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, limit))
}

func projectEnvironmentConfigChanges(beforeRaw, afterRaw []byte) ([]projectEnvironmentConfigChangePreview, error) {
	decode := func(raw []byte) (map[string]json.RawMessage, error) {
		values := map[string]json.RawMessage{}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" || trimmed == "null" {
			return values, nil
		}
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
		return values, nil
	}
	before, err := decode(beforeRaw)
	if err != nil {
		return nil, err
	}
	after, err := decode(afterRaw)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(before))
	seen := make(map[string]struct{}, len(before))
	for key := range before {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range after {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	changes := make([]projectEnvironmentConfigChangePreview, 0, len(keys))
	for _, key := range keys {
		beforeValue, beforeOK := before[key]
		afterValue, afterOK := after[key]
		if beforeOK && afterOK && bytes.Equal(bytes.TrimSpace(beforeValue), bytes.TrimSpace(afterValue)) {
			continue
		}
		change := projectEnvironmentConfigChangePreview{Key: key, Before: beforeValue, After: afterValue}
		switch {
		case !beforeOK:
			change.Kind = "added"
		case !afterOK:
			change.Kind = "removed"
		default:
			change.Kind = "changed"
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func renderProjectEnvironmentConfigPreview(preview projectEnvironmentConfigApplyPreview) int {
	if jsonOutput {
		return jsonOut(writeJSON(preview))
	}
	_, _ = fmt.Fprintf(osStdout, "Environment config preview %s/%s\n  current: v%d %s\n  next:    %s\n", preview.ProjectSlug, preview.Environment, preview.CurrentVersion, preview.CurrentHash, preview.NextHash)
	if len(preview.Changes) == 0 {
		_, _ = fmt.Fprintln(osStdout, "  no changes")
		return 0
	}
	for _, change := range preview.Changes {
		_, _ = fmt.Fprintf(osStdout, "  %-24s %-8s before=%s after=%s\n", change.Key, change.Kind, change.Before, change.After)
	}
	return 0
}

func cmdProjectsEnvironmentConfigDiff(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("projects-environments-diff", flag.ContinueOnError)
	from := fs.String("from", "", "source environment")
	to := fs.String("to", "", "target environment")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(*from) || !api.ValidProjectEnvironmentSlug(*to) {
		PrintUsage(os.Stderr, "usage: gregale projects environments diff <project-slug> --from <environment> --to <environment>", "projects environments")
		return 1
	}
	if *from == *to {
		return printErr("Invalid environments", fmt.Errorf("--from and --to must be different"))
	}
	return runProjectEnvironmentDiff(positional[0], *from, *to)
}

func runProjectEnvironmentDiff(project, from, to string) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	diff, err := client.GetProjectEnvironmentDiff(context.Background(), project, to, from)
	if err != nil {
		return printErr("Could not load environment diff", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(diff))
	}
	renderProjectEnvironmentDiff(diff)
	return 0
}

func renderProjectEnvironmentDiff(diff api.ProjectEnvironmentDiffResponse) {
	_, _ = fmt.Fprintf(osStdout, "Environment diff %s: %s -> %s\n\nCONFIGURATION\n  versions: %d -> %d\n  hashes: %s -> %s\n", diff.ProjectSlug, diff.FromEnvironment, diff.ToEnvironment, diff.Configuration.FromVersion, diff.Configuration.ToVersion, diff.Configuration.FromHash, diff.Configuration.ToHash)
	if len(diff.Configuration.Changes) == 0 {
		_, _ = fmt.Fprintln(osStdout, "  no changes")
	}
	for _, change := range diff.Configuration.Changes {
		_, _ = fmt.Fprintf(osStdout, "  %-24s %-8s before=%s after=%s\n", change.Key, change.Kind, change.Before, change.After)
	}
	for _, workload := range diff.Workloads {
		_, _ = fmt.Fprintf(osStdout, "\nAPPLICATION %s\n  release: %-9s %s -> %s\n", workload.WorkloadSlug, workload.Release.Kind, releaseSummary(workload.Release.Before), releaseSummary(workload.Release.After))
		for _, change := range workload.Variables {
			_, _ = fmt.Fprintf(osStdout, "  variable %-20s %-8s %s -> %s\n", change.Key, change.Kind, optionalString(change.Before), optionalString(change.After))
		}
		for _, change := range workload.Secrets {
			_, _ = fmt.Fprintf(osStdout, "  secret   %-20s %-8s %s -> %s\n", change.Key, change.Kind, secretCellSummary(change.Before), secretCellSummary(change.After))
		}
		for _, change := range workload.Bindings {
			_, _ = fmt.Fprintf(osStdout, "  binding  %-20s %-8s %s\n", change.BindingID, change.Change, change.Kind)
		}
	}
	_, _ = fmt.Fprintln(osStdout, "\nSHARED (not environment-scoped)")
	for _, resource := range diff.SharedResources {
		_, _ = fmt.Fprintf(osStdout, "  %s\n", resource.Kind)
	}
}

func releaseSummary(release api.ProjectEnvironmentReleaseWorkloadResponse) string {
	for _, identity := range []string{release.ImageDigest, release.SourceSHA256, release.CommitSHA, release.BuildID, release.DeploymentID} {
		if identity != "" {
			return identity
		}
	}
	return "<not deployed>"
}

func optionalString(value *string) string {
	if value == nil {
		return "<missing>"
	}
	return *value
}

func secretCellSummary(cell api.ProjectEnvironmentSecretCellResponse) string {
	if !cell.Present {
		return "<missing>"
	}
	if cell.CredentialGeneration > 0 {
		return fmt.Sprintf("generation %d", cell.CredentialGeneration)
	}
	if cell.ValueHash == "" {
		return "present (fingerprint unavailable)"
	}
	return "fingerprint " + cell.ValueHash
}

func cmdProjectsEnvironmentPromote(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes", "idempotency-key", "wait", "progress")
	fs := newFlagSet("projects-environments-promote", flag.ContinueOnError)
	from := fs.String("from", "", "source environment")
	to := fs.String("to", "", "target environment")
	yes := fs.Bool("yes", false, "confirm the promotion")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for retrying this promotion")
	wait := fs.Bool("wait", false, "wait for the promotion to reach a terminal status")
	progress := fs.Bool("progress", false, "print promotion transitions while waiting (human output only)")
	timeoutSeconds := fs.Int("timeout", defaultDeployWaitTimeoutSeconds, "maximum seconds to wait for promotion completion")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(*from) || !api.ValidProjectEnvironmentSlug(*to) {
		PrintUsage(os.Stderr, "usage: gregale projects environments promote <project-slug> --from <environment> --to <environment> [--yes] [--idempotency-key <KEY>] [--wait] [--progress] [--timeout SECONDS]", "projects environments")
		return 1
	}
	if *progress && !*wait {
		return printErr("Invalid wait options", errors.New("--progress requires --wait"))
	}
	if *timeoutSeconds <= 0 || *timeoutSeconds > 24*60*60 {
		return printErr("Invalid wait timeout", errors.New("--timeout must be between 1 and 86400 seconds"))
	}
	if *from == *to {
		return printErr("Invalid environments", fmt.Errorf("--from and --to must be different"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.GetProjectEnvironmentPromotionPreview(context.Background(), positional[0], *to, *from)
	if err != nil {
		return printErr("Promotion preview failed", err)
	}
	if !preview.CanPromote {
		if jsonOutput {
			return jsonOut(writeJSON(preview))
		}
		return printErr("Promotion is blocked", errors.New(strings.Join(preview.BlockingReasons, "; ")))
	}
	if !*yes {
		if jsonOutput {
			if code := jsonOut(writeJSON(preview)); code != 0 {
				return code
			}
			return printErr("Confirmation required", errors.New("project environment promotion requires --yes in JSON mode"))
		}
		if stdoutIsTTY() && stdinIsTTY() {
			_, _ = fmt.Fprintf(osStdout, "Promote %s: %s -> %s (%d workload changes)? [y/N] ", preview.ProjectSlug, preview.FromEnvironment, preview.ToEnvironment, promotionChangeCount(preview))
			line, readErr := readConfirmationLine(osStdin)
			if readErr != nil || (strings.ToLower(strings.TrimSpace(line)) != "y" && strings.ToLower(strings.TrimSpace(line)) != "yes") {
				return printErr("Aborted by user", errors.New("promotion was not confirmed"))
			}
		} else {
			return printErr("Confirmation required", errors.New("project environment promotion requires --yes when stdin or stdout is not a TTY"))
		}
	}
	approvalToken := ""
	key := strings.TrimSpace(*idempotencyKey)
	if key == "" {
		digest := sha256.Sum256([]byte("project-environment-promotion\x00" + preview.PromotionToken))
		key = "project-promotion-" + hex.EncodeToString(digest[:])
	}
	if preview.ApprovalRequired {
		approvalCtx := api.ContextWithIdempotencyKey(context.Background(), key+"-approval")
		approval, approvalErr := client.ApproveProjectEnvironment(approvalCtx, positional[0], *to,
			api.CreateProjectEnvironmentApprovalRequest{PromotionToken: preview.PromotionToken})
		if approvalErr != nil {
			return printErr("Protected environment approval failed", approvalErr)
		}
		approvalToken = approval.ApprovalToken
	}
	executeCtx := api.ContextWithIdempotencyKey(context.Background(), key)
	promoted, err := client.PromoteProjectEnvironment(executeCtx, positional[0], *to, api.PromoteProjectEnvironmentRequest{
		FromEnvironment: *from, PromotionToken: preview.PromotionToken, ApprovalToken: approvalToken,
	})
	if err != nil {
		return printErr("Promotion failed", err)
	}
	if *wait {
		initial := api.ProjectEnvironmentPromotionStatusResponse{
			PromotionID: promoted.PromotionID, ProjectSlug: promoted.ProjectSlug,
			FromEnvironment: promoted.FromEnvironment, ToEnvironment: promoted.ToEnvironment,
			PromotionHash: promoted.PromotionHash, Status: "running",
		}
		if !jsonOutput {
			PrintProgress(osStdout, "Waiting for promotion %s to finish...", promoted.PromotionID)
		}
		var lastProgress string
		var onProgress func(api.ProjectEnvironmentPromotionStatusResponse)
		if *progress && !jsonOutput {
			onProgress = func(status api.ProjectEnvironmentPromotionStatusResponse) {
				key := projectEnvironmentPromotionProgressKey(status)
				if key == lastProgress {
					return
				}
				lastProgress = key
				renderProjectEnvironmentPromotionProgress(status)
			}
		}
		status, timedOut, waitErr := waitForProjectEnvironmentPromotion(
			context.Background(), client, promoted.ProjectSlug, promoted.ToEnvironment, promoted.PromotionID,
			time.Duration(*timeoutSeconds)*time.Second, initial, onProgress,
		)
		if waitErr != nil {
			return printErr("Promotion status failed", waitErr)
		}
		return renderProjectEnvironmentPromotionWait(status, timedOut, time.Duration(*timeoutSeconds)*time.Second)
	}
	if jsonOutput {
		return jsonOut(writeJSON(promoted))
	}
	_, _ = fmt.Fprintf(osStdout, "Promoted %s: %s -> %s\n", promoted.ProjectSlug, promoted.FromEnvironment, promoted.ToEnvironment)
	for _, workload := range promoted.Workloads {
		_, _ = fmt.Fprintf(osStdout, "  %-20s %s\n", workload.WorkloadSlug, workload.Status)
	}
	return 0
}

func cmdProjectsEnvironmentPromotionStatus(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("projects-environments-status", flag.ContinueOnError)
	to := fs.String("to", "", "target environment")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 || !api.ValidProjectSlug(positional[0]) || strings.TrimSpace(positional[1]) == "" || !api.ValidProjectEnvironmentSlug(*to) {
		PrintUsage(os.Stderr, "usage: gregale projects environments status <project-slug> <promotion-id> --to <environment>", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	status, err := client.GetProjectEnvironmentPromotionStatus(context.Background(), positional[0], *to, positional[1])
	if err != nil {
		return printErr("Promotion status failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	renderProjectEnvironmentPromotionStatus(status)
	return 0
}

func cmdProjectsEnvironmentPromotionRollback(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes", "idempotency-key")
	fs := newFlagSet("projects-environments-rollback", flag.ContinueOnError)
	to := fs.String("to", "", "target environment")
	yes := fs.Bool("yes", false, "confirm the rollback")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for retrying this rollback")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 || !api.ValidProjectSlug(positional[0]) || strings.TrimSpace(positional[1]) == "" || !api.ValidProjectEnvironmentSlug(*to) {
		PrintUsage(os.Stderr, "usage: gregale projects environments rollback <project-slug> <promotion-id> --to <environment> [--yes] [--idempotency-key <KEY>]", "projects environments")
		return 1
	}
	if !*yes {
		return printErr("Confirmation required", errors.New("project environment rollback requires --yes"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	key := strings.TrimSpace(*idempotencyKey)
	if key == "" {
		digest := sha256.Sum256([]byte("project-environment-promotion-rollback\x00" + positional[0] + "\x00" + *to + "\x00" + positional[1]))
		key = "project-rollback-" + hex.EncodeToString(digest[:])
	}
	rollbackCtx := api.ContextWithIdempotencyKey(context.Background(), key)
	status, err := client.RollbackProjectEnvironmentPromotion(rollbackCtx, positional[0], *to, positional[1])
	if err != nil {
		return printErr("Promotion rollback failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	_, _ = fmt.Fprintf(osStdout, "Rolled back promotion %s: %s -> %s (%s)\n", status.PromotionID, status.FromEnvironment, status.ToEnvironment, status.RollbackStatus)
	for _, workload := range status.Workloads {
		line := fmt.Sprintf("  %-20s %s", workload.WorkloadSlug, workload.RollbackStatus)
		if workload.RollbackError != "" {
			line += " — " + workload.RollbackError
		}
		_, _ = fmt.Fprintln(osStdout, line)
	}
	return 0
}

func promotionChangeCount(preview api.ProjectEnvironmentPromotionPreviewResponse) int {
	count := 0
	for _, change := range preview.Changes {
		if change.Kind != "unchanged" {
			count++
		}
	}
	return count
}

func cmdProjectsEnvironmentPromotionPreview(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("projects-environments-preview", flag.ContinueOnError)
	from := fs.String("from", "", "source environment")
	to := fs.String("to", "", "target environment")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(*from) || !api.ValidProjectEnvironmentSlug(*to) {
		PrintUsage(os.Stderr, "usage: gregale projects environments preview <project-slug> --from <environment> --to <environment>", "projects environments")
		return 1
	}
	if *from == *to {
		return printErr("Invalid environments", fmt.Errorf("--from and --to must be different"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.GetProjectEnvironmentPromotionPreview(context.Background(), positional[0], *to, *from)
	if err != nil {
		return printErr("Promotion preview failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(preview))
	}
	_, _ = fmt.Fprintf(osStdout, "Promotion preview %s: %s -> %s\n  can promote: %t\n  approval required: %t\n  config changes: %d\n  promotion hash: %s\n",
		preview.ProjectSlug, preview.FromEnvironment, preview.ToEnvironment, preview.CanPromote,
		preview.ApprovalRequired, len(preview.ConfigDiff.Changes), preview.PromotionHash)
	for _, reason := range preview.BlockingReasons {
		_, _ = fmt.Fprintf(osStdout, "  blocked: %s\n", reason)
	}
	for _, change := range preview.Changes {
		_, _ = fmt.Fprintf(osStdout, "  %-16s %-10s %s\n", change.WorkloadSlug, change.Kind, change.SourceRevision)
	}
	return 0
}

func cmdProjectsEnvironmentsList(args []string) int {
	if len(args) != 1 || !api.ValidProjectSlug(args[0]) {
		PrintUsage(os.Stderr, "usage: gregale projects environments list <project-slug>", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	environments, err := client.ListProjectEnvironments(context.Background(), args[0])
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(environments))
	}
	_, _ = fmt.Fprintf(osStdout, "%-24s %-12s %s\n", "SLUG", "PROTECTED", "UPDATED")
	for _, environment := range environments {
		_, _ = fmt.Fprintf(osStdout, "%-24s %-12t %s\n", environment.Slug, environment.Protected, environment.UpdatedAt)
	}
	return 0
}

func cmdProjectsEnvironmentCreate(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("projects-environments-create", flag.ContinueOnError)
	protected := fs.Bool("protected", false, "protect the environment from promotion")
	from := fs.String("from", "", "source environment to clone")
	shareResources := fs.Bool("share-resources", false, "explicitly share managed database and object-storage data")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 {
		PrintUsage(os.Stderr, "usage: gregale projects environments create <project-slug> <environment-slug> [--from <environment>] [--protected] [--share-resources]", "projects environments")
		return 1
	}
	if !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) {
		return printErr("Invalid environment", fmt.Errorf("project and environment slugs must use lowercase letters, numbers, and internal hyphens"))
	}
	if *from != "" && (!api.ValidProjectEnvironmentSlug(*from) || *from == positional[1]) {
		return printErr("Invalid source environment", fmt.Errorf("--from must name a different project environment"))
	}
	if *shareResources && *from == "" {
		return printErr("Invalid resource sharing option", fmt.Errorf("--share-resources requires --from"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	environment, err := client.CreateProjectEnvironment(context.Background(), positional[0], api.CreateProjectEnvironmentRequest{
		Slug: positional[1], Protected: protected, FromEnvironment: *from, ShareResources: *shareResources,
	})
	if err != nil {
		return printErr("Create failed", err)
	}
	return renderProjectEnvironment(environment)
}

func cmdProjectsEnvironmentProtection(args []string, protected bool) int {
	if len(args) != 2 || !api.ValidProjectSlug(args[0]) || !api.ValidProjectEnvironmentSlug(args[1]) {
		verb := "unprotect"
		if protected {
			verb = "protect"
		}
		PrintUsage(os.Stderr, "usage: gregale projects environments "+verb+" <project-slug> <environment-slug>", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	environment, err := client.UpdateProjectEnvironment(context.Background(), args[0], args[1], api.UpdateProjectEnvironmentRequest{Protected: &protected})
	if err != nil {
		return printErr("Update failed", err)
	}
	return renderProjectEnvironment(environment)
}

func renderProjectEnvironment(environment api.ProjectEnvironmentResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(environment))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\n  protected: %t\n  updated: %s\n", environment.Slug, environment.Protected, environment.UpdatedAt)
	if environment.Clone != nil {
		_, _ = fmt.Fprintf(osStdout, "  cloned from: %s\n  copied: config=%t variables=%d secrets=%d workloads=%d bindings=%d\n  shared: %s\n",
			environment.ClonedFrom, environment.Clone.ConfigurationCopied, environment.Clone.VariablesCopied,
			environment.Clone.SecretsCopied, environment.Clone.WorkloadsCopied, environment.Clone.BindingsCopied,
			strings.Join(environment.Clone.SharedResources, ", "))
		if strings.Contains(strings.Join(environment.Clone.SharedResources, ","), "managed_postgres_data") ||
			strings.Contains(strings.Join(environment.Clone.SharedResources, ","), "object_storage_bucket_data") {
			_, _ = fmt.Fprintln(osStdout, "  warning: managed data is shared with the source; credentials are new and environment-scoped")
		}
	}
	return 0
}

func cmdProjectsList(args []string) int {
	if len(args) != 0 {
		PrintUsage(os.Stderr, "usage: gregale projects list", "projects")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	projects, err := client.ListProjects(context.Background())
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(projects))
	}
	_, _ = fmt.Fprintf(osStdout, "%-24s %-28s %-18s %s\n", "SLUG", "REPOSITORY", "BRANCH", "WORKLOADS")
	for _, project := range projects {
		_, _ = fmt.Fprintf(osStdout, "%-24s %-28s %-18s %d\n", project.Slug, project.RepoFullName, project.ProductionBranch, project.WorkloadCount)
	}
	return 0
}

func cmdProjectsInfo(args []string) int {
	if len(args) != 1 || !api.ValidProjectSlug(args[0]) {
		PrintUsage(os.Stderr, "usage: gregale projects info <slug>", "projects")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	project, err := client.GetProject(context.Background(), args[0])
	if err != nil {
		return printErr("Request failed", err)
	}
	return renderProject(project)
}

func cmdProjectsUpdate(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("projects-update", flag.ContinueOnError)
	repo := fs.String("repo", "", "GitHub repository owner/name; empty unbinds")
	branch := fs.String("branch", "", "production branch")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale projects update <slug> [--repo owner/name] [--branch main]", "projects")
		return 1
	}
	if !api.ValidProjectSlug(positional[0]) {
		return printErr("Invalid project slug", fmt.Errorf("%q does not match the project slug contract", positional[0]))
	}
	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if !seen["repo"] && !seen["branch"] {
		PrintUsage(os.Stderr, "projects update requires --repo or --branch", "projects")
		return 1
	}
	req := api.UpdateProjectRequest{}
	if seen["repo"] {
		req.RepoFullName = repo
	}
	if seen["branch"] {
		req.ProductionBranch = branch
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	project, err := client.UpdateProject(context.Background(), positional[0], req)
	if err != nil {
		return printErr("Update failed", err)
	}
	return renderProject(project)
}

func cmdProjectsRemove(args []string) int {
	flags, positional := splitArgsForFlags(args, "dry-run", "yes")
	fs := newFlagSet("projects-rm", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "preview affected state")
	yes := fs.Bool("yes", false, "confirm project deletion")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || (*dryRun == *yes) {
		PrintUsage(os.Stderr, "usage: gregale projects rm <slug> (--dry-run | --yes)", "projects")
		return 1
	}
	if !api.ValidProjectSlug(positional[0]) {
		return printErr("Invalid project slug", fmt.Errorf("%q does not match the project slug contract", positional[0]))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.PreviewDeleteProject(context.Background(), positional[0])
	if err != nil {
		return printErr("Preview failed", err)
	}
	if *dryRun {
		if jsonOutput {
			return jsonOut(writeJSON(preview))
		}
		_, _ = fmt.Fprintf(osStdout, "Project %s: %d workloads will be detached; %d domains, %d env values, and %d crons remain on those apps.\n",
			preview.Project.Slug, len(preview.Workloads), preview.DomainCount, preview.EnvCount, preview.CronCount)
		return 0
	}
	if err := client.DeleteProject(context.Background(), positional[0]); err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"slug": positional[0], "deleted": true, "workloads_detached": len(preview.Workloads)}))
	}
	PrintOK(osStdout, "Deleted project %s; detached %d workloads", positional[0], len(preview.Workloads))
	return 0
}

func renderProject(project api.ProjectResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(project))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\n  repository: %s\n  production branch: %s\n  scan source: %s\n  workloads: %d\n  exclusions: %s\n  last deploy: %s\n  last build: %s\n",
		project.Slug, project.RepoFullName, project.ProductionBranch, project.ScanSource,
		len(project.Workloads), strings.Join(project.Exclusions, ", "), project.LastReconciliationStatus, project.LastBuildStatus)
	return 0
}
