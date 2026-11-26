package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/pkg/api"
)

// A plan contains identifiers and digests, not JWTs, rows or request bodies.
type dataAPIRequalificationPlan struct {
	Version                   int                   `json:"version"`
	PreviousReceiptHash       string                `json:"previous_receipt_sha256"`
	PreviousDeployment        string                `json:"previous_deployment_id"`
	PreviousFingerprint       string                `json:"previous_schema_fingerprint"`
	PreviousConfigurationHash string                `json:"previous_configuration_sha256"`
	PreviousTypesHash         string                `json:"previous_types_sha256"`
	PreviousCollectionHash    string                `json:"previous_collection_sha256"`
	IdentityMappingHash       string                `json:"identity_mapping_sha256"`
	Candidate                 dataAPIConnectReceipt `json:"candidate"`
	Endpoint                  string                `json:"readiness_endpoint"`
	NewReceipt                string                `json:"new_receipt"`
}

func requalifyReceiptHash(r dataAPIConnectReceipt) string {
	b, _ := json.Marshal(r)
	return dataAPIConnectHash(b)
}

// Candidate observation never adopts a different app, database, binding or
// credential generation. Only a stable deployment/configuration may differ.
func observeRequalificationCandidate(ctx context.Context, client dataAPIConnectRemote, old dataAPIConnectReceipt) (dataAPIConnectReceipt, string, error) {
	if old.Stage != "verified" {
		return old, "", errors.New("a verified original receipt is required")
	}
	review, err := client.GetAppReleaseReview(ctx, old.App)
	if err != nil || len(review.ServingDeploymentIDs) != 1 || review.ServingDeploymentIDs[0] == "" {
		return old, "", errors.New("cannot observe a stable candidate deployment")
	}
	candidate := old
	candidate.DeploymentID = review.ServingDeploymentIDs[0]
	if err := dataAPIConnectSnapshot(ctx, client, &candidate, true); err != nil {
		return old, "", err
	}
	app, err := client.GetApp(ctx, old.App)
	if err != nil || app.ID != old.AppID || app.Slug != old.App {
		return old, "", errors.New("candidate app identity changed")
	}
	endpoint, err := dataAPIReadinessURL(app)
	if err != nil {
		return old, "", err
	}
	return candidate, endpoint, nil
}

func requalifyObserveExpected(ctx context.Context, client dataAPIConnectRemote, old, expected dataAPIConnectReceipt, endpoint string) error {
	got, address, err := observeRequalificationCandidate(ctx, client, old)
	if err != nil {
		return err
	}
	if got.DeploymentID != expected.DeploymentID || got.ConfigurationHash != expected.ConfigurationHash || address != endpoint {
		return errors.New("candidate deployment, configuration or endpoint changed")
	}
	return nil
}

// Re-observe the candidate around type generation and the read-only replay.
// This helper has no filesystem writes; a receipt is published only afterward.
func checkRequalification(ctx context.Context, client dataAPIConnectRemote, old dataAPIConnectReceipt, plan dataAPIRequalificationPlan, generate func(context.Context, string) (api.AppTaskResponse, error), replay func(context.Context, string, string) error) (string, error) {
	candidate := plan.Candidate
	if err := requalifyObserveExpected(ctx, client, old, candidate, plan.Endpoint); err != nil {
		return "", err
	}
	task, err := generate(ctx, old.App)
	if err != nil {
		return "", err
	}
	match := dataAPIFingerprint.FindStringSubmatch(task.StdoutTail)
	content := dataAPITypesForMode(task, old.NumericMode)
	if task.DeploymentID != candidate.DeploymentID || len(match) != 2 || match[1] != candidate.Fingerprint || dataAPIConnectHash([]byte(content)) != candidate.TypesHash {
		return "", errors.New("candidate types or fingerprint differ from the reviewed plan")
	}
	if err := requalifyObserveExpected(ctx, client, old, candidate, plan.Endpoint); err != nil {
		return "", err
	}
	if err := replay(ctx, plan.Endpoint, candidate.Fingerprint); err != nil {
		return "", errors.New("authorization checks failed")
	}
	if err := requalifyObserveExpected(ctx, client, old, candidate, plan.Endpoint); err != nil {
		return "", err
	}
	return content, nil
}

