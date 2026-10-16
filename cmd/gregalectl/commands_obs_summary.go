package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsSummary(args []string) int {
	fs := flag.NewFlagSet(subObsSummary, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	token := fs.String("admin-token", "", "admin bearer (default: $FAAS_ADMIN_TOKEN)")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs summary: no positional arguments and a positive --timeout required")
		return 2
	}
	if *token == "" {
		*token = os.Getenv("FAAS_ADMIN_TOKEN")
	}
	if *token == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs summary: --admin-token (or $FAAS_ADMIN_TOKEN) required")
		return 2
	}
	var out api.ObsAlertWorkloadSummary
	if err := fetchObsBundleSection(context.Background(), &http.Client{Timeout: *timeout}, apidBase()+operatorobs.AlertWorkloadSummaryPath, *token, "summary", &out); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if out.AsOf.IsZero() || out.Scope != "retained_platform_alert_episodes" || out.AgeBasis != "episode_started_at" || out.Groups == nil || out.Collection.State == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs summary: invalid response")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}
	writeObsSummaryHuman(osStdout, out)
	return 0
}

func writeObsSummaryHuman(w io.Writer, s api.ObsAlertWorkloadSummary) {
	_, _ = fmt.Fprintf(w, "alert_workload scope=%s as_of=%s age_basis=%s collection=%s reason=%s retention=%s expired_episodes=%d\n", s.Scope, s.AsOf.UTC().Format(time.RFC3339), s.AgeBasis, s.Collection.CollectionState, s.Collection.Reason, s.Retention.State, s.Retention.ExpiredEpisodes)
	if s.Counts == nil {
		_, _ = fmt.Fprintln(w, "counts=unknown (no successful collection)")
		return
	}
	c := s.Counts
	_, _ = fmt.Fprintf(w, "total=%d firing=%d unknown=%d resolved=%d needs_attention=%d\n", c.Total, c.Firing, c.Unknown, c.Resolved, c.NeedsAttention)
	for _, g := range s.Groups {
		_, _ = fmt.Fprintf(w, "  status=%s severity=%s acknowledged=%t owned=%t snoozed=%t count=%d\n", g.Status, g.Severity, g.Acknowledged, g.Owned, g.Snoozed, g.Count)
	}
	for _, entry := range []struct {
		name   string
		oldest *api.ObsAlertWorkloadOldest
	}{{"oldest_unacknowledged", s.OldestUnacknowledged}, {"oldest_unowned", s.OldestUnowned}} {
		if o := entry.oldest; o != nil {
			_, _ = fmt.Fprintf(w, "%s incident=%s status=%s age_seconds=%d started_at=%s evidence_path=%s\n", entry.name, o.IncidentID, o.Status, o.AgeSeconds, o.StartedAt.UTC().Format(time.RFC3339), o.EvidencePath)
		} else {
			_, _ = fmt.Fprintf(w, "%s=none\n", entry.name)
		}
	}
}
