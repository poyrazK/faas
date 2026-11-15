package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func cmdMCPTaskIntent(args []string) int {
	if len(args) == 0 {
		return printErr("Task release intent", errors.New("use intent prepare|get|approve|reject|revoke"))
	}
	action := args[0]
	flags := newFlagSet("mcp-task-intent", flag.ContinueOnError)
	plan := flags.String("plan", "", "native Task release plan to re-review")
	id := flags.String("id", "", "stable 64-character intent ID; generated on prepare if omitted")
	digest := flags.String("intent-sha256", "", "exact owner-reviewed intent digest")
	reason := flags.String("reason", "", "reviewable purpose for prepare")
	if err := flags.Parse(args[1:]); err != nil {
		return 1
	}
	if flags.NArg() != 0 || *plan == "" || (action != "prepare" && action != "get" && action != "approve" && action != "reject" && action != "revoke") ||
		(action != "prepare" && !toolpublication.ValidID(*id)) || (*id != "" && !toolpublication.ValidID(*id)) ||
		(action != "get" && action != "prepare" && !toolpublication.ValidID(*digest)) || (action == "prepare" && *reason == "") {
		return printErr("Task release intent", errors.New("require --plan, --id for existing intents, --reason for prepare and --intent-sha256 for owner decisions"))
	}
	review, err := reviewMCPTaskRelease(*plan)
	if err != nil {
		return printErr("Task release intent source", err)
	}
	out, err := requestMCPTaskIntent(action, *id, *digest, *reason, review)
	if err != nil {
		return printErr("Task release intent", err)
	}
	code := jsonOut(writeJSON(out))
	if target := map[string]string{"approve": "approved", "reject": "rejected", "revoke": "revoked"}[action]; code == 0 && target != "" && out.Status != target {
		return 1
	}
	return code
}

func requestMCPTaskIntent(action, id, digest, reason string, review mcphosting.TaskReleaseReview) (api.TaskToolReleaseIntentRecord, error) {
	var out api.TaskToolReleaseIntentRecord
	c, err := authedClient()
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if action == "prepare" {
		if id == "" {
			var token [32]byte
			if _, err := rand.Read(token[:]); err != nil {
				return out, err
			}
			id = hex.EncodeToString(token[:])
		}
		intent, err := mcpTaskIntent(ctx, c, review, reason)
		if err != nil {
			return out, err
		}
		return c.PrepareTaskToolReleaseIntent(ctx, id, intent)
	}
	if action == "get" {
		return c.CheckTaskToolReleaseIntent(ctx, id, api.TaskToolReleaseIntentCheck{ReleaseFingerprint: review.Fingerprint})
	}
	return c.DecideTaskToolReleaseIntent(ctx, id, api.TaskToolReleaseIntentDecision{
		IntentSHA256: digest, ReleaseFingerprint: review.Fingerprint, Decision: action})
}

func mcpTaskIntent(ctx context.Context, c *Client, review mcphosting.TaskReleaseReview, reason string) (api.TaskToolReleaseIntent, error) {
	intent := api.TaskToolReleaseIntent{ReleaseFingerprint: review.Fingerprint, WorkerRuntime: review.WorkerRuntime, Protocol: review.Protocol,
		ObserverMetricApp: review.ObserverMetricApp, Endpoint: review.Endpoint, AccessMode: review.AccessMode, ToolScopes: review.ToolScopes,
		RequiredSetup: tasktoolrelease.RequiredSetup(), Reason: reason, Apps: []api.TaskToolReleaseApp{}}
	roles := []struct{ role, app string }{{"web", review.WebApp}, {"worker", review.WorkerApp}, {"observer", review.ObserverApp}}
	for _, previous := range review.PreviousWorkerApps {
		roles = append(roles, struct{ role, app string }{"previous_worker", previous})
	}
	for _, role := range roles {
		pin, err := c.GetAppReleaseReview(ctx, role.app)
		if err != nil {
			return intent, err
		}
		intent.Apps = append(intent.Apps, api.TaskToolReleaseApp{Role: role.role, App: role.app, Review: pin})
	}
	return intent, tasktoolrelease.Validate(intent)
}
