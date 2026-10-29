package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type secretConfigurationRow struct {
	Key         string `json:"key"`
	Required    bool   `json:"required"`
	Present     bool   `json:"present"`
	Delivery    string `json:"delivery"`
	Application string `json:"application"`
}

type secretConfigurationReport struct {
	App                  string                        `json:"app_slug"`
	Scope                string                        `json:"scope"`
	Status               string                        `json:"status"`
	RequirementsDeclared bool                          `json:"requirements_declared"`
	RequirementsSource   string                        `json:"requirements_source"`
	RequirementsFile     string                        `json:"requirements_file,omitempty"`
	MissingTemplate      *missingSecretTemplateReceipt `json:"missing_template,omitempty"`
	Keys                 []secretConfigurationRow      `json:"keys"`
	NextCommand          string                        `json:"next_command,omitempty"`
}

func secretsCheck(args []string) int {
	fs := newFlagSet("secrets check", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked app)")
	scope := fs.String("scope", "", "single environment scope (defaults to linked environment)")
	file := fs.String("required-file", "", "JSON array of required secret key names; no values")
	template := fs.String("missing-template", "", "create a new private dotenv file containing missing required keys only")
	environments := fs.String("environments", "", "compare required names across comma-separated secret scopes")
	var required []string
	fs.Func("required", "required secret key (repeatable)", func(key string) error {
		if problem := api.ValidateSecretKey(key); problem != nil {
			return errors.New(problem.Detail)
		}
		required = append(required, key)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	fileSet, keysSet, templateSet, environmentsSet, scopeSet := false, false, false, false, false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "required-file" {
			fileSet = true
		}
		if f.Name == "required" {
			keysSet = true
		}
		if f.Name == "missing-template" {
			templateSet = true
		}
		if f.Name == "environments" {
			environmentsSet = true
		}
		if f.Name == "scope" {
			scopeSet = true
		}
	})
	var comparedScopes []string
	defaultsPath := ""
	if environmentsSet {
		if scopeSet || templateSet {
			return printErr("Invalid environment comparison", errors.New("--environments cannot be combined with --scope or --missing-template; compare first, then check one scope"))
		}
		var err error
		if strings.TrimSpace(*environments) == "saved" {
			var defaults localSecretDefaults
			defaults, defaultsPath, err = readSecretDefaults()
			comparedScopes = defaults.Environments
		} else {
			comparedScopes, err = parseSecretComparisonScopes(*environments)
		}
		if err != nil {
			return printErr("Invalid environment comparison", err)
		}
	}
	source := "none"
	if fileSet {
		if *file == "" {
			return printErr("Invalid requirements file", errors.New("--required-file requires a nonempty path"))
		}
		source = "explicit_file"
		if keysSet {
			source = "explicit_file_and_keys"
		}
	} else if keysSet {
		source = "flags"
	} else {
		discovered, err := discoverRequiredSecretFile()
		if err != nil {
			return printErr("Could not discover requirements", err)
		}
		*file = discovered
		if discovered != "" {
			source = "project_file"
		}
	}
	if *file != "" {
		absolute, err := filepath.Abs(*file)
		if err != nil {
			return printErr("Could not resolve requirements path", err)
		}
		*file = absolute
		keys, err := readRequiredSecretKeys(*file)
		if err != nil {
			return printErr("Invalid requirements file", err)
		}
		required = append(required, keys...)
	}
	required = sortedUniqueStrings(required)
	resolvedApp, err := resolveSecretApp(*app)
	if err != nil {
		return printErr("Could not resolve app", err)
	}
	if environmentsSet {
		if source == "none" {
			return printErr("Requirements not declared", errors.New("declare required keys before comparing environments"))
		}
		return compareRequiredSecretScopes(resolvedApp, comparedScopes, required, source, *file, defaultsPath)
	}
	resolvedScope, err := resolveEnvironmentFlagOrContext(*scope)
	if err != nil {
		return printErr("Could not resolve scope", err)
	}
	if resolvedScope == "__all__" {
		return printErr("Invalid scope", errors.New("check requires one scope; use secrets list --scope __all__ to browse scopes"))
	}
	var output *missingSecretTemplateOutput
	if templateSet {
		if source == "none" {
			return printErr("Requirements not declared", errors.New("declare required keys before exporting a missing-key template"))
		}
		var prepareErr error
		output, prepareErr = prepareMissingSecretTemplate(*template)
		if prepareErr != nil {
			return printErr("Cannot create missing-key template", prepareErr)
		}
		defer output.directory.Close()
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	metadata, err := client.ListSecretsWithScope(ctx, resolvedApp, resolvedScope)
	if err != nil {
		return printErr("Configuration check failed", err)
	}
	report := buildSecretConfigurationReport(resolvedApp, scopeOrDefault(resolvedScope), required, metadata)
	report.RequirementsDeclared = len(required) > 0 || *file != ""
	report.RequirementsSource, report.RequirementsFile = source, *file
	if output != nil {
		receipt, err := writeMissingSecretTemplate(ctx, output, report)
		if err != nil {
			return printErr("Could not save missing-key template", err)
		}
		report.MissingTemplate = receipt
		if receipt.Status == "created" {
			report.NextCommand = receipt.PreviewCommand
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Secret configuration: %s\nApp: %s\nScope: %s\n", report.Status, oneLine(report.App), oneLine(report.Scope))
		_, _ = fmt.Fprintf(osStdout, "Requirements source: %s\n", source)
		if *file != "" {
			_, _ = fmt.Fprintf(osStdout, "Declaration: %s\n", oneLine(*file))
		}
		if !report.RequirementsDeclared {
			_, _ = fmt.Fprintln(osStdout, "Required keys were not declared; completeness is unknown. Declare keys in .gregale/required-secrets.json at the project root, or use --required or --required-file.")
		}
		for _, row := range report.Keys {
			if !row.Present {
				_, _ = fmt.Fprintf(osStdout, "  %s: missing required secret\n", oneLine(row.Key))
				continue
			}
			_, _ = fmt.Fprintf(osStdout, "  %s: configured; delivery=%s; application=%s\n", oneLine(row.Key), row.Delivery, row.Application)
		}
		_, _ = fmt.Fprintln(osStdout, "This checks scoped sealed-secret metadata, not ordinary environment variables, reference destinations, or application behavior. Delivery alone does not confirm application adoption.")
		if report.MissingTemplate != nil {
			printMissingSecretTemplate(report.MissingTemplate)
		}
		if report.NextCommand != "" && (report.MissingTemplate == nil || report.MissingTemplate.Status != "created") {
			_, _ = fmt.Fprintf(osStdout, "Next step (POSIX shells):\n  %s\n", report.NextCommand)
		}
	}
	if report.Status == "needs attention" {
		return 1
	}
	return 0
}

func readRequiredSecretKeys(path string) ([]string, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, fmt.Errorf("open requirements: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return nil, fmt.Errorf("read requirements: %w", err)
	}
	if len(data) > 65536 {
		return nil, errors.New("requirements exceed 64 KiB")
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, errors.New("expected a JSON array of secret key names")
	}
	if keys == nil {
		return nil, errors.New("expected a JSON array of secret key names")
	}
	for _, key := range keys {
		if problem := api.ValidateSecretKey(key); problem != nil {
			return nil, errors.New("requirements contain an invalid secret key name")
		}
	}
	return keys, nil
}

func buildSecretConfigurationReport(app, scope string, required []string, metadata api.AppSecretListResponse) secretConfigurationReport {
	report := secretConfigurationReport{App: app, Scope: scope, Status: "no delivery issues reported", RequirementsDeclared: len(required) > 0, Keys: []secretConfigurationRow{}}
	rows := map[string]api.AppSecretResponse{}
	names := append([]string{}, required...)
	for _, row := range metadata.Secrets {
		if scopeOrDefault(row.Scope) != scope {
			continue
		}
		rows[row.Key] = row
		names = append(names, row.Key)
	}
	needed := map[string]bool{}
	for _, key := range required {
		needed[key] = true
	}
	for _, key := range sortedUniqueStrings(names) {
		secret, present := rows[key]
		row := secretConfigurationRow{Key: key, Required: needed[key], Present: present, Delivery: "unknown", Application: "unconfirmed"}
		if present {
			row.Delivery = secretDeliveryState(secret.DeliveryStatus, secret.DeliveryVersion, secret.DeliveredVersion)
			for _, target := range secret.RuntimeReloadObservations {
				if secret.DeliveryVersion > 0 && target.ApplicationAckVersion == secret.DeliveryVersion && target.ApplicationAck == "failed" {
					row.Application = "failed"
				}
			}
			if secret.RuntimeReloadTargetsComplete && len(secret.RuntimeReloadObservations) > 0 && secret.DeliveryVersion > 0 {
				applied := true
				for _, target := range secret.RuntimeReloadObservations {
					if target.ApplicationAckVersion != secret.DeliveryVersion || target.ApplicationAck != "applied" {
						applied = false
					}
				}
				if applied {
					row.Application = "acknowledged by all reported authorized targets"
				}
			}
		}
		if !present || row.Delivery != "delivered" || row.Application == "failed" {
			report.Status = "needs attention"
		}
		if !present && report.NextCommand == "" {
			report.NextCommand = recoveryCLI() + " env set --app " + recoveryArgument(app) + " --scope " + recoveryArgument(scope) + " --from-stdin"
		}
		report.Keys = append(report.Keys, row)
	}
	if report.Status == "needs attention" && report.NextCommand == "" {
		report.NextCommand = recoveryCLI() + " inspect " + recoveryArgument(app)
	}
	return report
}
