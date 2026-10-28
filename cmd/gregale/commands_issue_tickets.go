package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

func cmdIssueTickets(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, "usage: gregale debug issue tickets <create|sync|auto-sync|linear-connection> [flags]")
		if len(args) == 0 {
			return 1
		}
		return 0
	}
	if args[0] == "linear-connection" {
		return cmdIssueLinearConnection(args[1:])
	}
	if args[0] == "auto-sync" {
		return cmdIssueAutoSync(args[1:])
	}
	if len(args) > 0 && args[0] == "sync" {
		return runIssueTicketSync(args[1:], issuetracker.NewClient(nil))
	}
	return runIssueTickets(args, issuetracker.NewClient(nil))
}

func runIssueTickets(args []string, external issuetracker.Provider) int {
	usage := "usage: gregale debug issue tickets create --app SLUG --issue UUID --provider github|linear --target OWNER/REPO|TEAM_UUID --title TEXT --request-id UUID [--description TEXT --token-env NAME --journal-dir PATH --recover-url URL --native]"
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(os.Stderr, usage)
		return 0
	}
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	fs := newFlagSet("issues tickets create", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	issue := fs.String("issue", "", "issue UUID")
	provider := fs.String("provider", "", "github or linear")
	target := fs.String("target", "", "GitHub OWNER/REPO or Linear team UUID")
	title := fs.String("title", "", "explicit ticket title")
	description := fs.String("description", "", "optional ticket description")
	requestID := fs.String("request-id", "", "stable UUID reused on retries")
	tokenEnv := fs.String("token-env", "", "credential environment variable (default GREGALE_GITHUB_TOKEN or GREGALE_LINEAR_TOKEN)")
	journalDir := fs.String("journal-dir", "", "private durable journal directory")
	native := fs.Bool("native", false, "use the app owner stored connection and server recovery receipt")
	recoverLinear := fs.Bool("recover", false, "recover an unconfirmed native Linear creation by its request UUID")
	recoverURL := fs.String("recover-url", "", "verify an existing ticket after an uncertain create")
	flags, pos := normalizeDebugFlagArgs(args[1:], map[string]bool{"app": true, "issue": true, "provider": true, "target": true, "title": true, "description": true, "request-id": true, "token-env": true, "journal-dir": true, "recover-url": true})
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if *app == "" || len(pos) != 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	if *native {
		incompatible := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "token-env" || f.Name == "journal-dir" {
				incompatible = true
			}
		})
		if incompatible || (*provider != "github" && *provider != "linear") {
			return printErr("Invalid native ticket request", fmt.Errorf("--native requires --provider github or linear and cannot be combined with --token-env or --journal-dir"))
		}
	}
	if *recoverLinear && (!*native || *provider != "linear") {
		return printErr("Invalid recovery request", fmt.Errorf("--recover requires --native --provider linear"))
	}
	// Validate operator-controlled inputs before authentication or any network call.
	input, err := issuetracker.ValidateInputs(issuetracker.Request{RequestID: *requestID, APIBase: apiBase(), AppSlug: *app, IssueID: *issue, Provider: *provider, Target: *target, Title: *title, Description: *description})
	if err != nil {
		return printErr("Invalid ticket request", err)
	}
	if *tokenEnv == "" {
		if *provider == "github" {
			*tokenEnv = "GREGALE_GITHUB_TOKEN"
		} else {
			*tokenEnv = "GREGALE_LINEAR_TOKEN"
		}
	}
	if !*native && *journalDir == "" {
		path, e := tokenPath()
		if e != nil {
			return printErr("Cannot locate ticket journal", e)
		}
		*journalDir = filepath.Join(filepath.Dir(path), "issue-ticket-journal")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	metadata, err := client.GetApp(ctx, *app)
	if err != nil {
		return printErr("Cannot read app", err)
	}
	detail, err := client.GetIssue(ctx, *app, input.IssueID, "", "")
	if err != nil {
		return printErr("Cannot read issue", err)
	}
	if detail.Issue.AppID != metadata.ID {
		return printErr("Cannot create ticket", fmt.Errorf("issue does not belong to this app"))
	}
	input.AppID = metadata.ID
	if input.Description == "" {
		input.Description = "Gregale app: " + metadata.Slug + "\nGregale issue: " + input.IssueID
	}
	if *native {
		return runNativeIssueTicketCreate(ctx, client, *app, input, *recoverURL, *recoverLinear)
	}
	result, err := issuetracker.Run(ctx, *journalDir, input, os.Getenv(*tokenEnv), *recoverURL, external, func(ctx context.Context, ticket issuetracker.Ticket) (api.IssueTrackerLink, error) {
		return client.PutIssueTrackerLink(ctx, *app, input.IssueID, api.PutIssueTrackerLinkRequest{URL: ticket.URL, Title: input.Title})
	})
	if jsonOutput {
		if e := writeJSON(result); e != nil {
			return jsonOut(e)
		}
	} else if result.Ticket.URL != "" {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\n", result.State, result.RequestID, result.Ticket.URL)
	}
	if err != nil {
		return printErr("Ticket request incomplete", err)
	}
	return 0
}

func runNativeIssueTicketCreate(ctx context.Context, client *api.Client, slug string, input issuetracker.Request, recoveryURL string, recoverLinear bool) int {
	var out api.NativeIssueTicketResult
	var err error
	if input.Provider == "linear" {
		out, err = client.CreateNativeLinearIssueTicket(ctx, slug, input.IssueID, api.CreateNativeLinearIssueTicketRequest{RequestID: input.RequestID, TeamID: input.Target, Title: input.Title, Description: input.Description, Recover: recoverLinear, RecoveryURL: recoveryURL})
	} else {
		out, err = client.CreateNativeIssueTicket(ctx, slug, input.IssueID, api.CreateNativeIssueTicketRequest{RequestID: input.RequestID, Repository: input.Target, Title: input.Title, Description: input.Description, RecoveryURL: recoveryURL})
	}
	if err != nil {
		return printErr("Native ticket request incomplete", err)
	}
	n, validationErr := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: out.TicketURL})
	id, idErr := uuid.Parse(out.LinkID)
	if out.RequestID != input.RequestID || validationErr != nil || n.Provider != input.Provider || (input.Provider == "github" && !strings.HasPrefix(n.ExternalKey, input.Target+"#")) || (out.State != "created" && out.State != "linked") || (out.State == "linked" && (idErr != nil || id == uuid.Nil)) {
		return printErr("Native ticket response invalid", fmt.Errorf("preserve the request ID and retry identical inputs"))
	}
	if jsonOutput {
		if err := writeJSON(out); err != nil {
			return jsonOut(err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\n", out.State, out.RequestID, out.TicketURL)
	}
	if out.State != "linked" {
		return printErr("Ticket exists but attachment is incomplete", fmt.Errorf("retry the same request ID and inputs"))
	}
	return 0
}
