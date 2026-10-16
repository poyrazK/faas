package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"os"
	"strings"
	"unicode"
)

func cmdIssueLinearConnection(args []string) int {
	fs := newFlagSet("issues tickets linear-connection", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	connect := fs.Bool("connect", false, "verify and store a Linear personal API key for this app")
	disconnect := fs.Bool("disconnect", false, "erase the stored credential without contacting Linear")
	team := fs.String("team", "", "Linear team UUID")
	tokenEnv := fs.String("token-env", "GREGALE_LINEAR_TOKEN", "environment variable containing a Linear personal API key")
	webhookEnv := fs.String("webhook-secret-env", "", "optional environment variable containing the Linear webhook signing secret; omission disables callbacks on replacement")
	flags, pos := normalizeDebugFlagArgs(args, map[string]bool{"app": true, "team": true, "token-env": true, "webhook-secret-env": true})
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	credentialFlags := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "team" || f.Name == "token-env" || f.Name == "webhook-secret-env" {
			credentialFlags = true
		}
	})
	if *app == "" || len(pos) != 0 || *connect && *disconnect || !*connect && credentialFlags {
		return printErr("Invalid Linear connection request", fmt.Errorf("require --app; choose --connect with --team, --disconnect, or inspect without credential flags"))
	}
	in := api.UpdateIssueTrackerLinearConnectionRequest{Connected: *connect}
	if *connect {
		id, err := uuid.Parse(*team)
		if err != nil || id == uuid.Nil {
			return printErr("Invalid Linear team", fmt.Errorf("--team must be a nonzero UUID"))
		}
		in.TeamID = id.String()
		in.APIKey = os.Getenv(*tokenEnv)
		if *webhookEnv != "" {
			in.WebhookSecret = os.Getenv(*webhookEnv)
			if !issuetracker.ValidLinearWebhookSecret(in.WebhookSecret) {
				return printErr("Invalid callback signing secret", fmt.Errorf("selected variable must contain a valid signing secret"))
			}
		}
		if len(in.APIKey) == 0 || len(in.APIKey) > api.IssueTrackerCredentialMaxBytes || strings.IndexFunc(in.APIKey, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return printErr("Invalid Linear credential", fmt.Errorf("selected environment variable must contain a valid personal API key"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetIssueTrackerLinearConnection(context.Background(), *app)
	if err != nil {
		return printErr("Cannot read Linear connection", err)
	}
	if *connect || *disconnect {
		in.ExpectedRevision = out.Revision
		out, err = client.UpdateIssueTrackerLinearConnection(context.Background(), *app, in)
		if err != nil {
			return printErr("Cannot configure Linear connection", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "connected=%t\trevision=%d\tworkspace=%s\tteam=%s\tcallbacks=%t\tcallback_path=%s\n", out.Connected, out.Revision, out.WorkspaceID, out.TeamID, out.WebhookEnabled, out.CallbackPath)
	return 0
}
