// commands_obs_wake.go — `gregalectl obs wake <wake_id>`.
//
// One timeline for one wake: the platform events apid recorded for it
// (GET /v1/admin/obs/wakes/{wake_id}: admitted, boot started, restore
// breakdown, completed or failed) merged with every log line that mentions
// the wake_id on any host, from Loki (the same client as `obs logs`). Until
// now an operator ran `obs evidence` and `obs logs` and lined them up by
// eye. Logs are optional: without a configured Loki the events still print.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

const subObsWake = "wake"

// obsWakeRow is one line of the merged timeline.
type obsWakeRow struct {
	TS     time.Time `json:"ts"`
	Source string    `json:"source"` // "event" or "log"
	Text   string    `json:"text"`
}

func cmdObsWake(args []string) int {
	fs := flag.NewFlagSet(subObsWake, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout for apid and Loki")
	noLogs := fs.Bool("no-logs", false, "events only; do not query Loki")
	margin := fs.Duration("log-margin", 2*time.Minute, "how far around the first and last event to search the logs")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(reorderObsArgs(args, "admin-token", "timeout", "log-margin")); err != nil {
		return 2
	}
	if fs.NArg() != 1 || !operatorobs.ValidWakeID(fs.Arg(0)) || *timeout <= 0 || *margin < 0 || *margin > time.Hour {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl obs wake <wake_id> [--no-logs] [--log-margin 2m] [--json]; wake_id is 8-64 letters, digits or dashes")
		return 2
	}
	wakeID := fs.Arg(0)
	cred, ok := resolveObsCredential("obs wake", *token)
	if !ok {
		return 2
	}
	var wake api.ObsWakeTimeline
	err := fetchObsBundleSection(context.Background(), &http.Client{Timeout: *timeout}, apidBase()+"/v1/admin/obs/wakes/"+url.PathEscape(wakeID), cred, "wake", &wake)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			_, _ = fmt.Fprintf(osStderr, "gregalectl obs wake: no events are recorded for %s\n", wakeID)
			return 1
		}
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	logs, logQuery, logNote := obsWakeLogs(wake, *noLogs, *margin, *timeout)
	rows := mergeObsWakeTimeline(wake, logs)
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(struct {
			Wake     api.ObsWakeTimeline `json:"wake"`
			LogQuery string              `json:"log_query,omitempty"`
			LogNote  string              `json:"log_note,omitempty"`
			Logs     []obsLogEntry       `json:"logs"`
			Timeline []obsWakeRow        `json:"timeline"`
		}{wake, logQuery, logNote, logs, rows})
	}
	writeObsWake(osStdout, wake, rows, len(logs), logNote)
	return 0
}

// reorderObsArgs moves flags in front of positional arguments, so an id may
// come first (`obs wake <id> --no-logs`, `obs app <slug> --range 24h`); the
// flag package otherwise stops parsing at the first positional argument.
// valueFlags names the flags that take a separate value.
func reorderObsArgs(args []string, valueFlags ...string) []string {
	takesValue := map[string]bool{}
	for _, name := range valueFlags {
		takesValue["-"+name] = true
		takesValue["--"+name] = true
	}
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "-"):
			flags = append(flags, a)
			if takesValue[a] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		default:
			positional = append(positional, a)
		}
	}
	return append(flags, positional...)
}

// obsWakeLogs queries Loki for lines mentioning the wake within the events'
// time span plus margin. A missing Loki configuration or a failed query is a
// note, never a failure: the events alone are still the answer.
func obsWakeLogs(wake api.ObsWakeTimeline, skip bool, margin, timeout time.Duration) ([]obsLogEntry, string, string) {
	if skip || len(wake.Events) == 0 {
		return []obsLogEntry{}, "", ""
	}
	cfg := &lokiClientConfig{}
	if err := cfg.resolve(); err != nil {
		return []obsLogEntry{}, "", "logs not searched: " + err.Error()
	}
	opts := &obsLogsOptions{
		wakeID:  wake.WakeID,
		start:   wake.Events[0].At.Add(-margin),
		end:     wake.Events[len(wake.Events)-1].At.Add(margin),
		limit:   obsLogsMaxLimit,
		forward: true,
	}
	query := opts.logQL()
	entries, err := queryLoki(context.Background(), cfg, timeout, query, opts)
	if err != nil {
		return []obsLogEntry{}, query, "logs not searched: " + err.Error()
	}
	return entries, query, ""
}

func mergeObsWakeTimeline(wake api.ObsWakeTimeline, logs []obsLogEntry) []obsWakeRow {
	rows := make([]obsWakeRow, 0, len(wake.Events)+len(logs))
	for _, e := range wake.Events {
		rows = append(rows, obsWakeRow{TS: e.At.UTC(), Source: "event", Text: formatObsWakeEvent(e)})
	}
	for _, l := range logs {
		unit := strings.TrimSuffix(strings.TrimPrefix(l.Labels["unit"], "faas-"), ".service")
		rows = append(rows, obsWakeRow{TS: l.TS.UTC(), Source: "log", Text: orDash(l.Labels["host"]) + " " + orDash(unit) + " " + formatObsLogLine(l.Line)})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TS.Before(rows[j].TS) })
	return rows
}

// formatObsWakeEvent renders kind plus the payload's top-level fields,
// sorted, with the wake_id itself left out (it is in the header).
func formatObsWakeEvent(e api.ObsWakeEvent) string {
	var b strings.Builder
	b.WriteString(e.Kind)
	if e.Actor != "" {
		b.WriteString(" actor=" + e.Actor)
	}
	var fields map[string]any
	if len(e.Data) > 0 && json.Unmarshal(e.Data, &fields) == nil {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			if k != "wake_id" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(" " + k + "=")
			switch v := fields[k].(type) {
			case string:
				if strings.ContainsAny(v, " \t\"=") || v == "" {
					b.WriteString(strconv.Quote(v))
				} else {
					b.WriteString(v)
				}
			default:
				raw, _ := json.Marshal(v)
				b.Write(raw)
			}
		}
	}
	return b.String()
}

func writeObsWake(w io.Writer, wake api.ObsWakeTimeline, rows []obsWakeRow, logCount int, logNote string) {
	header := fmt.Sprintf("wake %s", wake.WakeID)
	if wake.Subject != "" {
		header += " subject=" + wake.Subject
	}
	header += fmt.Sprintf(" events=%d logs=%d", len(wake.Events), logCount)
	if len(rows) > 0 {
		header += " span=" + rows[len(rows)-1].TS.Sub(rows[0].TS).Round(time.Millisecond).String()
	}
	_, _ = fmt.Fprintln(w, header)
	if wake.Truncated {
		_, _ = fmt.Fprintf(w, "note: only the first %d events are shown\n", api.ObsWakeTimelineEventsMax)
	}
	if logNote != "" {
		_, _ = fmt.Fprintln(w, "note: "+logNote)
	}
	var prev time.Time
	for i, r := range rows {
		delta := ""
		if i > 0 {
			delta = "+" + r.TS.Sub(prev).Round(time.Millisecond).String()
		}
		prev = r.TS
		_, _ = fmt.Fprintf(w, "%s %8s %-5s %s\n", r.TS.Format("15:04:05.000"), delta, r.Source, r.Text)
	}
}
