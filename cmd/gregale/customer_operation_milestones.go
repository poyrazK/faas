// adr: 640
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type customerMilestoneCommand struct {
	app, id string
	self    bool
	options api.OperationMilestoneListOptions
}

func parseCustomerMilestoneCommand(args []string) (customerMilestoneCommand, error) {
	var c customerMilestoneCommand
	fs := newFlagSet("customer-operations milestones", flag.ContinueOnError)
	fs.StringVar(&c.app, "app", "", "account app slug, or app UUID for tenant business-reference reads")
	fs.BoolVar(&c.self, "self", false, "derive customer ownership from credentials")
	fs.StringVar(&c.options.Scope, "scope", "", "explicit environment for business-reference timeline")
	fs.StringVar(&c.options.SubjectType, "subject-type", "", "business reference type")
	fs.StringVar(&c.options.SubjectID, "subject-id", "", "exact business reference ID")
	fs.StringVar(&c.options.Workflow, "workflow", "", "workflow name for one business workflow run")
	fs.StringVar(&c.options.WorkflowInstanceID, "workflow-instance-id", "", "exact workflow run ID")
	fs.StringVar(&c.options.TenantID, "tenant", "", "account operator tenant UUID filter")
	fs.StringVar(&c.options.Cursor, "cursor", "", "next milestone page cursor")
	fs.StringVar(&c.options.WorkflowStateCursor, "workflow-state-cursor", "", "next workflow state history page cursor")
	fs.BoolVar(&c.options.WorkflowStaleOnly, "stale-only", false, "show only workflow runs past an app-declared state age threshold")
	fs.IntVar(&c.options.Limit, "limit", api.OperationHistoryPageDefault, "milestone page size")
	flags, ids := splitArgsForFlags(args, "self", "stale-only")
	if err := fs.Parse(flags); err != nil {
		return c, err
	}
	if len(ids) > 1 || fs.NArg() != 0 || c.options.Limit < 1 || c.options.Limit > api.OperationHistoryPageMax ||
		len(c.options.Cursor) > api.OperationHistoryCursorMaxBytes || len(c.options.WorkflowStateCursor) > api.OperationHistoryCursorMaxBytes {
		return c, fmt.Errorf("invalid milestone arguments or page bounds")
	}
	if len(ids) == 1 {
		c.id = ids[0]
		id, err := uuid.Parse(c.id)
		if err != nil || id == uuid.Nil || c.options.Scope != "" || c.options.SubjectType != "" || c.options.SubjectID != "" || c.options.TenantID != "" ||
			c.options.Workflow != "" || c.options.WorkflowInstanceID != "" || c.options.WorkflowStateCursor != "" || c.options.WorkflowStaleOnly || c.self && c.app != "" {
			return c, fmt.Errorf("operation timeline requires an ID; omit reference filters and --app with --self")
		}
	} else {
		if c.app == "" || api.ValidateScope(c.options.Scope) != nil {
			return c, fmt.Errorf("reference timeline requires --app, --scope, --subject-type and --subject-id")
		}
		if (c.options.Workflow == "") != (c.options.WorkflowInstanceID == "") || c.options.Workflow != "" &&
			(api.ValidateOperationWorkflowName(c.options.Workflow) != nil || api.ValidateOperationWorkflowInstanceID(c.options.WorkflowInstanceID) != nil) ||
			c.options.WorkflowStateCursor != "" && c.options.Workflow == "" {
			return c, fmt.Errorf("workflow state history requires a valid --workflow and --workflow-instance-id pair")
		}
		if err := api.ValidateOperationSubject(api.OperationSubject{Type: c.options.SubjectType, ID: c.options.SubjectID}); err != nil {
			return c, err
		}
		if c.self {
			id, err := uuid.Parse(c.app)
			if err != nil || id == uuid.Nil || c.options.TenantID != "" {
				return c, fmt.Errorf("tenant reference timeline requires app UUID and derives tenant ownership")
			}
			c.options.AppID = id.String()
		}
	}
	if !c.self && strings.TrimSpace(c.app) == "" {
		return c, fmt.Errorf("account timeline requires --app")
	}
	return c, nil
}