func validateRequalificationPlan(plan dataAPIRequalificationPlan, old dataAPIConnectReceipt, collection []byte, identityHash, oldPath, planPath string) error {
	candidate := plan.Candidate
	if plan.Version != 1 || plan.PreviousReceiptHash != requalifyReceiptHash(old) || plan.PreviousDeployment != old.DeploymentID || plan.PreviousFingerprint != old.Fingerprint || plan.PreviousConfigurationHash != old.ConfigurationHash || plan.PreviousTypesHash != old.TypesHash || plan.PreviousCollectionHash != old.CollectionHash || !dataAPIApprovalFingerprint(plan.IdentityMappingHash) || plan.IdentityMappingHash != identityHash || candidate.validate() != nil || candidate.Stage != "deployed" || !dataAPIApprovalFingerprint(candidate.Fingerprint) || !dataAPIApprovalFingerprint(candidate.TypesHash) || candidate.App != old.App || candidate.AppID != old.AppID || candidate.APIHash != old.APIHash || candidate.DatabaseID != old.DatabaseID || candidate.BindingID != old.BindingID || candidate.CredentialGeneration != old.CredentialGeneration || candidate.NumericMode != old.NumericMode || candidate.CollectionHash != dataAPIConnectHash(collection) || !filepath.IsAbs(plan.NewReceipt) || candidate.Output == plan.NewReceipt || candidate.Output == old.Output || candidate.Output == oldPath || plan.NewReceipt == oldPath || plan.NewReceipt == old.Output || planPath == plan.NewReceipt || planPath == candidate.Output {
		return errors.New("original receipt, candidate identity, checks or destinations do not match the reviewed plan")
	}
	return nil
}

func writeNewRequalificationPlan(path string, content []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".gregale-requalify-plan-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	n, err := f.Write(content)
	if err == nil && n != len(content) {
		err = io.ErrShortWrite
	}
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
	return os.Link(f.Name(), path)
}

func requalifyReadFile(path string, limit int64) ([]byte, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("input must be a bounded regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("cannot read bounded input")
	}
	return b, nil
}

// Reject duplicate JSON keys so a reviewed plan cannot have multiple
// interpretations in a reviewer and in the CLI.
func requalifyUniqueJSON(dec *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("plan is too deeply nested")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			token, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := token.(string)
			if !ok || seen[name] {
				return errors.New("duplicate plan field")
			}
			seen[name] = true
			if err := requalifyUniqueJSON(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := requalifyUniqueJSON(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid plan structure")
	}
	_, err = dec.Token()
	return err
}

func requalifyNewPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("a new output path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(absolute); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("output path already exists or cannot be inspected")
	}
	info, err := os.Stat(filepath.Dir(absolute))
	if err != nil || !info.IsDir() {
		return "", errors.New("output directory must exist")
	}
	return absolute, nil
}

