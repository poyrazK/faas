// adr: 1668 — read-only owner preflight and existing binding rollout evidence.
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

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var dataAPIRotationActions = map[string]string{
	"owner_authority":     "Use the managed migration connection with its stable schema-owner role and the api schema.",
	"enrollment_tables":   "Enroll coordinated rotation with the matching owner tool; explicitly repair private table ownership, shape or ACL drift.",
	"refresh_hooks":       "Install the matching rotation tool and explicitly repair protected refresh hook versions, ownership or ACLs.",
	"binding_enrollment":  "Enroll this binding UUID and current managed login in the same database and resource scope.",
	"source_role":         "Repair the recorded source login identity, OID, restricted attributes and memberships through the managed owner workflow.",
	"serving_grants":      "Restore private schema usage and execution of the two fixed serving refresh hooks for the source login.",
	"sql_deadline":        "Use the enrolled DATA_API_SQL_DEADLINE mode and repair the matching protected deadline hooks and grants.",
	"rpc_grants":          "Review approved RPC ownership, invoker security, annotations and grants; verify them through coordinated pause and resume.",
	"catalog_witness":     "Review catalog and privilege drift, then use coordinated pause, verification and resume before retrying rotation.",
	"serving_contract":    "Verify schema and source-login permissions through coordinated refresh before retrying rotation.",
	"rls_compatibility":   "Review policies tied to the source SQL login. Use an owner-reviewed claim-based policy applicable to replacement logins, then verify through coordinated refresh.",
	"baseline_privileges": "Restore the managed api table, sequence and schema grants; remove unsupported owner or external table privileges before verifying coordinated refresh.",
}

type dataAPIRotationCheck struct {
	Code       string `json:"code"`
	Status     string `json:"status"`
	NextAction string `json:"next_action"`
}

type dataAPIRotationOwnerReport struct {
	Version     int                    `json:"version"`
	Kind        string                 `json:"kind"`
	BindingID   string                 `json:"binding_id"`
	Ready       bool                   `json:"ready"`
	Complete    bool                   `json:"complete"`
	Paused      *bool                  `json:"paused,omitempty"`
	MemberCount *int                   `json:"member_count,omitempty"`
	Checks      []dataAPIRotationCheck `json:"checks"`
}

type dataAPIRotationReport struct {
	Version    int                         `json:"version"`
	App        string                      `json:"app"`
	BindingID  string                      `json:"binding_id"`
	Ready      *bool                       `json:"ready,omitempty"`
	Owner      *dataAPIRotationOwnerReport `json:"owner,omitempty"`
	Rollout    dataAPIRotationRollout      `json:"rollout"`
	Limitation string                      `json:"limitation"`
}

func validDataAPIRotationBinding(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func cmdDataAPIRotation(args []string) int {
	if len(args) == 0 || (args[0] != "preflight" && args[0] != "status") {
		return printErr("Invalid rotation command", errors.New("use data-api rotation preflight|status NAME --binding UUID; preflight also requires --owner-check FILE"))
	}
	fs := newFlagSet("data-api rotation "+args[0], flag.ContinueOnError)
	binding := fs.String("binding", "", "managed Data API binding UUID")
	ownerPath := fs.String("owner-check", "", "JSON argv/directory for the trusted local read-only owner preflight command")
	timeout := fs.Duration("timeout", api.DataAPIRotationCheckTimeout, "deadline for the check")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	preflight := args[0] == "preflight"
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || !validDataAPIRotationBinding(*binding) || preflight != (*ownerPath != "") || *timeout <= 0 || *timeout > api.DataAPIRotationMaxTimeout {
		return printErr("Invalid rotation arguments", errors.New("use preflight|status NAME --binding UUID [--owner-check FILE for preflight] [--timeout DURATION up to 1h]"))
	}
	var command dataAPISyncCommand
	var err error
	if preflight {
		command, err = loadDataAPIRotationCommand(*ownerPath)
		if err != nil {
			return printErr("Invalid owner preflight configuration", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	interrupted, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupted, *timeout)
	defer cancel()
	rollout, err := readDataAPIRotationRollout(ctx, client, fs.Arg(0), *binding, "")
	if err != nil {
		return printErr("Could not inspect Data API rotation", err)
	}
	report := dataAPIRotationReport{Version: 1, App: fs.Arg(0), BindingID: *binding, Rollout: rollout,
		Limitation: "Catalog preflight is advisory and may change before preparation. It does not create a candidate, evaluate arbitrary RLS expressions, prove runtime credential use, or verify provider revocation. The provider rechecks preparation transactionally."}
	if preflight {
		owner, err := runDataAPIRotationOwnerCheck(ctx, command, *binding)
		if err != nil {
			return printErr("Could not check rotation prerequisites", err)
		}
		report.Owner = &owner
		ready := owner.Ready && rollout.Complete && rollout.BindingState == "ready" && !rollout.RotationPending
		report.Ready = &ready
	}
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return printErr("Could not write rotation report", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "%s: binding %s, generation %d\nRotation: %s; retirement: %s\n%s\n", report.App, report.BindingID, rollout.CredentialGeneration, rollout.State, rollout.Retirement, rollout.NextAction)
		if report.Owner != nil {
			_, _ = fmt.Fprintf(osStdout, "Preflight ready: %t\n", *report.Ready)
			for _, check := range report.Owner.Checks {
				if check.Status != "passed" {
					_, _ = fmt.Fprintf(osStdout, "%s: %s — %s\n", check.Code, check.Status, check.NextAction)
				}
			}
		}
		_, _ = fmt.Fprintln(osStdout, report.Limitation)
	}
	if !rollout.Complete || (report.Ready != nil && !*report.Ready) {
		return 1
	}
	return 0
}

func decodeDataAPIRotationJSON(content []byte, result any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return errors.New("expected one valid rotation JSON object with known fields")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("expected one rotation JSON object")
	}
	return nil
}

func loadDataAPIRotationCommand(path string) (dataAPISyncCommand, error) {
	var command dataAPISyncCommand
	file, err := openCustomerFile(path)
	if err != nil {
		return command, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, api.DataAPIRotationConfigMaxBytes+1))
	if err != nil || len(content) > api.DataAPIRotationConfigMaxBytes {
		return command, errors.New("could not read bounded owner preflight configuration")
	}
	if err := decodeDataAPIRotationJSON(content, &command); err != nil {
		return command, err
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return command, err
	}
	if err := prepareDataAPISyncCommand(base, &command); err != nil {
		return command, errors.New("owner command must have executable argv and an existing directory")
	}
	return command, nil
}

