package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

type issueTicketSyncResult struct {
	Direction        string `json:"direction"`
	IssueID          string `json:"issue_id"`
	LinkID           string `json:"link_id"`
	URL              string `json:"url"`
	GregaleState     string `json:"gregale_state"`
	ProviderState    string `json:"provider_state"`
	DesiredState     string `json:"desired_state"`
	WouldChange      bool   `json:"would_change"`
	Applied          bool   `json:"applied"`
	NeedsDeployment  bool   `json:"needs_deployment"`
	NeedsLinearState bool   `json:"needs_linear_state"`
}

func linkedIssueTicket(ctx context.Context, client *Client, slug, issue, link string) (api.IssueTrackerLink, error) {
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := client.ListIssueTrackerLinks(ctx, slug, issue, cursor)
		if err != nil {
			return api.IssueTrackerLink{}, err
		}
		for _, item := range page.Items {
			if item.ID == link {
				return item, nil
			}
		}
		if page.NextCursor == "" {
			return api.IssueTrackerLink{}, fmt.Errorf("tracker reference does not belong to this issue group")
		}
		if seen[page.NextCursor] || len(page.Items) == 0 {
			return api.IssueTrackerLink{}, fmt.Errorf("invalid tracker reference pagination")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
}
func runIssueTicketSync(args []string, provider issuetracker.StatusProvider) int {
	usage := "usage: gregale debug issue tickets sync --app SLUG --issue UUID --link UUID --direction push|pull [--native --apply --deployment UUID --linear-state UUID --token-env NAME]"
	fs := newFlagSet("issues tickets sync", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	issue := fs.String("issue", "", "issue UUID")
	linkID := fs.String("link", "", "stored tracker reference UUID")
	direction := fs.String("direction", "", "push Gregale status or pull provider status")
	native := fs.Bool("native", false, "use the app owner's stored provider connection")
	apply := fs.Bool("apply", false, "apply the previewed lifecycle change")
	deployment := fs.String("deployment", "", "fixing deployment UUID for a pulled resolution")
	linearState := fs.String("linear-state", "", "team workflow state UUID for a Linear push")
	tokenEnv := fs.String("token-env", "", "provider credential environment variable")
	flags, pos := normalizeDebugFlagArgs(args, map[string]bool{"app": true, "issue": true, "link": true, "direction": true, "deployment": true, "linear-state": true, "token-env": true})
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	issueUUID, e1 := uuid.Parse(*issue)
	linkUUID, e2 := uuid.Parse(*linkID)
	if len(pos) != 0 || *app == "" || e1 != nil || e2 != nil || issueUUID == uuid.Nil || linkUUID == uuid.Nil || (*direction != "push" && *direction != "pull") {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	for _, value := range []string{*deployment, *linearState} {
		if value != "" {
			id, e := uuid.Parse(value)
			if e != nil || id == uuid.Nil {
				return printErr("Invalid sync request", fmt.Errorf("deployment and workflow state must be nonzero UUIDs"))
			}
		}
	}
	invalid := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "deployment" && *direction != "pull" || f.Name == "linear-state" && *direction != "push" {
			invalid = true
		}
	})
	if *native {
		mixed := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "token-env" {
				mixed = true
			}
		})
		if mixed {
			return printErr("Invalid sync request", fmt.Errorf("native mode cannot use --token-env"))
		}
	}
	if invalid {
		return printErr("Invalid sync request", fmt.Errorf("deployment applies to pull; Linear workflow state applies to push"))
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
	detail, err := client.GetIssue(ctx, *app, issueUUID.String(), "", "")
	if err != nil {
		return printErr("Cannot read issue", err)
	}
	if detail.Issue.AppID != metadata.ID {
		return printErr("Cannot sync ticket", fmt.Errorf("issue does not belong to this app"))
	}
	if detail.Issue.State != "open" && detail.Issue.State != "resolved" {
		return printErr("Cannot sync ticket", fmt.Errorf("ignored or unsupported Gregale states require manual triage"))
	}
	link, err := linkedIssueTicket(ctx, client, *app, detail.Issue.ID, linkUUID.String())
	if err != nil {
		return printErr("Cannot read tracker reference", err)
	}
	normalized, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: link.URL})
	if err != nil || normalized.Provider != link.Provider {
		return printErr("Cannot sync ticket", fmt.Errorf("tracker reference has inconsistent provider metadata"))
	}
	if link.Provider != "linear" && *linearState != "" {
		return printErr("Invalid sync request", fmt.Errorf("linear workflow state does not apply to GitHub"))
	}
	if *native {
		if link.Provider == "linear" {
			provider = &nativeLinearStatusProvider{client: client, app: *app, issue: detail.Issue.ID, link: link.ID}
		} else {
			provider = nativeIssueStatusProvider{client: client, app: *app, issue: detail.Issue.ID, link: link.ID}
		}
	}
	if *tokenEnv == "" {
		if link.Provider == "github" {
			*tokenEnv = "GREGALE_GITHUB_TOKEN"
		} else {
			*tokenEnv = "GREGALE_LINEAR_TOKEN"
		}
	}
	token := ""
	if !*native {
		token = os.Getenv(*tokenEnv)
	}
	remote, err := provider.ReadStatus(ctx, link.URL, token)
	if err != nil {
		return printErr("Cannot read provider status", err)
	}
	if remote.URL != normalized.URL || remote.Provider != link.Provider || remote.State != "open" && remote.State != "resolved" {
		return printErr("Cannot sync ticket", fmt.Errorf("provider returned inconsistent status evidence"))
	}
	result := issueTicketSyncResult{Direction: *direction, IssueID: detail.Issue.ID, LinkID: link.ID, URL: remote.URL, GregaleState: detail.Issue.State, ProviderState: remote.State}
	result.DesiredState = detail.Issue.State
	if *direction == "pull" {
		result.DesiredState = remote.State
	}
	result.WouldChange = detail.Issue.State != remote.State
	result.NeedsDeployment = result.WouldChange && *direction == "pull" && remote.State == "resolved" && *deployment == ""
	result.NeedsLinearState = result.WouldChange && *direction == "push" && remote.Provider == "linear" && *linearState == ""
	if *apply && result.WouldChange {
		if result.NeedsDeployment || result.NeedsLinearState {
			return printErr("Cannot apply sync", fmt.Errorf("supply the fixing deployment or selected Linear workflow state shown by the preview"))
		}
		// Recheck current group identity, lifecycle and membership before a write.
		current, e := client.GetIssue(ctx, *app, issueUUID.String(), "", "")
		if e != nil {
			return printErr("Cannot recheck issue", e)
		}
		latest, e := linkedIssueTicket(ctx, client, *app, current.Issue.ID, link.ID)
		if e != nil {
			return printErr("Cannot recheck reference", e)
		}
		if current.Issue.ID != detail.Issue.ID || current.Issue.AppID != metadata.ID || current.Issue.State != detail.Issue.State || latest.URL != link.URL || latest.Provider != link.Provider {
			return printErr("Sync snapshot changed", fmt.Errorf("issue group, lifecycle or tracker reference changed; preview again"))
		}
		if *direction == "push" {
			after, e := provider.SetStatus(ctx, remote, token, result.DesiredState, *linearState)
			if e != nil {
				return printErr("Provider sync was not confirmed", e)
			}
			if after.URL != remote.URL || after.ID != remote.ID || after.Provider != remote.Provider || after.TeamID != remote.TeamID || after.State != result.DesiredState {
				return printErr("Provider sync was not confirmed", fmt.Errorf("provider returned inconsistent update evidence"))
			}
		} else {
			// Re-read the remote authority immediately before the Gregale action.
			fresh, e := provider.ReadStatus(ctx, link.URL, token)
			if e != nil {
				return printErr("Cannot recheck provider", e)
			}
			if fresh != remote {
				return printErr("Sync snapshot changed", issuetracker.ErrStatusChanged)
			}
			action := api.IssueActionRequest{Action: "reopen"}
			if remote.State == "resolved" {
				action.Action = "resolve"
				action.FixedDeploymentID = *deployment
			}
			after, e := client.ActOnIssue(ctx, *app, detail.Issue.ID, action)
			if e != nil {
				return printErr("Gregale sync was not confirmed", e)
			}
			if after.ID != detail.Issue.ID || after.State != result.DesiredState {
				return printErr("Gregale sync was not confirmed", fmt.Errorf("gregale returned inconsistent lifecycle evidence"))
			}
		}
		result.Applied = true
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "%s: Gregale %s, %s %s; desired %s; change=%t applied=%t\n", result.Direction, result.GregaleState, remote.Provider, result.ProviderState, result.DesiredState, result.WouldChange, result.Applied)
	if result.NeedsDeployment {
		_, _ = fmt.Fprintln(osStdout, "Resolution requires --deployment UUID.")
	}
	if result.NeedsLinearState {
		_, _ = fmt.Fprintln(osStdout, "Linear push requires --linear-state UUID.")
	}
	return 0
}
