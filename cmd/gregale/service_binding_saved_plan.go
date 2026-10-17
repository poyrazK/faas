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

	"github.com/onebox-faas/faas/pkg/api"
)

const serviceBindingSavedPlanMaxBytes = 4 * api.SourceManifestMaxBytes

type serviceBindingSavedPlan struct {
	Version             int                                `json:"version"`
	Endpoint            string                             `json:"endpoint"`
	AccountID           string                             `json:"account_id"`
	App                 string                             `json:"app"`
	AppID               string                             `json:"app_id"`
	ExpectedFingerprint string                             `json:"expected_fingerprint"`
	Patch               json.RawMessage                    `json:"patch"`
	Review              serviceBindingConfigurationReceipt `json:"review"`
	SHA256              string                             `json:"sha256"`
}

func (p serviceBindingSavedPlan) checksum() string {
	p.SHA256 = ""
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func saveServiceBindingPlan(ctx context.Context, client *api.Client, app api.AppResponse, config serviceBindingConfiguration, review serviceBindingConfigurationReceipt, path string) error {
	fingerprint, fingerprintErr := hex.DecodeString(app.ServiceBindingFingerprint)
	if app.ID == "" || fingerprintErr != nil || len(fingerprint) != sha256.Size {
		return fmt.Errorf("server does not support guarded service binding plans")
	}
	account, err := client.Whoami(ctx)
	if err != nil {
		return err
	}
	if account.ID == "" {
		return fmt.Errorf("server did not identify the account")
	}
	patch, _ := json.Marshal(config)
	plan := serviceBindingSavedPlan{Version: 1, Endpoint: client.BaseURL(), AccountID: account.ID, App: app.Slug, AppID: app.ID, ExpectedFingerprint: app.ServiceBindingFingerprint, Patch: patch, Review: review}
	plan.SHA256 = plan.checksum()
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	if len(encoded)+1 > serviceBindingSavedPlanMaxBytes {
		return fmt.Errorf("saved plan exceeds size limit")
	}
	return writeSavedJSONPlan(path, plan)
}

func readServiceBindingPlan(path string) (serviceBindingSavedPlan, error) {
	var plan serviceBindingSavedPlan
	file, err := openCustomerFile(path)
	if err != nil {
		return plan, err
	}
	defer func() { _ = file.Close() }()
	// A plan contains bounded input and before/after policy inventories.
	raw, err := io.ReadAll(io.LimitReader(file, serviceBindingSavedPlanMaxBytes+1))
	if err != nil {
		return plan, err
	}
	if len(raw) > serviceBindingSavedPlanMaxBytes {
		return plan, fmt.Errorf("saved plan exceeds size limit")
	}
	if err := servicePolicyJSONKeys(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return plan, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&plan); err != nil {
		return plan, err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return plan, fmt.Errorf("expected one saved plan")
	}
	fingerprint, err := hex.DecodeString(plan.ExpectedFingerprint)
	if err != nil || len(fingerprint) != sha256.Size || plan.Version != 1 || plan.AppID == "" || plan.AccountID == "" || plan.Endpoint == "" || !api.ValidAppSlug(plan.App) || !plan.Review.DryRun || plan.Review.App != plan.App || plan.SHA256 != plan.checksum() {
		return plan, fmt.Errorf("invalid or modified service binding plan; create a fresh plan")
	}
	return plan, nil
}

func cmdBindingsServiceApply(args []string) int {
	fs := newFlagSet("bindings service apply", flag.ContinueOnError)
	path := fs.String("plan", "", "saved service binding plan")
	flags, pos := splitArgsForFlags(args)
	if fs.Parse(flags) != nil || fs.NArg() != 0 || len(pos) != 1 || !api.ValidAppSlug(pos[0]) || *path == "" {
		return printErr("Invalid service plan", fmt.Errorf("usage: gregale bindings service apply APP --plan plan.json"))
	}
	plan, err := readServiceBindingPlan(*path)
	if err != nil {
		return printErr("Could not read service plan", err)
	}
	if plan.App != pos[0] {
		return printErr("Wrong service plan target", fmt.Errorf("plan belongs to app %s", plan.App))
	}
	config, err := decodeServiceBindingConfiguration(plan.Patch, plan.App)
	if err != nil {
		return printErr("Invalid service plan patch", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if client.BaseURL() != plan.Endpoint {
		return printErr("Wrong service plan endpoint", fmt.Errorf("select the connection used to create the plan"))
	}
	ctx := context.Background()
	account, err := client.Whoami(ctx)
	if err != nil {
		return printErr("Could not identify account", err)
	}
	if account.ID != plan.AccountID {
		return printErr("Wrong service plan account", fmt.Errorf("select the account used to create the plan"))
	}
	before, err := client.GetApp(ctx, plan.App)
	if err != nil {
		return printErr("Could not read service settings", err)
	}
	if before.ID != plan.AppID || before.ServiceBindingFingerprint != plan.ExpectedFingerprint {
		return printErr("Service binding plan is stale", fmt.Errorf("settings or app identity changed; create a fresh plan"))
	}
	return runServiceBindingConfiguration(ctx, client, plan.App, config, before, false, "", &plan.ExpectedFingerprint)
}
