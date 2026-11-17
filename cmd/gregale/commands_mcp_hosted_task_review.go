package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func cmdMCPHostedTaskReview(args []string) int {
	if len(args) == 0 {
		return printErr("hosted Task source review", errors.New("use source-review get|check|approve|reject|revoke"))
	}
	action := args[0]
	flags := newFlagSet("mcp-hosted-task-review", flag.ContinueOnError)
	id := flags.String("id", "", "retained hosted source review identity")
	digest := flags.String("intent-sha256", "", "exact owner-reviewed intent digest")
	review := flags.String("review-sha256", "", "exact retained source-review fingerprint")
	if err := flags.Parse(args[1:]); err != nil {
		return 1
	}
	decision := action == "approve" || action == "reject" || action == "revoke"
	if flags.NArg() != 0 || !toolpublication.ValidID(*id) || !decision && action != "get" && action != "check" ||
		action != "get" && !toolpublication.ValidID(*review) || decision && !toolpublication.ValidID(*digest) {
		return printErr("hosted Task source review", errors.New("require --id, --review-sha256 for checks and decisions, and --intent-sha256 for owner decisions"))
	}
	out, err := requestMCPHostedTaskReview(action, *id, *digest, *review)
	if err != nil {
		return printErr("hosted Task source review", err)
	}
	code := jsonOut(writeJSON(out))
	if target := map[string]string{"approve": "approved", "reject": "rejected", "revoke": "revoked"}[action]; code == 0 && target != "" && out.ReviewStatus != target {
		return 1
	}
	return code
}

func requestMCPHostedTaskReview(action, id, digest, fingerprint string) (api.HostedTaskSourceIntent, error) {
	var out api.HostedTaskSourceIntent
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
	if action == "check" {
		out, err = c.CheckHostedTaskSourceIntent(ctx, id, api.HostedTaskSourceCheck{ReviewSHA256: fingerprint})
	} else {
		out, err = c.GetHostedTaskSourceIntent(ctx, id)
	}
	if err != nil {
		return out, err
	}
	if tasktoolsource.ValidateRecord(out, account.ID, id) != nil {
		return out, errors.New("native hosted Task source receipt is invalid")
	}
	if action != "get" && action != "check" {
		if out.IntentSHA256 != digest || out.ReviewSHA256 != fingerprint {
			return out, errors.New("owner digests differ from the exact retained source review")
		}
		out, err = c.DecideHostedTaskSourceIntent(ctx, id, api.HostedTaskSourceDecision{IntentSHA256: digest, ReviewSHA256: fingerprint, Decision: action})
		if err != nil {
			return out, err
		}
		if tasktoolsource.ValidateRecord(out, account.ID, id) != nil || out.IntentSHA256 != digest || out.ReviewSHA256 != fingerprint {
			return out, errors.New("native hosted Task source decision receipt is invalid")
		}
	}
	return tasktoolsource.View(out, out.SourceState == "retained", time.Now()), nil
}
