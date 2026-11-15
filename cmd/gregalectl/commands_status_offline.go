package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/statusmirror"
)

// statusOfflineStore opens the off-platform status mirror bucket
// (ADR-20261115090000). Tests replace it.
var statusOfflineStore = func() (statusmirror.Store, error) {
	store, err := statusmirror.Load(os.Getenv)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("set %s, %s, %s and %s to the status mirror bucket and an operator token", statusmirror.EnvEndpoint, statusmirror.EnvBucket, statusmirror.EnvAccessKey, statusmirror.EnvSecretKey)
	}
	return store, nil
}

var statusOfflineNow = time.Now

const statusOfflineUsage = `usage: gregalectl status offline <post|update|resolve|list> [flags]

Posts incidents straight to the off-platform status page bucket, without
apid. Use it when Gregale's API is down; once it recovers, post the incident
through 'gregalectl status incident create' as usual and resolve the offline
notice.

  post     --title --impact degraded|partial_outage|major_outage --components a,b --message [--state investigating]
  update   --id --message [--state identified|monitoring]
  resolve  --id --message
  list
`

func cmdStatusOffline(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(osStderr, statusOfflineUsage)
		return 2
	}
	switch args[0] {
	case "post":
		return cmdStatusOfflinePost(args[1:])
	case "update":
		return cmdStatusOfflineUpdate(args[1:], "")
	case "resolve":
		return cmdStatusOfflineUpdate(args[1:], "resolved")
	case "list":
		return cmdStatusOfflineList(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl status offline: unknown command %q\n%s", args[0], statusOfflineUsage)
		return 2
	}
}

func cmdStatusOfflinePost(args []string) int {
	fs := flag.NewFlagSet("status offline post", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	title := fs.String("title", "", "public incident title")
	impact := fs.String("impact", "", "degraded|partial_outage|major_outage")
	components := fs.String("components", "", "comma-separated public capabilities")
	stateValue := fs.String("state", "investigating", "investigating|identified|monitoring")
	message := fs.String("message", "", "public update message")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	store, err := statusOfflineStore()
	if err != nil {
		return statusCLIError("offline post", err.Error())
	}
	now := statusOfflineNow()
	event, err := statusmirror.PostNotice(context.Background(), store, statusmirror.NoticeInput{
		Title: *title, Impact: *impact, Components: splitStatusComponents(*components),
		State: *stateValue, Message: *message, StartsAt: now,
	}, now)
	if err != nil {
		return statusCLIError("offline post", err.Error())
	}
	return printStatusOfflineMutation(event)
}

func cmdStatusOfflineUpdate(args []string, forcedState string) int {
	action := "offline update"
	if forcedState != "" {
		action = "offline resolve"
	}
	fs := flag.NewFlagSet("status "+action, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	id := fs.String("id", "", "offline notice id")
	stateValue := fs.String("state", forcedState, "new lifecycle state (default: unchanged)")
	message := fs.String("message", "", "public update message")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *message == "" {
		return statusCLIError(action, "--id and --message are required")
	}
	store, err := statusOfflineStore()
	if err != nil {
		return statusCLIError(action, err.Error())
	}
	event, err := statusmirror.UpdateNotice(context.Background(), store, statusmirror.NoticeUpdate{ID: *id, State: *stateValue, Message: *message}, statusOfflineNow())
	if err != nil {
		return statusCLIError(action, err.Error())
	}
	return printStatusOfflineMutation(event)
}

func cmdStatusOfflineList(args []string) int {
	fs := flag.NewFlagSet("status offline list", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	store, err := statusOfflineStore()
	if err != nil {
		return statusCLIError("offline list", err.Error())
	}
	doc, _, err := statusmirror.ReadNotices(context.Background(), store)
	if err != nil {
		return statusCLIError("offline list", err.Error())
	}
	if jsonEnabled() {
		return emitOperatorJSON(doc.Events)
	}
	if len(doc.Events) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No offline notices.")
		return 0
	}
	for _, event := range doc.Events {
		_, _ = fmt.Fprintf(osStdout, "%s  %-14s  %-14s  %s\n", event.ID, event.State, event.Impact, event.Title)
	}
	return 0
}

func printStatusOfflineMutation(event api.PublicStatusEvent) int {
	if jsonEnabled() {
		return emitOperatorJSON(event)
	}
	_, _ = fmt.Fprintf(osStdout, "offline incident %s: %s\nid: %s\nVisible on the off-platform status page within about a minute.\n", event.State, event.Title, event.ID)
	return 0
}