func cmdDataAPIConnectRequalify(args []string) int {
	fs := newFlagSet("data-api connect requalification", flag.ContinueOnError)
	planFile := fs.String("requalify-plan", "", "write a candidate plan")
	applyFile := fs.String("requalify-apply", "", "apply a reviewed plan")
	planHash := fs.String("plan-sha256", "", "reviewed plan digest")
	receiptFile := fs.String("receipt", "", "original connection receipt")
	newReceipt := fs.String("new-receipt", "", "new receipt path")
	output := fs.String("output", "", "new types path")
	collection := fs.String("collection", "", "read-only request collection")
	identities := fs.String("identities", "", "identity environment mapping")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || (*planFile == "") == (*applyFile == "") || *collection == "" || *identities == "" || (*planFile != "" && (*planHash != "" || *newReceipt == "" || *output == "")) || (*applyFile != "" && (!dataAPIApprovalFingerprint(*planHash) || *newReceipt != "" || *output != "")) {
		return printErr("Invalid requalification options", errors.New("provide NAME, collection, identities and either --requalify-plan with new paths or --requalify-apply with --plan-sha256"))
	}
	if *receiptFile == "" {
		*receiptFile = ".gregale-data-api-" + fs.Arg(0) + ".connect.json"
	}
	oldPath, err := filepath.Abs(*receiptFile)
	if err != nil {
		return printErr("Invalid original receipt", err)
	}
	lock := flock.New(filepath.Join(os.TempDir(), "gregale-data-connect-"+dataAPIConnectHash([]byte(oldPath))+".lock"))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return printErr("Connection is busy", errors.New("could not lock original receipt"))
	}
	defer func() { _ = lock.Unlock() }()
	old, err := readDataAPIConnectReceipt(oldPath)
	if err != nil || old.App != fs.Arg(0) || old.Stage != "verified" {
		return printErr("Invalid original receipt", errors.New("provide the verified original receipt for this app"))
	}
	oldTypes, err := requalifyReadFile(old.Output, 2*api.DataAPIContractArtifactBytes+1024)
	if err != nil || dataAPIConnectHash(oldTypes) != old.TypesHash {
		return printErr("Invalid original types", errors.New("the recorded types are missing or changed"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if old.APIHash != dataAPIConnectHash([]byte(client.BaseURL())) {
		return printErr("Invalid original receipt", errors.New("receipt belongs to a different control plane"))
	}
	base, err := os.Getwd()
	if err != nil {
		return printErr("Cannot inspect project", err)
	}
	data, err := requalifyReadFile(dataAPISyncPath(base, *collection), 65536)
	if err != nil {
		return printErr("Invalid authorization checks", err)
	}
	identityData, err := requalifyReadFile(dataAPISyncPath(base, *identities), 65536)
	if err != nil {
		return printErr("Invalid identity mapping", err)
	}
	uniqueIdentities := json.NewDecoder(bytes.NewReader(identityData))
	if requalifyUniqueJSON(uniqueIdentities, 0) != nil {
		return printErr("Invalid identity mapping", errors.New("mapping contains invalid or duplicate fields"))
	}
	if _, err := uniqueIdentities.Token(); err != io.EOF {
		return printErr("Invalid identity mapping", errors.New("mapping must contain one JSON document"))
	}
	tmp, err := os.MkdirTemp("", "gregale-requalify-")
	if err != nil {
		return printErr("Cannot prepare checks", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	config := dataAPIHostedReplayConfig{Collection: filepath.Join(tmp, "requests.json"), Identities: filepath.Join(tmp, "identities.json"), ReadOnly: true}
	if err := os.WriteFile(config.Collection, data, 0600); err != nil {
		return printErr("Cannot prepare checks", err)
	}
	if err := os.WriteFile(config.Identities, identityData, 0600); err != nil {
		return printErr("Cannot prepare identity mapping", err)
	}
	if err := prepareDataAPIHostedReplay(base, &config); err != nil {
		return printErr("Invalid authorization checks", err)
	}
	mappingBytes, err := json.Marshal(config.mappings)
	if err != nil {
		return printErr("Invalid identity mapping", err)
	}
	identityHash := dataAPIConnectHash(mappingBytes)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if err := runDataAPIHostedReplay(ctx, &config, "", "", true, false, nil); err != nil {
		return printErr("Invalid authorization checks", errors.New("provide reviewed read-only assertions for two distinct subjects"))
	}
	var plan dataAPIRequalificationPlan
	var planPath string
	if *planFile != "" {
		planPath, err = requalifyNewPath(*planFile)
		if err != nil {
			return printErr("Invalid plan destination", err)
		}
		newPath, e := requalifyNewPath(*newReceipt)
		if e != nil {
			return printErr("Invalid new receipt destination", e)
		}
		typesPath, e := requalifyNewPath(*output)
		if e != nil {
			return printErr("Invalid types destination", e)
		}
		if planPath == newPath || planPath == typesPath || newPath == typesPath || oldPath == newPath || oldPath == typesPath || old.Output == typesPath || old.Output == newPath {
			return printErr("Invalid destinations", errors.New("use separate new plan, receipt and types paths"))
		}
		candidate, endpoint, e := observeRequalificationCandidate(ctx, client, old)
		if e != nil {
			return printErr("Candidate unavailable", e)
		}
		task, e := generateDataAPITypes(ctx, client, old.App)
		if e != nil {
			return printErr("Cannot inspect candidate types", e)
		}
		match := dataAPIFingerprint.FindStringSubmatch(task.StdoutTail)
		if task.DeploymentID != candidate.DeploymentID || len(match) != 2 {
			return printErr("Candidate changed", errors.New("types do not match the observed deployment"))
		}
		content := dataAPITypesForMode(task, old.NumericMode)
		candidate.Fingerprint = match[1]
		candidate.TypesHash = dataAPIConnectHash([]byte(content))
		candidate.CollectionHash = dataAPIConnectHash(data)
		candidate.Output = typesPath
		candidate.Stage = "deployed"
		if err := requalifyObserveExpected(ctx, client, old, candidate, endpoint); err != nil {
			return printErr("Candidate changed", err)
		}
		plan = dataAPIRequalificationPlan{Version: 1, PreviousReceiptHash: requalifyReceiptHash(old), PreviousDeployment: old.DeploymentID, PreviousFingerprint: old.Fingerprint, PreviousConfigurationHash: old.ConfigurationHash, PreviousTypesHash: old.TypesHash, PreviousCollectionHash: old.CollectionHash, IdentityMappingHash: identityHash, Candidate: candidate, Endpoint: endpoint, NewReceipt: newPath}
		encoded, e := json.MarshalIndent(plan, "", "  ")
		if e != nil {
			return printErr("Cannot encode plan", e)
		}
		encoded = append(encoded, '\n')
		if err := writeDataAPIConnectTypes(typesPath, content); err != nil {
			return printErr("Cannot create candidate types", err)
		}
		if err := writeNewRequalificationPlan(planPath, encoded); err != nil {
			return printErr("Cannot create plan", fmt.Errorf("candidate types remain at %q; inspect them before retrying", typesPath))
		}
		if jsonOutput {
			if err := writeNDJSON([]map[string]any{{"event": "requalification_plan", "plan": plan, "plan_sha256": dataAPIConnectHash(encoded), "authorization": "not_tested"}}); err != nil {
				return printErr("Cannot write plan report", err)
			}
		} else {
			_, _ = fmt.Fprintf(osStdout, "Candidate plan %q\nDeployment: %s -> %s\nConfiguration: %s -> %s\nSchema: %s -> %s\nTypes: %s -> %s\nCollection: %s -> %s\nIdentity mapping SHA-256: %s\nCandidate types: %q\nNew receipt: %q\nPlan SHA-256: %s\nReview the plan, candidate types, identity mapping and assertions before applying. Authorization has not been tested.\n", planPath, old.DeploymentID, candidate.DeploymentID, old.ConfigurationHash, candidate.ConfigurationHash, old.Fingerprint, candidate.Fingerprint, old.TypesHash, candidate.TypesHash, old.CollectionHash, candidate.CollectionHash, identityHash, typesPath, newPath, dataAPIConnectHash(encoded))
		}
		return 0
	}
	planPath, err = filepath.Abs(*applyFile)
	if err != nil {
		return printErr("Invalid plan", err)
	}
	encoded, err := requalifyReadFile(planPath, 32768)
	if err != nil || dataAPIConnectHash(encoded) != *planHash {
		return printErr("Invalid plan", errors.New("plan is unavailable or its digest differs from the reviewed digest"))
	}
	unique := json.NewDecoder(bytes.NewReader(encoded))
	if requalifyUniqueJSON(unique, 0) != nil {
		return printErr("Invalid plan", errors.New("plan contains invalid or duplicate fields"))
	}
	if _, err := unique.Token(); err != io.EOF {
		return printErr("Invalid plan", errors.New("plan contains multiple JSON documents"))
	}
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.DisallowUnknownFields()
	if dec.Decode(&plan) != nil || dec.Decode(new(any)) != io.EOF {
		return printErr("Invalid plan", errors.New("plan is malformed"))
	}
	candidate := plan.Candidate
	if err := validateRequalificationPlan(plan, old, data, identityHash, oldPath, planPath); err != nil {
		return printErr("Invalid plan", err)
	}
	if _, err := requalifyNewPath(plan.NewReceipt); err != nil {
		return printErr("Invalid receipt destination", err)
	}
	reviewedTypes, err := requalifyReadFile(candidate.Output, 2*api.DataAPIContractArtifactBytes+1024)
	if err != nil || dataAPIConnectHash(reviewedTypes) != candidate.TypesHash {
		return printErr("Candidate types changed", errors.New("the reviewed types are unavailable or changed"))
	}
	content, err := checkRequalification(ctx, client, old, plan, func(ctx context.Context, slug string) (api.AppTaskResponse, error) {
		return generateDataAPITypes(ctx, client, slug)
	}, func(ctx context.Context, endpoint, fingerprint string) error {
		return runDataAPIHostedReplay(ctx, &config, endpoint, fingerprint, false, false, nil)
	})
	if err != nil {
		return printErr("Requalification failed", err)
	}
	again, err := readDataAPIConnectReceipt(oldPath)
	if err != nil || requalifyReceiptHash(again) != plan.PreviousReceiptHash {
		return printErr("Original receipt changed", errors.New("inspect the receipt before retrying"))
	}
	if err := dataAPIConnectTypesMatch(old.Output, string(oldTypes)); err != nil {
		return printErr("Original types changed", err)
	}
	if err := dataAPIConnectTypesMatch(candidate.Output, content); err != nil {
		return printErr("Candidate types changed", err)
	}
	candidate.Stage = "verified"
	if err := writeDataAPIConnectReceipt(plan.NewReceipt, candidate, true); err != nil {
		return printErr("Cannot create candidate receipt", fmt.Errorf("candidate types remain at %q; inspect them before retrying", candidate.Output))
	}
	if jsonOutput {
		if err := writeNDJSON([]map[string]any{{"event": "requalification_complete", "receipt": plan.NewReceipt, "deployment_id": candidate.DeploymentID, "schema_fingerprint": candidate.Fingerprint, "coverage": "supplied_scenarios_only"}}); err != nil {
			return printErr("Cannot write result", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Requalified deployment %s; types %q; new receipt %q. Only the supplied authorization scenarios were tested.\n", candidate.DeploymentID, candidate.Output, plan.NewReceipt)
	}
	return 0
}
