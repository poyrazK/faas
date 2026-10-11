package main

// adr: 797
import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
)

type provisionTargetFile struct {
	Region            string               `json:"region"`
	RegionalAccountID string               `json:"regional_account_id"`
	RegionalTokenEnv  string               `json:"regional_token_env"`
	App               api.CreateAppRequest `json:"app"`
}

func readProvisionTargets(path string) (api.PutGlobalAppProvisioningRequest, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return api.PutGlobalAppProvisioningRequest{}, fmt.Errorf("could not open targets file")
	}
	defer func() { _ = f.Close() }()
	var input struct {
		Targets []provisionTargetFile `json:"targets"`
	}
	raw, err := io.ReadAll(io.LimitReader(f, api.GlobalAppProvisionRequestMaxBytes+1))
	if err != nil || len(raw) > api.GlobalAppProvisionRequestMaxBytes {
		return api.PutGlobalAppProvisioningRequest{}, fmt.Errorf("targets file exceeds the provisioning request bound")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || d.Decode(&struct{}{}) != io.EOF || len(input.Targets) == 0 || len(input.Targets) > api.GlobalImageReleaseTargetsMax {
		return api.PutGlobalAppProvisioningRequest{}, fmt.Errorf("targets file must contain one bounded regional target set")
	}
	out := api.PutGlobalAppProvisioningRequest{Targets: make([]api.GlobalAppProvisionTargetRequest, 0, len(input.Targets))}
	seen := make(map[string]bool)
	for _, t := range input.Targets {
		if t.Region == "" || !api.ValidComputeRegion(t.Region) || seen[t.Region] || !globaldirectory.ValidID(t.RegionalAccountID) || !validDirectoryTokenEnv(t.RegionalTokenEnv) {
			return api.PutGlobalAppProvisioningRequest{}, fmt.Errorf("each target needs a unique region, canonical account UUID and regional_token_env")
		}
		key := os.Getenv(t.RegionalTokenEnv)
		if !api.ValidAPIKeyFormat(key) {
			return api.PutGlobalAppProvisioningRequest{}, fmt.Errorf("a regional token environment variable is unavailable")
		}
		seen[t.Region] = true
		out.Targets = append(out.Targets, api.GlobalAppProvisionTargetRequest{Region: t.Region, RegionalAccountID: t.RegionalAccountID, RegionalAPIKey: key, App: t.App})
	}
	return out, nil
}

func cmdGlobalAppProvision(args []string) int {
	if len(args) == 0 || !globaldirectory.ValidSlug(args[0]) {
		return printErr("Invalid global app", fmt.Errorf("a global app slug is required"))
	}
	fs := newFlagSet("global-app provision", flag.ContinueOnError)
	path := fs.String("targets", "", "JSON file with regions, account UUIDs, app settings and token environment variable names")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	if *path == "" || fs.NArg() != 0 {
		return printErr("Invalid provisioning request", fmt.Errorf("--targets is required"))
	}
	req, err := readProvisionTargets(*path)
	if err != nil {
		return printErr("Invalid provisioning request", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.GlobalImageReleaseRequestTimeout)
	defer cancel()
	out, err := client.PutGlobalAppProvisioning(ctx, args[0], req)
	if err != nil {
		return printErr("Could not provision regional apps", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	for _, t := range out {
		_, _ = fmt.Fprintln(osStdout, strings.Join([]string{t.Region, t.Status, t.ProvisionID, t.Diagnostic}, "\t"))
	}
	return 0
}
