package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// The receipt contains resource identities and digests, never credentials,
// identity mappings, issuer settings, request bodies, or customer responses.
type dataAPIConnectReceipt struct {
	Version              int    `json:"version"`
	App                  string `json:"app"`
	APIHash              string `json:"api_hash"`
	DatabaseID           string `json:"database_id"`
	AppID                string `json:"app_id,omitempty"`
	BindingID            string `json:"binding_id,omitempty"`
	CredentialGeneration int64  `json:"credential_generation,omitempty"`
	DeploymentID         string `json:"deployment_id,omitempty"`
	ConfigurationHash    string `json:"configuration_hash,omitempty"`
	CollectionHash       string `json:"collection_hash"`
	Output               string `json:"output"`
	NumericMode          string `json:"numeric_mode"`
	Fingerprint          string `json:"fingerprint,omitempty"`
	TypesHash            string `json:"types_hash,omitempty"`
	Stage                string `json:"stage"`
}

func dataAPIConnectHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (r dataAPIConnectReceipt) validate() error {
	if r.Version != 1 || !api.ValidAppSlug(r.App) || !dataAPIApprovalFingerprint(r.APIHash) || r.DatabaseID == "" || !dataAPIApprovalFingerprint(r.CollectionHash) || !filepath.IsAbs(r.Output) || !dataAPIDiagnosticAllowed(r.NumericMode, "number", "lossless", "exact") {
		return errors.New("invalid connection receipt")
	}
	if r.Stage != "creating" && r.Stage != "deployed" && r.Stage != "types_written" && r.Stage != "verified" {
		return errors.New("invalid connection receipt stage")
	}
	if r.Stage != "creating" && (r.AppID == "" || r.BindingID == "" || r.CredentialGeneration < 1 || r.DeploymentID == "" || !dataAPIApprovalFingerprint(r.ConfigurationHash)) {
		return errors.New("connection receipt lacks verified resource identities")
	}
	if (r.Stage == "types_written" || r.Stage == "verified") && (!dataAPIApprovalFingerprint(r.Fingerprint) || !dataAPIApprovalFingerprint(r.TypesHash)) {
		return errors.New("connection receipt lacks verified types")
	}
	return nil
}

func readDataAPIConnectReceipt(path string) (dataAPIConnectReceipt, error) {
	var receipt dataAPIConnectReceipt
	file, err := openCustomerFile(path)
	if err != nil {
		return receipt, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16384 {
		return receipt, errors.New("receipt must be a regular file under 16 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(data) > 16384 || json.Unmarshal(data, &receipt) != nil {
		return receipt, errors.New("invalid connection receipt")
	}
	return receipt, receipt.validate()
}

func writeDataAPIConnectReceipt(path string, receipt dataAPIConnectReceipt, create bool) error {
	if err := receipt.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), ".gregale-connect-receipt-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if create {
		return os.Link(f.Name(), path)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("connection receipt was removed or is not a regular file")
	}
	return os.Rename(f.Name(), path)
}

type dataAPIConnectRemote interface {
	GetApp(context.Context, string) (api.AppResponse, error)
	GetManagedPostgresDatabase(context.Context, string) (api.ManagedPostgresDatabase, error)
	ListManagedPostgresBindings(context.Context, string) (api.ManagedPostgresBindingList, error)
	GetAppReleaseReview(context.Context, string) (api.AppReleaseReview, error)
}

