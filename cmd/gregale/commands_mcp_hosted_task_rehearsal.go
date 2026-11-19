package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func cmdMCPHostedTaskRehearsal(args []string) int {
	const label = "hosted Task rehearsal"
	if len(args) == 0 {
		return printErr(label, errors.New("use rehearsal plan|get|approve|revoke"))
	}
	action := args[0]
	flags := newFlagSet("mcp-hosted-task-rehearsal", flag.ContinueOnError)
	id := flags.String("id", "", "retained hosted source intent")
	plan := flags.String("plan-sha256", "", "exact rehearsal plan digest for a decision")
	in := api.HostedTaskRehearsalPrepare{}
	flags.StringVar(&in.IntentSHA256, "intent-sha256", "", "exact retained intent digest")
	flags.StringVar(&in.ReviewSHA256, "review-sha256", "", "exact approved review digest")
	flags.StringVar(&in.BindingSHA256, "binding-sha256", "", "exact passing binding digest")
	if err := flags.Parse(args[1:]); err != nil {
		return 1
	}
	if flags.NArg() != 0 || !toolpublication.ValidID(*id) || !validHostedTaskRehearsalFlags(action, *plan, in) {
		return printErr(label, errors.New("require --id and all three observation digests for plan, or --plan-sha256 for a decision"))
	}
	out, err := requestMCPHostedTaskRehearsal(action, *id, *plan, in)
	if err != nil {
		return printErr(label, err)
	}
	code := jsonOut(writeJSON(out))
	if code == 0 && (action == "plan" && out.Status != tasktoolrehearsal.Planned && out.Status != tasktoolrehearsal.Approved && out.Status != tasktoolrehearsal.Claimed ||
		action == "approve" && out.Status != tasktoolrehearsal.Approved && out.Status != tasktoolrehearsal.Claimed ||
		action == "revoke" && out.Status != tasktoolrehearsal.Revoked) {
		return 1
	}
	return code
}

func validHostedTaskRehearsalFlags(action, plan string, in api.HostedTaskRehearsalPrepare) bool {
	switch action {
	case "plan":
		return plan == "" && tasktoolrehearsal.ValidPrepare(in)
	case "get":
		return plan == "" && in == (api.HostedTaskRehearsalPrepare{})
	case "approve", "revoke":
		return toolpublication.ValidID(plan) && in == (api.HostedTaskRehearsalPrepare{})
	default:
		return false
	}
}

func requestMCPHostedTaskRehearsal(action, id, plan string, in api.HostedTaskRehearsalPrepare) (api.HostedTaskRehearsalPlan, error) {
	var out api.HostedTaskRehearsalPlan
	c, err := authedClient()
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	account, err := c.Whoami(ctx)
	if err != nil {
		return out, err
	}
	switch action {
	case "plan":
		out, err = c.PrepareHostedTaskRehearsal(ctx, id, in)
	case "get":
		out, err = c.GetHostedTaskRehearsal(ctx, id)
	default:
		out, err = c.DecideHostedTaskRehearsal(ctx, id, api.HostedTaskRehearsalDecision{PlanSHA256: plan, Decision: action})
	}
	if err != nil {
		return out, err
	}
	if tasktoolrehearsal.Validate(out, account.ID, id) != nil || plan != "" && plan != out.PlanSHA256 ||
		action == "plan" && out.HostedTaskRehearsalPrepare != in {
		return api.HostedTaskRehearsalPlan{}, errors.New("native Task rehearsal receipt is invalid")
	}
	return out, nil
}
