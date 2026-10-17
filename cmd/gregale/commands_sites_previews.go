package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdSitesPreviews(args []string) int {
	if strings.ToLower(os.Getenv("FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED")) != "true" && os.Getenv("FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED") != "1" {
		return printErr("Private previews are disabled", errors.New("set FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED=1; API admission and Worker serving also require separate opt-ins"))
	}
	if len(args) == 0 {
		return printErr("Preview action required", errors.New("use sites previews <create|list|revoke> --site UUID"))
	}
	verb := args[0]
	flags := newFlagSet("sites-previews-"+verb, flag.ContinueOnError)
	site := flags.String("site", "", "site UUID (required)")
	release := flags.String("release", "", "verified release UUID (create)")
	id := flags.String("preview", "", "preview UUID (revoke)")
	generation := flags.Int64("expected-generation", 0, "reviewed generation (revoke)")
	ttl := flags.Duration("ttl", api.HostingPreviewTTLDefault, "immutable expiry duration (create)")
	if err := flags.Parse(args[1:]); err != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if uuid.Validate(*site) != nil || verb != "create" && verb != "list" && verb != "revoke" {
		return printErr("Invalid preview request", errors.New("site UUID and create, list, or revoke are required"))
	}
	if verb == "create" && (uuid.Validate(*release) != nil || *ttl < api.HostingPreviewTTLMin || *ttl > api.HostingPreviewTTLMax || *ttl%time.Second != 0) || verb == "revoke" && (uuid.Validate(*id) != nil || *generation != 1) {
		return printErr("Invalid preview options", errors.New("create requires a verified release and a TTL from 5m to 72h; revoke requires preview and expected-generation=1"))
	}
	if verb != "create" && (*release != "" || *ttl != api.HostingPreviewTTLDefault) || verb != "revoke" && (*id != "" || *generation != 0) {
		return printErr("Invalid preview options", errors.New("release/ttl are create-only; preview/expected-generation are revoke-only"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var result any
	switch verb {
	case "create":
		result, err = client.CreateHostingPreview(ctx, *site, api.CreateHostingPreviewRequest{ReleaseID: *release, TTLSeconds: int64(*ttl / time.Second)})
	case "list":
		result, err = client.ListHostingPreviews(ctx, *site)
	case "revoke":
		result, err = client.RevokeHostingPreview(ctx, *site, *id, *generation)
	}
	if err != nil {
		return printErr("Preview request not confirmed; list previews before retrying", err)
	}
	return jsonOut(writeJSON(result))
}