func dataAPIConnectSnapshot(ctx context.Context, client dataAPIConnectRemote, r *dataAPIConnectReceipt, capture bool) error {
	db, err := client.GetManagedPostgresDatabase(ctx, r.DatabaseID)
	if err != nil {
		return fmt.Errorf("database lookup: %w", err)
	}
	if db.ID != r.DatabaseID || db.State != "ready" || db.Hold != "" {
		return errors.New("the recorded database is not ready")
	}
	app, err := client.GetApp(ctx, r.App)
	if err != nil {
		return fmt.Errorf("app lookup: %w", err)
	}
	if app.ID == "" || app.Slug != r.App || app.ID != r.AppID {
		return errors.New("app identity changed")
	}
	bindings, err := client.ListManagedPostgresBindings(ctx, r.DatabaseID)
	if err != nil {
		return fmt.Errorf("binding lookup: %w", err)
	}
	var chosen api.ManagedPostgresBinding
	for _, b := range bindings.Items {
		if b.AppID == app.ID && b.Scope == api.DefaultEnvScope && b.EnvironmentKey == "DATABASE_URL" && b.Access == "read_only" && b.State == "ready" {
			if chosen.ID != "" {
				return errors.New("multiple matching bindings; inspect the app")
			}
			chosen = b
		}
	}
	if chosen.ID == "" || chosen.DatabaseID != r.DatabaseID || chosen.RotationPending || chosen.CredentialGeneration < 1 {
		return errors.New("the expected read-only binding is not ready")
	}
	if chosen.ID != r.BindingID || chosen.CredentialGeneration != r.CredentialGeneration {
		return errors.New("database binding identity or credential generation changed")
	}
	review, err := client.GetAppReleaseReview(ctx, r.App)
	if err != nil {
		return fmt.Errorf("deployment review: %w", err)
	}
	if review.AppID != app.ID || review.Scope != api.DefaultEnvScope || !dataAPIApprovalFingerprint(review.ConfigurationHash) || review.LatestDeploymentID == "" || len(review.ServingDeploymentIDs) != 1 || review.ServingDeploymentIDs[0] != review.LatestDeploymentID {
		return errors.New("app does not have one stable serving deployment")
	}
	if review.LatestDeploymentID != r.DeploymentID || (!capture && review.ConfigurationHash != r.ConfigurationHash) {
		return errors.New("app configuration or deployment changed")
	}
	if capture {
		r.ConfigurationHash = review.ConfigurationHash
	}
	return nil
}

func dataAPIConnectTypesMatch(path, content string) error {
	f, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(content)) {
		return errors.New("types file differs from the generated contract")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(len(content))+1))
	if err != nil || string(data) != content {
		return errors.New("types file differs from the generated contract")
	}
	return nil
}

