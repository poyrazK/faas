package main

// ADR-20261122220000: verification contracts. `gregale release check`
// validates gregale.verify.yaml locally and stores it on an app; Gregale then
// runs it against every new deployment before the deployment takes traffic.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

const verifyUsage = "usage: gregale release check <validate|set|get|delete>"

func cmdVerify(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, verifyUsage, "verify")
		return 1
	}
	switch args[0] {
	case "validate":
		return verifyValidate(args[1:])
	case "set":
		return verifySet(args[1:])
	case "get":
		return verifyGet(args[1:])
	case "delete":
		return verifyDelete(args[1:])
	}
	printCommandValidation(os.Stderr, "gregale release check: unknown subcommand %q\n", args[0])
	return unknownSubcommandChoices(os.Stderr, "verify", nestedCliCommand("verify"), args[0])
}

// readVerifyContract reads and validates the contract file.
func readVerifyContract(path string) (verifycontract.Contract, error) {
	if path == "" {
		path = verifycontract.FileName
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return verifycontract.Contract{}, err
	}
	return verifycontract.ParseYAML(data)
}

func verifyAppSlug(app string) (string, error) {
	slug, err := freezeAppFlag(app)
	if err != nil {
		return "", err
	}
	if slug == "" {
		return "", errors.New("pass --app <slug>, or link this checkout with gregale link <project> --app <slug>")
	}
	return slug, nil
}

func verifyValidate(args []string) int {
	fs := newFlagSet("verify validate", flag.ContinueOnError)
	file := fs.String("file", verifycontract.FileName, "contract file")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	contract, err := readVerifyContract(*file)
	if err != nil {
		return printErr("Invalid verification contract", err)
	}
	_, sum, err := contract.Canonical()
	if err != nil {
		return printErr("Invalid verification contract", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"valid": true, "contract_sha256": sum, "checks": verifyCheckSummary(contract)}))
	}
	_, _ = fmt.Fprintf(osStdout, "%s is valid: %d checks, %ds budget.\n", *file, len(contract.Checks), contract.Timeout())
	for _, c := range verifyCheckSummary(contract) {
		_, _ = fmt.Fprintf(osStdout, "  %-24s %-4s %s\n", c["name"], c["kind"], c["policy"])
	}
	return 0
}

func verifyCheckSummary(contract verifycontract.Contract) []map[string]string {
	out := make([]map[string]string, 0, len(contract.Checks))
	for _, c := range contract.Checks {
		policy := "required"
		if c.Advisory {
			policy = "advisory"
		}
		if c.Mutating() {
			policy += ", previews and branches only"
		}
		out = append(out, map[string]string{"name": c.Name, "kind": c.Kind(), "policy": policy})
	}
	return out
}

func verifySet(args []string) int {
	fs := newFlagSet("verify set", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	file := fs.String("file", verifycontract.FileName, "contract file")
	expected := fs.Int64("expected-revision", -1, "refuse unless the stored contract has this revision (0: none stored)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	contract, err := readVerifyContract(*file)
	if err != nil {
		return printErr("Invalid verification contract", err)
	}
	slug, err := verifyAppSlug(*app)
	if err != nil {
		return printErr("App required", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	stored, err := uploadVerifyContract(context.Background(), client, slug, contract, *expected)
	if err != nil {
		return printErr("Could not store verification contract", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(stored))
	}
	_, _ = fmt.Fprintf(osStdout, "Stored verification contract for %s (revision %d, %d checks). New deployments must pass it before they take traffic.\n", slug, stored.Revision, stored.CheckCount)
	return 0
}

func uploadVerifyContract(ctx context.Context, client *api.Client, slug string, contract verifycontract.Contract, expected int64) (api.VerificationContract, error) {
	raw, _, err := contract.Canonical()
	if err != nil {
		return api.VerificationContract{}, err
	}
	req := api.PutVerificationContractRequest{Contract: raw}
	if expected >= 0 {
		req.ExpectedRevision = &expected
	}
	return client.PutVerificationContract(ctx, slug, req)
}

func verifyGet(args []string) int {
	fs := newFlagSet("verify get", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, err := verifyAppSlug(*app)
	if err != nil {
		return printErr("App required", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	stored, err := client.GetVerificationContract(context.Background(), slug)
	if err != nil {
		return printErr("Could not read verification contract", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(stored))
	}
	contract, err := verifycontract.ParseJSON(stored.Contract)
	if err != nil {
		return printErr("Stored contract is not readable by this CLI version", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Verification contract for %s: revision %d, %d checks, sha256 %s\n", slug, stored.Revision, stored.CheckCount, stored.ContractSHA256)
	for _, c := range verifyCheckSummary(contract) {
		_, _ = fmt.Fprintf(osStdout, "  %-24s %-4s %s\n", c["name"], c["kind"], c["policy"])
	}
	return 0
}

func verifyDelete(args []string) int {
	fs := newFlagSet("verify delete", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, err := verifyAppSlug(*app)
	if err != nil {
		return printErr("App required", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteVerificationContract(context.Background(), slug, nil); err != nil {
		return printErr("Could not delete verification contract", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"deleted": true, "app": slug}))
	}
	_, _ = fmt.Fprintf(osStdout, "Deleted the verification contract of %s.\n", slug)
	return 0
}

// renderVerificationContractReport prints a deployment's contract report
// under its hosting receipt.
func renderVerificationContractReport(w io.Writer, report *verifycontract.Report) {
	if report == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "%-14s %s (%s, %d ms)\n", "verify:", report.Status, report.Environment, report.DurationMS)
	for _, c := range report.Checks {
		policy := ""
		if !c.Required {
			policy = " advisory"
		}
		line := fmt.Sprintf("  %-8s %-24s %-4s%s", c.Status, c.Name, c.Kind, policy)
		if c.HTTPStatus != 0 {
			line += fmt.Sprintf(" HTTP %d", c.HTTPStatus)
		}
		if len(c.Failures) > 0 {
			line += ": " + strings.Join(c.Failures, "; ")
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

// deployVerificationContract stores sourceDir's gregale.verify.yaml on the
// app before a deploy, so the new deployment is checked against it. An
// invalid file stops the deploy. When the capability is not released to the
// account the route answers 404: the deploy continues and says the contract
// is not enforced.
func deployVerificationContract(ctx context.Context, client *api.Client, slug, sourceDir string) error {
	if sourceDir == "" {
		return nil
	}
	path := filepath.Join(sourceDir, verifycontract.FileName)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("%s: %w", verifycontract.FileName, err)
	}
	contract, err := readVerifyContract(path)
	if err != nil {
		return fmt.Errorf("%s: %w", verifycontract.FileName, err)
	}
	stored, err := uploadVerifyContract(ctx, client, slug, contract, -1)
	if isNotFound(err) {
		_, _ = fmt.Fprintf(os.Stderr, "warning: %s found, but verification contracts are not enabled for this account; this deployment is not checked against it\n", verifycontract.FileName)
		return nil
	}
	if err != nil {
		return err
	}
	if !jsonOutput {
		PrintOK(osStdout, "Verification contract applied: %d checks run before this deployment takes traffic", stored.CheckCount)
	}
	return nil
}

// verifyProductionScope mirrors state.IsProductionScope: the production
// serving lane is the unscoped, default or production scope.
func verifyProductionScope(scope string) bool {
	return scope == "" || scope == "default" || scope == "production"
}