func cmdCustomerOperationMilestones(args []string) int {
	c, err := parseCustomerMilestoneCommand(args)
	if err != nil {
		return printErr("Invalid milestone command", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	page, err := readCustomerMilestones(ctx, client, c)
	if err != nil {
		return printErr("Milestone timeline failed", err)
	}
	if err = printCustomerMilestones(osStdout, page, jsonOutput); err != nil {
		return printErr("Print milestones", err)
	}
	return 0
}

func readCustomerMilestones(ctx context.Context, client *api.Client, c customerMilestoneCommand) (api.OperationMilestonesResponse, error) {
	if c.id != "" {
		if c.self {
			return client.GetPlatformTenantSelfOperationMilestones(ctx, c.id, c.options)
		}
		return client.GetAccountOperationMilestones(ctx, c.app, c.id, c.options)
	}
	if c.self {
		return client.ListPlatformTenantSelfBusinessMilestones(ctx, c.options)
	}
	return client.ListAccountBusinessMilestones(ctx, c.app, c.options)
}

func printCustomerMilestones(out io.Writer, page api.OperationMilestonesResponse, jsonMode bool) error {
	if jsonMode {
		return json.NewEncoder(out).Encode(page)
	}
	for _, state := range page.WorkflowStates {
		status := "active"
		if state.Terminal {
			status = "terminal"
		}
		if _, err := fmt.Fprintf(out, "workflow-state\t%s\tinstance=%s\tstatus=%s\trevision=%d\tupdated=%s\n", state.Workflow, state.InstanceID, status,
			state.Revision, state.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999Z")); err != nil {
			return err
		}
		staleAfter := "none"
		if state.StaleAfterSeconds > 0 {
			staleAfter = (time.Duration(state.StaleAfterSeconds) * time.Second).String()
		}
		if _, err := fmt.Fprintf(out, "workflow-age\t%s\tinstance=%s\tstale=%t\tstale-after=%s\toccurred=%s\n", state.Workflow, state.InstanceID, state.Stale, staleAfter,
			state.OccurredAt.UTC().Format("2006-01-02T15:04:05.999999Z")); err != nil {
			return err
		}
	}
	for _, transition := range page.WorkflowStateHistory {
		from := ""
		if transition.FromState != "" {
			from = "\tfrom=" + transition.FromState
		}
		if _, err := fmt.Fprintf(out, "workflow-transition\t%s\tinstance=%s\trevision=%d%s\tto=%s\toccurred=%s\tpublished=%s\toperation=%s\n",
			transition.Workflow, transition.InstanceID, transition.Revision, from, transition.State,
			transition.OccurredAt.UTC().Format("2006-01-02T15:04:05.999999Z"),
			transition.PublishedAt.UTC().Format("2006-01-02T15:04:05.999999Z"), transition.OperationID); err != nil {
			return err
		}
	}
	for _, m := range page.Milestones {
		workflow := make([]string, 0, len(m.WorkflowSteps))
		for _, step := range m.WorkflowSteps {
			workflow = append(workflow, step.Workflow+":"+step.InstanceID+":"+strconv.Itoa(step.Position)+":"+step.Label)
		}
		workflowField := ""
		if len(workflow) > 0 {
			workflowField = "\tworkflow=" + strings.Join(workflow, ",")
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\toperation=%s\toccurred=%s%s\tpayload=%s\n", m.ID, m.Name, m.OperationID,
			m.OccurredAt.UTC().Format("2006-01-02T15:04:05.999999Z"), workflowField, m.Payload); err != nil {
			return err
		}
	}
	if page.NextCursor != "" {
		if _, err := fmt.Fprintf(out, "Next page: --cursor %s\n", page.NextCursor); err != nil {
			return err
		}
	}
	if page.NextWorkflowStateCursor != "" {
		_, err := fmt.Fprintf(out, "Next workflow state page: --workflow-state-cursor %s\n", page.NextWorkflowStateCursor)
		return err
	}
	return nil
}
