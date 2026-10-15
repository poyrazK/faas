package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsTriage(args []string) int {
	fs := flag.NewFlagSet(subObsTriage, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	incident := fs.String("incident", "", "immutable platform_alert/episode-id")
	action := fs.String("action", "", "acknowledge|unacknowledge|claim|release|snooze|unsnooze")
	revision := fs.Int64("expected-revision", -1, "revision returned in incident evidence (required)")
	until := fs.String("until", "", "RFC 3339 snooze deadline, at most 24h ahead")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	timeout := fs.Duration("timeout", 10*time.Second, "total request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	kind, id, _ := strings.Cut(*incident, "/")
	if fs.NArg() != 0 || kind != "platform_alert" || operatorobs.ValidateReference(kind, id) != nil || !api.AllowedObsAlertTriageAction(*action) || *revision < 0 || *revision >= api.ObsAlertTriageRevisionMax || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs triage: valid --incident, --action, --expected-revision and positive --timeout required")
		return 2
	}
	request := api.ObsAlertTriageRequest{Action: *action, ExpectedRevision: revision}
	if *action == "snooze" {
		at, err := time.Parse(time.RFC3339, *until)
		now := time.Now()
		if err != nil || !at.After(now) || at.After(now.Add(api.ObsAlertTriageSnoozeMax)) {
			_, _ = fmt.Fprintln(osStderr, "gregalectl obs triage: snooze requires --until within the next 24 hours")
			return 2
		}
		request.SnoozedUntil = &at
	} else if *until != "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs triage: --until is only valid for snooze")
		return 2
	}
	session, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs triage:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var response api.ObsAlertTriageResponse
	if err := newOperatorHTTPClient(&session).doJSON(ctx, http.MethodPost, "/v1/admin/obs/incidents/platform_alert/"+id+"/triage", request, &response, true, nil); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs triage:", err)
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "incident_id=%s ", response.IncidentID)
	writeObsTriageHuman(response.Triage)
	_, _ = fmt.Fprintln(osStdout)
	return 0
}

func writeObsTriageHuman(t api.ObsAlertTriage) {
	_, _ = fmt.Fprintf(osStdout, "revision=%d acknowledged=%t owned=%t owner_account_id=%s snoozed=%t", t.Revision, t.Acknowledged, t.Owned, t.OwnerAccountID, t.Snoozed)
	if t.SnoozedUntil != nil {
		_, _ = fmt.Fprintf(osStdout, " snoozed_until=%s", t.SnoozedUntil.UTC().Format(time.RFC3339))
	}
}
