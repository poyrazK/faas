// adr: 650 — inspect once and compare the schema and generated client contract in CI.
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
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPICheckTypes struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Matches bool   `json:"matches"`
}

type dataAPICheckReport struct {
	Version             int               `json:"version"`
	App                 string            `json:"app,omitempty"`
	TaskID              string            `json:"task_id,omitempty"`
	DeploymentID        string            `json:"deployment_id,omitempty"`
	BaselineFingerprint string            `json:"baseline_fingerprint"`
	CurrentFingerprint  string            `json:"current_fingerprint"`
	Drift               bool              `json:"drift"`
	Breaking            int               `json:"breaking"`
	Approved            int               `json:"approved"`
	Unapproved          int               `json:"unapproved"`
	Changes             []dataAPIChange   `json:"changes"`
	Types               dataAPICheckTypes `json:"types"`
	Passed              bool              `json:"passed"`
}

func cmdDataAPICheck(args []string) int {
	fs := newFlagSet("data-api check", flag.ContinueOnError)
	baseline := fs.String("baseline", "", "committed JSON contract baseline")
	current := fs.String("current", "", "saved current contract for an offline check")
	types := fs.String("types", "", "committed TypeScript types to compare without rewriting")
	approvalPath := fs.String("approval", "", "reviewed breaking changes bound to both fingerprints")
	timeout := fs.Duration("timeout", 2*time.Minute, "entire check deadline (maximum 1h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	validTarget := (*current == "" && fs.NArg() == 1 && api.ValidAppSlug(fs.Arg(0))) || (*current != "" && fs.NArg() == 0)
	if !validTarget || *baseline == "" || *types == "" || *timeout <= 0 || *timeout > time.Hour {
		PrintUsage(os.Stderr, "usage: gregale data-api check [NAME | --current FILE] --baseline FILE --types FILE [--approval FILE] [--timeout DURATION]", "data-api")
		return 1
	}
	// Invalid local inputs never create an owner task. Missing types are a check
	// failure rather than an input error, so compatibility results remain visible.
	before, err := readDataAPIContractFile(*baseline)
	if err != nil {
		return printErr("Invalid baseline", err)
	}
	var approval *dataAPIApproval
	if *approvalPath != "" {
		approval, err = readDataAPIApprovalFile(*approvalPath)
		if err != nil {
			return printErr("Invalid compatibility approval", err)
		}
		if approval.BaselineFingerprint != before.Fingerprint {
			return printErr("Invalid compatibility approval", errors.New("baseline fingerprint does not match"))
		}
	}
	committed, typeResult, err := readDataAPICheckTypes(*types)
	if err != nil {
		return printErr("Could not read committed types", err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		return printErr("Node.js required", errors.New("install Node.js 22 or newer for the embedded type generator"))
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	var after dataAPIContract
	var task api.AppTaskResponse
	if *current != "" {
		after, err = readDataAPIContractFile(*current)
	} else {
		client, clientErr := authedClient()
		if clientErr != nil {
			return printErr("Not logged in", clientErr)
		}
		task, err = generateDataAPIContract(ctx, client, fs.Arg(0))
		if err == nil {
			after, err = parseDataAPIContract([]byte(task.StdoutTail))
		}
	}
	if err != nil {
		return printErr("Schema check failed", err)
	}
	changes := diffDataAPISnapshots(before.Parsed, after.Parsed)
	if err := applyDataAPIApproval(approval, before.Fingerprint, after.Fingerprint, changes); err != nil {
		return printErr("Invalid compatibility approval", err)
	}
	generated, err := renderDataAPICheckTypes(ctx, after)
	if err != nil {
		return printErr("Type comparison failed", err)
	}
	if typeResult.Status == "pending" {
		typeResult.Matches = bytes.Equal(committed, generated)
		typeResult.Status = "stale"
		if typeResult.Matches {
			typeResult.Status = "current"
		}
	}
	report := dataAPICheckReport{Version: 1, App: fs.Arg(0), TaskID: task.ID, DeploymentID: task.DeploymentID, BaselineFingerprint: before.Fingerprint, CurrentFingerprint: after.Fingerprint, Drift: before.Fingerprint != after.Fingerprint, Changes: changes, Types: typeResult}
	for _, change := range changes {
		if change.Breaking {
			report.Breaking++
			if change.Approved {
				report.Approved++
			}
		}
	}
	report.Unapproved = report.Breaking - report.Approved
	report.Passed = report.Unapproved == 0 && report.Types.Matches
	if err := writeDataAPICheckReport(report); err != nil {
		return printErr("Could not write check report", err)
	}
	if !report.Passed {
		return 1
	}
	return 0
}

func readDataAPICheckTypes(path string) ([]byte, dataAPICheckTypes, error) {
	result := dataAPICheckTypes{Path: path, Status: "pending"}
	file, err := openCustomerFile(path)
	if errors.Is(err, os.ErrNotExist) {
		result.Status = "missing"
		return nil, result, nil
	}
	if err != nil {
		return nil, result, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, api.AppTaskDefaultMaxOutputBytes+1))
	if err != nil {
		return nil, result, err
	}
	if len(content) > api.AppTaskDefaultMaxOutputBytes {
		result.Status = "too_large"
		return nil, result, nil
	}
	return content, result, nil
}

// Rendering imports only embedded code and Node builtins. No database binding,
// npm installation, caller script or second catalog inspection is involved.
func renderDataAPICheckTypes(ctx context.Context, contract dataAPIContract) ([]byte, error) {
	runtime, err := os.MkdirTemp("", "gregale-data-api-check-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(runtime) }()
	for _, name := range []string{"type-render.mjs", "config.mjs"} {
		content, err := templates.FS.ReadFile("data-api/" + name)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(runtime, name), content, 0600); err != nil {
			return nil, err
		}
	}
	input, err := json.Marshal(contract)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	const script = `import {readFileSync} from 'node:fs'; import {pathToFileURL} from 'node:url'; const {generate}=await import(pathToFileURL(process.argv[1])); const c=JSON.parse(readFileSync(0,'utf8')); process.stdout.write(generate(c.snapshot,c.fingerprint));`
	command := exec.CommandContext(ctx, "node", "--input-type=module", "--eval", script, filepath.Join(runtime, "type-render.mjs")) //nolint:gosec // Fixed script and embedded module; schema travels through stdin, never code.
	command.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	command.Stdout = &dataAPICheckOutput{writer: &output, remaining: api.AppTaskDefaultMaxOutputBytes}
	command.Stderr = io.Discard
	command.WaitDelay = 2 * time.Second
	configureDataAPISyncProcess(command)
	defer cleanupDataAPISyncProcess(command)
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("embedded type generation failed; check Node.js and schema/output limits")
	}
	return output.Bytes(), nil
}

type dataAPICheckOutput struct {
	writer    io.Writer
	remaining int
}

func (w *dataAPICheckOutput) Write(content []byte) (int, error) {
	if len(content) > w.remaining {
		return 0, errors.New("generated types exceed the task output limit")
	}
	n, err := w.writer.Write(content)
	w.remaining -= n
	return n, err
}

func writeDataAPICheckReport(report dataAPICheckReport) error {
	if jsonOutput {
		return writeJSON(report)
	}
	for _, change := range report.Changes {
		label := "compatible"
		if change.Breaking {
			label = "breaking"
		}
		if change.Approved {
			label = "approved breaking"
		}
		if _, err := fmt.Fprintf(osStdout, "%s %q: %s\n", label, change.Path, change.Reason); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(osStdout, "%d changes, %d breaking, %d approved, %d unapproved\nTypes %q: %s\nCheck passed: %t\n", len(report.Changes), report.Breaking, report.Approved, report.Unapproved, report.Types.Path, report.Types.Status, report.Passed)
	return err
}
