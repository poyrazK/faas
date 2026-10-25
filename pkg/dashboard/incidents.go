package dashboard

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const incidentTimeLayout = "2006-01-02 15:04 UTC"

// IncidentsData is the /dashboard/incidents page (ADR-20261022193512):
// everything open, then what resolved in the default window.
type IncidentsData struct {
	Enabled bool
	// Error names the sources that could not be read.
	Error     string
	Since     string
	Truncated bool
	Rows      []IncidentRow
}

// IncidentRow is one incident in the list.
type IncidentRow struct {
	ID, Status, Source, App, Title, Opened, Duration, AcknowledgedBy string
	Open                                                             bool
}

// NewIncidentsData turns the incident list into rows.
func NewIncidentsData(resp api.IncidentsResponse, now time.Time) IncidentsData {
	out := IncidentsData{Enabled: true, Since: resp.Since.UTC().Format(incidentTimeLayout), Truncated: resp.Truncated}
	if len(resp.UnavailableSources) > 0 {
		out.Error = "Could not read: " + strings.Join(resp.UnavailableSources, ", ") + "; the list may be missing incidents."
	}
	for _, inc := range resp.Incidents {
		out.Rows = append(out.Rows, IncidentRow{
			ID: inc.ID, Status: inc.Status, Source: strings.ReplaceAll(inc.Source, "_", " "), App: inc.App, Title: inc.Title,
			Opened: inc.OpenedAt.UTC().Format(incidentTimeLayout), Duration: incidentDuration(inc, now),
			AcknowledgedBy: inc.AcknowledgedBy, Open: inc.Status == api.IncidentStatusOpen,
		})
	}
	return out
}

// IncidentData is the /dashboard/incidents/{id} page.
type IncidentData struct {
	Enabled bool
	Error   string
	// CSRF guards the acknowledge and note forms.
	CSRF string
	IncidentRow
	Resolved, Metric, Impact, AcknowledgedAt string
	LogLine, LogSearch                       string
	Fires                                    []IncidentEntry
	Updates                                  []IncidentEntry
	Changes                                  []IncidentEntry
	ChangesTruncated                         bool
	Notes                                    []IncidentEntry
	// Unavailable names the evidence that could not be read.
	Unavailable  string
	NoteMaxChars int
}

// IncidentEntry is one timestamped line of evidence or one note.
type IncidentEntry struct {
	At, Label, Text string
}

// NewIncidentData turns an incident detail into the page.
func NewIncidentData(d api.IncidentDetail, now time.Time) IncidentData {
	list := NewIncidentsData(api.IncidentsResponse{Incidents: []api.Incident{d.Incident}}, now)
	inc := d.Incident
	out := IncidentData{Enabled: true, IncidentRow: list.Rows[0], Metric: inc.Metric, Impact: inc.Impact,
		Unavailable: strings.Join(d.UnavailableSources, ", "), ChangesTruncated: d.ChangesTruncated, NoteMaxChars: api.IncidentNoteMaxChars}
	if inc.ResolvedAt != nil {
		out.Resolved = inc.ResolvedAt.UTC().Format(incidentTimeLayout)
	}
	if inc.AcknowledgedAt != nil {
		out.AcknowledgedAt = inc.AcknowledgedAt.UTC().Format(incidentTimeLayout)
	}
	out.LogLine, out.LogSearch = api.IncidentLogSummary(d.LogContext)
	for _, f := range d.Fires {
		out.Fires = append(out.Fires, IncidentEntry{At: f.At.UTC().Format(incidentTimeLayout), Label: f.Status, Text: "observed " + strconv.FormatFloat(f.Observed, 'g', 6, 64)})
	}
	for _, u := range d.Updates {
		out.Updates = append(out.Updates, IncidentEntry{At: u.At.UTC().Format(incidentTimeLayout), Label: u.State, Text: u.Message})
	}
	for _, c := range d.Changes {
		out.Changes = append(out.Changes, IncidentEntry{At: c.At.UTC().Format(incidentTimeLayout), Label: c.Source, Text: c.Summary})
	}
	for _, n := range d.Notes {
		text := n.Body
		if n.Kind == api.IncidentNoteKindAcknowledged {
			text = "Acknowledged"
		}
		out.Notes = append(out.Notes, IncidentEntry{At: n.CreatedAt.UTC().Format(incidentTimeLayout), Label: n.Author, Text: text})
	}
	return out
}

// incidentDuration is how long the incident lasted, or has lasted so far.
func incidentDuration(inc api.Incident, now time.Time) string {
	end := now
	if inc.ResolvedAt != nil {
		end = *inc.ResolvedAt
	}
	d := end.Sub(inc.OpenedAt).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
