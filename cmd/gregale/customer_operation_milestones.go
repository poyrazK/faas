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
	if instance := page.WorkflowInstance; instance != nil && instance.Decision != nil {
		d := instance.Decision
		if _, err := fmt.Fprintf(out, "workflow-decision\t%s\tinstance=%s\treason=%s\tattention=%t\trevision=%d\t%s\n", instance.Workflow, instance.InstanceID, d.Reason, d.NeedsAttention, d.StateRevision, d.Explanation); err != nil {
			return err
		}

		for _, blocker := range d.Blockers {
			if _, err := fmt.Fprintf(out, "workflow-blocker\toperation=%s\tcode=%s\t%s\n", blocker.Operation, blocker.Code, blocker.Description); err != nil {
				return err
			}
		}
		for _, action := range d.NextActions {
			if _, err := fmt.Fprintf(out, "workflow-next-action\toperation=%s\tfrom=%s\tto=%s\trequired-milestones=%s\n", action.Operation, action.From, action.To, strings.Join(action.RequiredMilestones, ",")); err != nil {
				return err
			}
		}
	}
	if instance := page.WorkflowInstance; instance != nil {
		if overview := instance.Readiness; overview != nil {
			for _, item := range overview.Items {
				if _, err := fmt.Fprintf(out, "workflow-readiness\toperation=%s\tfrom=%s\tto=%s\tdeclared=%t\tready=%t\trevision=%d\treasons=%s\tmissing-milestones=%s\tadvisories=%s\n", item.Transition.Operation, item.Transition.From, item.Transition.To, item.Declared, item.Ready, item.StateRevision, strings.Join(item.Reasons, ","), strings.Join(item.MissingMilestones, ","), strings.Join(item.Advisories, ",")); err != nil {
					return err
				}
				for _, unmet := range item.UnmetEffects {
					if _, err := fmt.Fprintf(out, "workflow-effect-required\toperation=%s\tmilestone=%s\tcode=%s\tversion=%s\treason=%s\n", item.Transition.Operation, unmet.Requirement.Milestone, unmet.Requirement.Code, unmet.Requirement.Version, unmet.Reason); err != nil {
						return err
					}
				}
				for _, unmet := range item.UnmetInvariants {
					if _, err := fmt.Fprintf(out, "workflow-invariant-required\toperation=%s\tmilestone=%s\tcode=%s\tversion=%s\treason=%s\n", item.Transition.Operation, unmet.Requirement.Milestone, unmet.Requirement.Code, unmet.Requirement.Version, unmet.Reason); err != nil {
						return err
					}
				}
				for _, blocker := range item.InvariantBlockers {
					if _, err := fmt.Fprintf(out, "workflow-invariant-blocker\toperation=%s\tcode=%s\tdescription=%s\n", blocker.Operation, blocker.Code, blocker.Description); err != nil {
						return err
					}
				}
				for _, workflow := range item.MissingDependencyWorkflows {
					if _, err := fmt.Fprintf(out, "workflow-prerequisite-required\toperation=%s\tworkflow=%s\n", item.Transition.Operation, workflow); err != nil {
						return err
					}
				}
				for _, policy := range item.MissingPolicies {
					if _, err := fmt.Fprintf(out, "workflow-policy-required\toperation=%s\tmilestone=%s\trule=%s\tversion=%s\tcode=%s\n", item.Transition.Operation, policy.Milestone, policy.RuleID, policy.RuleVersion, policy.Code); err != nil {
						return err
					}
				}

			}
		}
		if trace := instance.DependencyTrace; trace != nil {
			if _, err := fmt.Fprintf(out, "workflow-trace\tvisited=%d\texamined-dependencies=%d\ttruncated=%t\tlimits=%s\n", trace.VisitedWorkflowCount, trace.ExaminedDependencyCount, trace.Truncated, strings.Join(trace.LimitsReached, ",")); err != nil {
				return err
			}
			for _, finding := range trace.Findings {
				if _, err := fmt.Fprintf(out, "workflow-root-cause\tkind=%s\tlimit=%s\t%s\n", finding.Kind, finding.Limit, finding.Explanation); err != nil {
					return err
				}
				for position, reference := range finding.Path {
					if _, err := fmt.Fprintf(out, "  trace-step\tposition=%d\tsubject=%s:%s\tworkflow=%s\tinstance=%s\trequired-outcome=%s\n", position, reference.SubjectType, reference.SubjectID, reference.Workflow, reference.InstanceID, reference.RequiredOutcomeCode); err != nil {
						return err
					}
				}
				if finding.State != nil {
					state := finding.State
					if _, err := fmt.Fprintf(out, "  trace-state\tstate=%s\trevision=%d\toutcome=%s\tdue-at=%s\n", state.State, state.Revision, state.OutcomeCode, state.DeadlineAt); err != nil {
						return err
					}
					for _, blocker := range state.Blockers {
						if _, err := fmt.Fprintf(out, "  trace-blocker\toperation=%s\tcode=%s\t%s\n", blocker.Operation, blocker.Code, blocker.Description); err != nil {
							return err
						}
					}
				}
			}
		}
		if impact := instance.DependencyImpact; impact != nil {
			if _, err := fmt.Fprintf(out, "workflow-impact\tdependents=%d\taffected=%d\tshown=%d\thas-more=%t\n", impact.WorkflowCount, impact.ImpactedWorkflowCount, len(impact.Items), impact.HasMore); err != nil {
				return err
			}
			for _, dependent := range impact.Items {
				if _, err := fmt.Fprintf(out, "workflow-dependent\tsubject=%s:%s\tworkflow=%s\tinstance=%s\tstate=%s\trevision=%d\tprerequisite-status=%s\trequired-outcome=%s\taffected=%t\toperation=%s\n", dependent.Subject.Type, dependent.Subject.ID, dependent.State.Workflow, dependent.State.InstanceID, dependent.State.State, dependent.State.Revision, dependent.DependencyStatus, dependent.RequiredOutcomeCode, dependent.NeedsAttention, dependent.State.OperationID); err != nil {
					return err
				}
			}
		}
		for _, related := range instance.RelatedWorkflows {
			state, outcome := "", ""
			if related.State != nil {
				state, outcome = related.State.State, related.State.OutcomeCode
			}
			if _, err := fmt.Fprintf(out, "workflow-dependency\tsubject=%s:%s\tworkflow=%s\tinstance=%s\tstatus=%s\trequired-outcome=%s\tstate=%s\toutcome=%s\n", related.Dependency.SubjectType, related.Dependency.SubjectID, related.Dependency.Workflow, related.Dependency.InstanceID, related.Status, related.Dependency.RequiredOutcomeCode, state, outcome); err != nil {
				return err
			}
		}
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
		if state.OutcomeCode != "" {
			if _, err := fmt.Fprintf(out, "workflow-outcome\t%s\tinstance=%s\trevision=%d\tcode=%s\t%s\n", state.Workflow, state.InstanceID, state.Revision, state.OutcomeCode, state.OutcomeDescription); err != nil {
				return err
			}
		}
		if state.DeadlineAt != "" {
			if _, err := fmt.Fprintf(out, "workflow-deadline\t%s\tinstance=%s\tdue-at=%s\toverdue=%t\toverdue-seconds=%d\n", state.Workflow, state.InstanceID, state.DeadlineAt, state.Overdue, state.OverdueSeconds); err != nil {
				return err
			}
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
		for _, resolution := range transition.BlockerResolutions {
			if _, err := fmt.Fprintf(out, "workflow-blocker-resolution\t%s\tinstance=%s\trevision=%d\treport=%s\treported-by=%s\ttarget-operation=%s\tcode=%s\tblocker-revision=%d\tblocker-report=%s\tblocker-operation=%s\toccurred=%s\tpublished=%s\t%s\n", transition.Workflow, transition.InstanceID, transition.Revision, transition.ID, transition.OperationID, resolution.Operation, resolution.Code, resolution.BlockerRevision, resolution.BlockerReportID, resolution.BlockerOperationID, transition.OccurredAt.UTC().Format(time.RFC3339Nano), transition.PublishedAt.UTC().Format(time.RFC3339Nano), resolution.Description); err != nil {
				return err
			}
		}
		if transition.DependenciesOnly {
			if _, err := fmt.Fprintf(out, "workflow-dependencies-update\t%s\tinstance=%s\trevision=%d\tdependencies=%d\n", transition.Workflow, transition.InstanceID, transition.Revision, len(transition.DependsOn)); err != nil {
				return err
			}
			continue
		}
		if transition.OutcomeOnly {
			if _, err := fmt.Fprintf(out, "workflow-outcome-update\t%s\tinstance=%s\trevision=%d\tstate=%s\tcode=%s\treport=%s\t%s\n", transition.Workflow, transition.InstanceID, transition.Revision, transition.State, transition.OutcomeCode, transition.ID, transition.OutcomeDescription); err != nil {
				return err
			}
			continue
		}
		if transition.DeadlineOnly {
			if _, err := fmt.Fprintf(out, "workflow-deadline-update\t%s\tinstance=%s\trevision=%d\tstate=%s\tdue-at=%s\n", transition.Workflow, transition.InstanceID, transition.Revision, transition.State, transition.DeadlineAt); err != nil {
				return err
			}
			continue
		}
		from := ""
		if transition.BlockersOnly {
			if _, err := fmt.Fprintf(out, "workflow-blocker-update\t%s\tinstance=%s\trevision=%d\tstate=%s\tblockers=%d\n", transition.Workflow, transition.InstanceID, transition.Revision, transition.State, len(transition.Blockers)); err != nil {
				return err
			}
			continue
		}
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
