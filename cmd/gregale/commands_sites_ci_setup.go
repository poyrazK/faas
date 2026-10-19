package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/hosting/cisetup"
)

type hostingCISetupRecipe struct {
	SiteID  string                   `json:"site_id"`
	TrustID string                   `json:"trust_id"`
	Config  api.HostingCISetupConfig `json:"config"`
}

func cmdSitesCISetup(args []string, preflight bool) int {
	enabled := os.Getenv("FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED")
	if enabled != "1" && enabled != "true" {
		return printErr("GitHub deployment setup is disabled", errors.New("set FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED=1; API opt-in is separate"))
	}
	f := newFlagSet("sites-ci-setup", flag.ContinueOnError)
	config := f.String("config", "", "reviewed setup JSON recipe from the dashboard")
	var out, root *string
	if preflight {
		root = f.String("repo-root", "", "repository root containing the saved workflow and preparation Action")
	} else {
		out = f.String("out", "", "new file for the reviewable workflow; never overwritten")
	}
	if err := f.Parse(args); err != nil || rejectUnexpectedFlagArgs(f) {
		return 1
	}
	if *config == "" || preflight && *root == "" || !preflight && *out == "" {
		return printErr("Setup paths required", errors.New("provide --config and either --out or --repo-root"))
	}
	recipe, err := readHostingCISetupRecipe(*config)
	if err != nil {
		return printErr("Invalid setup recipe", err)
	}
	if recipe.Config.ReservePreview && !hostingPreviewReservationsWorkstationEnabled() {
		return printErr("Preview reservations are disabled", errors.New("set FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED=1"))
	}
	client, err := hostingDiagnosticClient(recipe.SiteID, "")
	if err != nil {
		return printErr("Operator connection unavailable", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, api.HostingCISetupTimeout)
	defer cancel()
	report, err := client.PrepareHostingCISetup(ctx, recipe.SiteID, api.HostingCISetupRequest{TrustID: recipe.TrustID, Config: recipe.Config})
	if err != nil {
		return printErr("Setup configuration unavailable", err)
	}
	if report.SiteID != recipe.SiteID || report.TrustID != recipe.TrustID || !validHostingWorkflowPath(report.WorkflowPath) || report.ConfigurationValid && report.WorkflowYAML == "" {
		return printErr("Invalid setup response", errors.New("site, trust or workflow contract does not match the request"))
	}
	if report.ConfigurationValid {
		if preflight {
			preflightHostingRepository(&report, recipe, *root)
		} else if err = writeNewHostingWorkflow(*out, report.WorkflowYAML); err != nil {
			return printErr("Cannot export workflow", err)
		}
	}
	if code := jsonOut(writeJSON(report)); code != 0 {
		return code
	}
	if !report.ConfigurationValid {
		return 2
	}
	return 0
}
func readHostingCISetupRecipe(filename string) (hostingCISetupRecipe, error) {
	var recipe hostingCISetupRecipe
	raw, err := readHostingSetupFile(filename, api.HostingCISetupRequestMaxBytes)
	if err != nil {
		return recipe, errors.New("cannot read a bounded regular setup JSON file")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&recipe) != nil || !errors.Is(d.Decode(new(any)), io.EOF) || !hosting.ValidCIRunID(recipe.SiteID) || !hosting.ValidCIRunID(recipe.TrustID) {
		return recipe, errors.New("provide one setup JSON object with canonical site and trust UUIDs; unknown fields are rejected")
	}
	return recipe, nil
}
func readHostingSetupFile(filename string, maxBytes int64) (string, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("setup JSON must be a regular file")
	}
	f, err := openCustomerFile(filename)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return readHostingSetupHandle(f, maxBytes)
}
func readHostingSetupHandle(f *os.File, maxBytes int64) (string, error) {
	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !stat.Mode().IsRegular() || stat.Size() > maxBytes {
		return "", errors.New("file is not a bounded regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || int64(len(raw)) > maxBytes {
		return "", errors.New("file is unavailable or oversized")
	}
	return string(raw), nil
}
func writeNewHostingWorkflow(filename, body string) error {
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("choose a writable new output file; existing files are never overwritten")
	}
	_, writeErr := io.WriteString(f, body)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(filename)
		return errors.New("workflow export did not complete")
	}
	return nil
}
func validHostingWorkflowPath(p string) bool {
	return strings.HasPrefix(p, ".github/workflows/") && path.Dir(p) == ".github/workflows" && (strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml")) && !strings.ContainsAny(p, "\\\x00\r\n")
}
func preflightHostingRepository(report *api.HostingCISetup, recipe hostingCISetupRecipe, rootPath string) {
	add := func(code string, pass bool, message string) {
		state := "pass"
		if !pass {
			state = "blocked"
			report.ConfigurationValid = false
		}
		report.Checks = append(report.Checks, api.HostingCISetupCheck{Code: code, State: state, Message: message})
	}
	// Root.Open permits contained symlinks but refuses repository escapes. Neither
	// local source file is uploaded, logged, interpreted as shell or executed.
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		add("local_repository", false, "Repository root is unavailable.")
		return
	}
	defer func() { _ = root.Close() }()
	read := func(name string) (string, error) {
		info, e := root.Stat(name)
		if e != nil {
			return "", e
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("local source must be a regular file")
		}
		f, e := root.Open(name)
		if e != nil {
			return "", e
		}
		defer func() { _ = f.Close() }()
		return readHostingSetupHandle(f, api.HostingCISetupWorkflowMaxBytes)
	}
	raw, err := read(report.WorkflowPath)
	add("local_workflow", err == nil && cisetup.WorkflowMatches(raw, report.WorkflowYAML), "Save the generated workflow at its exact trusted path. Only comments and mapping order may differ; permissions, triggers, pins, steps and settings must match.")
	prep := strings.TrimPrefix(recipe.Config.PrepareAction, "./")
	valid := false
	for _, name := range []string{"action.yml", "action.yaml"} {
		raw, e := read(path.Join(prep, name))
		if e == nil {
			valid = cisetup.ValidPreparationAction(raw)
			break
		}
		if !errors.Is(e, os.ErrNotExist) {
			break
		}
	}
	add("local_preparation", valid, "The selected preparation directory must contain a bounded, well-formed composite action.yml or action.yaml with steps. Its contents still require manual review.")
}