func continueDataAPIConnect(ctx context.Context, client *api.Client, path string, receipt *dataAPIConnectReceipt, config *dataAPIHostedReplayConfig) int {
	if err := dataAPIConnectSnapshot(ctx, client, receipt, false); err != nil {
		return printErr("Connection verification blocked", err)
	}
	task, err := generateDataAPITypes(ctx, client, receipt.App)
	if err != nil {
		return printErr("API created; could not export types", err)
	}
	if task.DeploymentID == "" || task.DeploymentID != receipt.DeploymentID {
		return printErr("Connection verification blocked", errors.New("types came from a different deployment"))
	}
	fingerprint := dataAPIFingerprint.FindStringSubmatch(task.StdoutTail)
	if len(fingerprint) != 2 {
		return printErr("API created; could not export types", errors.New("runtime returned types without a schema fingerprint"))
	}
	content := dataAPITypesForMode(task, receipt.NumericMode)
	if receipt.Fingerprint != "" && (receipt.Fingerprint != fingerprint[1] || receipt.TypesHash != dataAPIConnectHash([]byte(content))) {
		return printErr("Connection verification blocked", errors.New("the database contract changed since types were recorded"))
	}
	if err := dataAPIConnectSnapshot(ctx, client, receipt, false); err != nil {
		return printErr("Connection verification blocked", err)
	}
	if _, err := os.Lstat(receipt.Output); errors.Is(err, os.ErrNotExist) {
		if receipt.Stage == "types_written" || receipt.Stage == "verified" {
			return printErr("Connection verification blocked", errors.New("recorded types file is missing"))
		}
		if err := writeDataAPIConnectTypes(receipt.Output, content); err != nil {
			return printErr("API created; could not export types", err)
		}
	} else if err != nil {
		return printErr("API created; could not inspect types", err)
	} else if err := dataAPIConnectTypesMatch(receipt.Output, content); err != nil {
		return printErr("Connection verification blocked", err)
	}
	receipt.Fingerprint, receipt.TypesHash, receipt.Stage = fingerprint[1], dataAPIConnectHash([]byte(content)), "types_written"
	if err := writeDataAPIConnectReceipt(path, *receipt, false); err != nil {
		return printErr("Could not record connection progress", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Types match %s; verifying the same schema with both identities.\n", receipt.Output)
	app, err := client.GetApp(ctx, receipt.App)
	if err != nil || app.ID != receipt.AppID {
		return printErr("Connection verification blocked", errors.New("could not confirm the recorded app"))
	}
	endpoint, err := dataAPIReadinessURL(app)
	if err != nil {
		return printErr("API created; verification incomplete", err)
	}
	if err := runDataAPIHostedReplay(ctx, config, endpoint, receipt.Fingerprint, false, false, nil); err != nil {
		return printErr("API created; verification incomplete", errors.New("inspect the assertions, then rerun connect --resume with the original collection"))
	}
	if err := dataAPIConnectSnapshot(ctx, client, receipt, false); err != nil {
		return printErr("Connection verification blocked", err)
	}
	receipt.Stage = "verified"
	if err := writeDataAPIConnectReceipt(path, *receipt, false); err != nil {
		return printErr("Could not record connection progress", err)
	}
	_, _ = fmt.Fprintln(osStdout, "Connection complete. The supplied authorization assertions passed; they do not establish coverage of every policy or relation.")
	return 0
}

func reportDataAPIConnectStatus(ctx context.Context, client *api.Client, receipt dataAPIConnectReceipt) int {
	report := map[string]any{"app": receipt.App, "app_id": receipt.AppID, "database_id": receipt.DatabaseID, "binding_id": receipt.BindingID, "deployment_id": receipt.DeploymentID, "stage": receipt.Stage, "resource_status": "unknown", "types_status": "not_recorded", "authorization": "not_currently_checked"}
	var problem error
	if receipt.Stage == "creating" {
		problem = errors.New("creation outcome requires manual inspection")
	} else if err := dataAPIConnectSnapshot(ctx, client, &receipt, false); err != nil {
		report["resource_status"] = "changed_or_unavailable"
		problem = err
	} else {
		report["resource_status"] = "matches_receipt"
	}
	if receipt.TypesHash != "" {
		f, err := openCustomerFile(receipt.Output)
		if err == nil {
			info, statErr := f.Stat()
			const maxTypes = 2*api.DataAPIContractArtifactBytes + 1024
			if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxTypes {
				err = errors.New("types file is not a bounded regular file")
			} else {
				var data []byte
				data, err = io.ReadAll(io.LimitReader(f, maxTypes+1))
				if err == nil && dataAPIConnectHash(data) != receipt.TypesHash {
					err = errors.New("types file changed")
				}
			}
			_ = f.Close()
		}
		if err != nil {
			report["types_status"] = "changed_or_unavailable"
			if problem == nil {
				problem = err
			}
		} else {
			report["types_status"] = "matches_receipt"
		}
	}
	if receipt.Stage == "verified" {
		report["authorization"] = "passed_on_last_run_not_rechecked"
	}
	steps := map[string]string{"inspection": "passed_on_original_run", "creation": "passed_on_original_run", "types": "pending", "authorization": "pending"}
	if receipt.Stage == "creating" {
		steps["creation"] = "outcome_unknown"
	}
	if receipt.TypesHash != "" {
		steps["types"] = report["types_status"].(string)
	}
	if receipt.Stage == "verified" {
		steps["authorization"] = "passed_on_last_run_not_rechecked"
	}
	report["steps"] = steps
	if problem != nil {
		report["status"] = "blocked"
		report["reason"] = problem.Error()
		report["next_action"] = "inspect_recorded_resources"
	} else {
		report["status"] = "observed"
		if receipt.Stage == "verified" {
			report["next_action"] = "none"
		} else {
			report["next_action"] = "resume_verification"
		}
	}
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return printErr("Could not write connection status", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Connection %s: stage=%s resources=%s types=%s authorization=%s next=%s\n", receipt.App, report["stage"], report["resource_status"], report["types_status"], report["authorization"], report["next_action"])
		_, _ = fmt.Fprintf(osStdout, "Recorded identities: app=%s database=%s binding=%s deployment=%s\n", receipt.AppID, receipt.DatabaseID, receipt.BindingID, receipt.DeploymentID)
		if problem != nil {
			_, _ = fmt.Fprintf(osStderr, "Next: inspect the recorded app and binding. %s\n", problem)
		}
	}
	if problem != nil {
		return 1
	}
	return 0
}
