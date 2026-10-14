package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAutomationsInitialEvidence(args []string) int {
	fs := newFlagSet("automations failure-recovery-evidence", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	review := fs.String("review", "", "initial recovery review UUID")
	repair := fs.Bool("repair", false, "save a verified exact-generation terminal outcome")
	request := fs.String("request-id", "", "stable UUID for repair replay")
	reason := fs.String("reason", "", "operator repair reason")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *app == "" || !workflowUUIDPattern.MatchString(*review) || !*repair && (*request != "" || *reason != "") {
		return printErr("Invalid arguments", errors.New("provide --app and --review; --request-id and --reason require --repair"))
	}
	req := api.WorkflowInitialEvidenceRepairRequest{RequestID: *request, Reason: *reason}
	if *repair {
		if err := req.Validate(); err != nil {
			return printErr("Invalid repair", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.WorkflowInitialEvidenceDiagnosis
	if *repair {
		out, err = c.RepairWorkflowInitialEvidence(ctx, *app, *review, req)
	} else {
		out, err = c.DiagnoseWorkflowInitialEvidence(ctx, *app, *review)
	}
	if err != nil {
		return printErr("Could not inspect or repair evidence", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Review %s | %s | repairable %t | run %s generation %d\n", oneLine(out.ReviewID), oneLine(out.Reason), out.Repairable, oneLine(out.RunID), out.ResumeCount)
	if out.Repair != nil {
		_, _ = fmt.Fprintf(osStdout, "Repair %s | %s | %s\n", oneLine(out.Repair.RequestID), oneLine(out.Repair.Outcome), out.Repair.RepairedAt.String())
	}
	return 0
}