type dataAPIRotationOutput struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *dataAPIRotationOutput) Write(p []byte) (int, error) {
	if len(p) > api.DataAPIRotationReportMaxBytes-b.buffer.Len() {
		b.exceeded = true
		return 0, errors.New("owner preflight output exceeds limit")
	}
	return b.buffer.Write(p)
}

func runDataAPIRotationOwnerCheck(ctx context.Context, step dataAPISyncCommand, binding string) (dataAPIRotationOwnerReport, error) {
	var report dataAPIRotationOwnerReport
	var output dataAPIRotationOutput
	command := exec.CommandContext(ctx, step.Command[0], step.Command[1:]...) //nolint:gosec // Explicit trusted developer argv; no shell.
	command.Dir, command.Env = step.Directory, os.Environ()
	// Owner output is untrusted and may contain a DSN. Only validated fields
	// reach the report. Never relay child stderr or command arguments.
	command.Stdout, command.Stderr = &output, io.Discard
	command.WaitDelay = 2 * time.Second
	configureDataAPISyncProcess(command)
	defer cleanupDataAPISyncProcess(command)
	err := command.Run()
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	if output.exceeded {
		return report, errors.New("owner preflight output exceeds the report limit")
	}
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return report, errors.New("owner preflight command failed; verify the configured tool and migration connection")
	}
	if err := decodeDataAPIRotationJSON(output.buffer.Bytes(), &report); err != nil {
		return report, errors.New("owner preflight did not return a bounded valid report; use the matching refresh-rotation.mjs preflight command")
	}
	if err := validateDataAPIRotationOwnerReport(&report, binding); err != nil {
		return report, err
	}
	if exit != nil && report.Ready {
		return report, errors.New("owner preflight exit status disagrees with its report")
	}
	return report, nil
}

func validateDataAPIRotationOwnerReport(report *dataAPIRotationOwnerReport, binding string) error {
	invalid := errors.New("owner preflight report is incompatible, incomplete or belongs to a different binding")
	if report.Version != 1 || report.Kind != "data_api_rotation_preflight" || report.BindingID != binding || len(report.Checks) != len(dataAPIRotationActions) || (report.MemberCount != nil && *report.MemberCount < 1) {
		return invalid
	}
	seen, ready, complete := map[string]bool{}, true, true
	for i := range report.Checks {
		check := &report.Checks[i]
		action, ok := dataAPIRotationActions[check.Code]
		if !ok || seen[check.Code] || (check.Status != "passed" && check.Status != "blocked" && check.Status != "unknown") {
			return invalid
		}
		seen[check.Code] = true
		check.NextAction = action
		ready = ready && check.Status == "passed"
		complete = complete && check.Status != "unknown"
	}
	if ready != report.Ready || complete != report.Complete || (report.Ready && (report.MemberCount == nil || report.Paused == nil)) {
		return invalid
	}
	return nil
}
