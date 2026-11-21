package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func cmdMCPHostedTaskBindings(args []string) int {
	if len(args) == 0 {
		return printErr("hosted Task bindings", errors.New("use bindings qualify|get|revoke"))
	}
	action := args[0]
	flags := newFlagSet("mcp-hosted-task-bindings", flag.ContinueOnError)
	id := flags.String("id", "", "retained hosted source intent")
	in := api.HostedTaskBindingSelection{}
	flags.StringVar(&in.IntentSHA256, "intent-sha256", "", "exact approved intent digest")
	flags.StringVar(&in.ReviewSHA256, "review-sha256", "", "exact approved source review digest")
	flags.StringVar(&in.Namespace, "namespace", "", "native Task queue namespace")
	flags.StringVar(&in.WebDatabaseRef, "web-database-ref", "", "web native managed PostgreSQL secret reference")
	flags.StringVar(&in.WorkerDatabaseRef, "worker-database-ref", "", "worker native managed PostgreSQL secret reference")
	flags.StringVar(&in.MigrationDatabaseRef, "migration-database-ref", "", "worker migration-only native secret reference")
	flags.StringVar(&in.ObserverDatabaseRef, "observer-database-ref", "", "observer read-only native secret reference")
	digest := flags.String("binding-sha256", "", "exact binding fingerprint to revoke")
	if err := flags.Parse(args[1:]); err != nil {
		return 1
	}
	if flags.NArg() != 0 || !toolpublication.ValidID(*id) || action != "get" && action != "qualify" && action != "revoke" || action == "qualify" && tasktoolbinding.ValidateSelection(in) != nil || action == "revoke" && !toolpublication.ValidID(*digest) {
		return printErr("hosted Task bindings", errors.New("require a valid action, --id and exact qualification references or revocation fingerprint"))
	}
	out, err := requestMCPHostedTaskBindings(action, *id, *digest, in)
	if err != nil {
		return printErr("hosted Task bindings", err)
	}
	code := jsonOut(writeJSON(out))
	if code == 0 && (action == "qualify" && out.Status != "passed" || action == "revoke" && out.Status != "revoked") {
		return 1
	}
	return code
}
func requestMCPHostedTaskBindings(action, id, digest string, in api.HostedTaskBindingSelection) (api.HostedTaskBindingVerification, error) {
	var out api.HostedTaskBindingVerification
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
	case "qualify":
		out, err = c.SelectHostedTaskBindings(ctx, id, in)
		if err == nil && (tasktoolbinding.ValidateReceipt(out, account.ID, id) != nil || out.IntentSHA256 != in.IntentSHA256 || out.ReviewSHA256 != in.ReviewSHA256) {
			return out, errors.New("native Task binding selection receipt is invalid")
		}
		if err == nil {
			digest = out.BindingSHA256
			out, err = c.CheckHostedTaskBindings(ctx, id, api.HostedTaskBindingCheck{BindingSHA256: digest})
		}
	case "revoke":
		out, err = c.RevokeHostedTaskBindings(ctx, id, api.HostedTaskBindingCheck{BindingSHA256: digest})
	default:
		out, err = c.GetHostedTaskBindings(ctx, id)
	}
	if err != nil {
		return out, err
	}
	if tasktoolbinding.ValidateReceipt(out, account.ID, id) != nil || digest != "" && out.BindingSHA256 != digest || action == "qualify" && (out.IntentSHA256 != in.IntentSHA256 || out.ReviewSHA256 != in.ReviewSHA256) {
		return out, errors.New("native Task binding receipt is invalid")
	}
	return tasktoolbinding.View(out, time.Now()), nil
}
