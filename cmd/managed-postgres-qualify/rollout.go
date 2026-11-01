package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/guestqualification"
)

func runVerificationArguments(args []string, getenv func(string) string, output, errorOutput io.Writer) int {
	if getenv == nil {
		return verificationUsage(errorOutput)
	}
	paths := map[string]string{
		"--approval":         strings.TrimSpace(getenv(managedpostgres.QualificationApprovalPathEnv)),
		"--guest-report":     strings.TrimSpace(getenv(managedpostgres.GuestQualificationPathEnv)),
		"--runtime-manifest": strings.TrimSpace(getenv(managedpostgres.RuntimeManifestPathEnv)),
	}
	rollout := false
	seen := make(map[string]bool)
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if seen[flag] {
			return verificationUsage(errorOutput)
		}
		seen[flag] = true
		if flag == "--rollout" {
			rollout = true
			continue
		}
		if _, known := paths[flag]; !known || i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return verificationUsage(errorOutput)
		}
		i++
		paths[flag] = strings.TrimSpace(args[i])
		rollout = rollout || flag != "--approval"
	}
	// Daemon opt-in and verifier semantics must agree; supplied guest inputs
	// cannot be ignored by accidentally leaving --rollout off the invocation.
	required, requirementErr := managedpostgres.GuestQualificationRequired(getenv)
	if requirementErr != nil || required || paths["--guest-report"] != "" || paths["--runtime-manifest"] != "" {
		rollout = true
	}
	if !rollout {
		return runVerify(getenv, paths["--approval"], output, errorOutput)
	}
	return runVerifyRollout(getenv, paths["--approval"], paths["--guest-report"], paths["--runtime-manifest"], requirementErr != nil, output, errorOutput)
}

func verificationUsage(output io.Writer) int {
	_, _ = fmt.Fprintln(output, "usage: managed-postgres-qualify --verify [--approval PATH] [--rollout --guest-report PATH --runtime-manifest PATH]")
	return 2
}

func runVerifyRollout(getenv func(string) string, approvalPath, guestPath, runtimePath string, gateErr bool, output, errorOutput io.Writer) int {
	if getenv == nil || !strings.EqualFold(strings.TrimSpace(getenv(managedpostgres.EnvironmentEnv)), managedpostgres.QualificationStagingEnvironment) {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification verification requires FAAS_ENVIRONMENT=staging")
		return 2
	}
	artifact, approvalErr := readQualificationArtifact(approvalPath)
	registry, registryErr := managedpostgres.Load(getenv, describeFactories())
	accounts, accountErr := managedpostgres.ParseStagingCanaryAccounts(getenv(managedpostgres.CanaryAccountsEnv))
	if registryErr != nil || registry == nil || accountErr != nil {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification configuration is unavailable")
		return 2
	}
	var guest *guestqualification.Report
	var manifest *guestqualification.RuntimeManifest
	var reasons []string
	if guestPath != "" {
		loaded, err := guestqualification.Load[guestqualification.Report](guestPath)
		if err == nil {
			guest = &loaded
		} else {
			reasons = append(reasons, "guest_report_unreadable")
		}
	}
	if runtimePath != "" {
		loaded, err := guestqualification.Load[guestqualification.RuntimeManifest](runtimePath)
		if err == nil {
			manifest = &loaded
		} else {
			reasons = append(reasons, "runtime_manifest_unreadable")
		}
	}
	result := registry.VerifyRolloutQualification(artifact, guest, manifest, accounts, time.Now().UTC())
	if approvalErr != nil {
		reasons = append(reasons, "provider_artifact_unreadable")
	}
	if gateErr {
		reasons = append(reasons, "guest_requirement_invalid")
	}
	if len(reasons) != 0 {
		result.Ready = false
		result.Reasons = append(result.Reasons, reasons...)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		_, _ = fmt.Fprintln(errorOutput, "cannot write rollout verification")
		return 1
	}
	if !result.Ready {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres rollout qualification is not ready")
		return 1
	}
	return 0
}
