package dashboard

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ProfileCapturesView is the profiles-page panel for on-demand captures
// (ADR-1155): a capture form and the app's recent captures.
type ProfileCapturesView struct {
	AppSlug     string
	CSRF        string
	Captures    []api.ProfileCapture
	MaxDuration int
	Error       string
}

// ProfileCapturePage renders one capture. Refresh is set while the capture
// is queued or running so the page polls without JavaScript.
type ProfileCapturePage struct {
	AppSlug string
	Capture api.ProfileCapture
	Kinds   []ProfileCaptureKindView
	Refresh bool
	// Compare lists earlier ready captures the page can be diffed against.
	Compare []ProfileCaptureCompareOption
}

// ProfileCaptureCompareOption is one capture in the compare picker.
type ProfileCaptureCompareOption struct {
	ID, Label string
}

// AnnouncedProfileCapturesView is the app page banner for retained
// announced captures (ADR-1155): how many, how many an OOM kill followed,
// and the newest.
type AnnouncedProfileCapturesView struct {
	Count, OOMKilled int
	Days             int
	Latest           api.ProfileCapture
	LatestURL        string
	LatestTrigger    string
	ListURL          string
}

// BuildAnnouncedProfileCaptures summarizes captures, newest first, for the
// banner. It returns nil when none is announced.
func BuildAnnouncedProfileCaptures(slug string, captures []api.ProfileCapture) *AnnouncedProfileCapturesView {
	var out *AnnouncedProfileCapturesView
	for _, c := range captures {
		if !c.Announced() {
			continue
		}
		if out == nil {
			out = &AnnouncedProfileCapturesView{Days: int(api.ProfileCaptureRetention / (24 * time.Hour)), Latest: c,
				LatestURL: ProfileCaptureURL(slug, c.ID), LatestTrigger: ProfileCaptureTriggerLabel(c.Trigger),
				ListURL: "/dashboard/apps/" + url.PathEscape(slug) + "/profiles#profile-captures"}
		}
		out.Count++
		if c.OOMKilledAt != nil {
			out.OOMKilled++
		}
	}
	return out
}

// ProfileCaptureTriggerLabel names what started a capture.
func ProfileCaptureTriggerLabel(trigger string) string {
	switch trigger {
	case api.ProfileCaptureTriggerMemoryPressure:
		return "memory pressure"
	case api.ProfileCaptureTriggerSlowRequest:
		return "slow request"
	case api.ProfileCaptureTriggerCPUSaturation:
		return "CPU saturation"
	}
	return "manual"
}

// ProfileCaptureCompareOptions labels comparable captures for the picker.
func ProfileCaptureCompareOptions(captures []api.ProfileCapture) []ProfileCaptureCompareOption {
	out := make([]ProfileCaptureCompareOption, 0, len(captures))
	for _, c := range captures {
		label := c.CreatedAt.UTC().Format("2006-01-02 15:04Z") + " · " + strings.Join(c.Kinds, "+") + fmt.Sprintf(" · %ds", c.DurationSeconds)
		if c.Automatic() {
			label += " · " + ProfileCaptureTriggerLabel(c.Trigger)
		}
		if c.OOMKilledAt != nil {
			label += " · OOM-killed after"
		}
		out = append(out, ProfileCaptureCompareOption{ID: c.ID, Label: label})
	}
	return out
}

// ProfileCaptureDiffURL is the dashboard comparison of capture id against base.
func ProfileCaptureDiffURL(slug, id, base, kind string) string {
	q := url.Values{"base": {base}}
	if kind != "" {
		q.Set("kind", kind)
	}
	return ProfileCaptureURL(slug, id) + "/diff?" + q.Encode()
}

// ProfileCaptureDiffPage renders a capture comparison. Flamegraph is
// serialized into the page for the inline differential renderer.
type ProfileCaptureDiffPage struct {
	AppSlug        string
	Diff           api.ProfileCaptureDiff
	BaselineURL    string
	CandidateURL   string
	BaselineTotal  string
	CandidateTotal string
	Delta          string
	Rows           []ProfileCaptureDiffRow
	Flamegraph     *api.ProfileCaptureStackDelta
	Error          string
}

type ProfileCaptureDiffRow struct {
	Name, Location, Baseline, Candidate, Delta string
	Grew                                       bool
	SourceURL                                  string
}

// BuildProfileCaptureDiff summarizes a comparison for the diff page.
func BuildProfileCaptureDiff(slug string, d api.ProfileCaptureDiff) ProfileCaptureDiffPage {
	out := ProfileCaptureDiffPage{AppSlug: slug, Diff: d, BaselineURL: ProfileCaptureURL(slug, d.Baseline.ID), CandidateURL: ProfileCaptureURL(slug, d.Candidate.ID),
		BaselineTotal: FormatProfileAmount(d.BaselineTotal, d.Unit), CandidateTotal: FormatProfileAmount(d.CandidateTotal, d.Unit),
		Delta: FormatProfileDelta(d.Delta, d.Unit), Flamegraph: d.Flamegraph}
	for i, f := range d.Functions {
		if i >= ProfileCaptureTopFunctions {
			break
		}
		location := f.File
		if f.Line > 0 {
			location = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		row := ProfileCaptureDiffRow{Name: f.Name, Location: location, Baseline: FormatProfileAmount(f.Baseline, d.Unit),
			Candidate: FormatProfileAmount(f.Candidate, d.Unit), Delta: FormatProfileDelta(f.Delta, d.Unit), Grew: f.Delta > 0}
		if f.Source != nil {
			row.SourceURL = f.Source.URL
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

// FormatProfileDelta is FormatProfileAmount with an explicit sign.
func FormatProfileDelta(v int64, unit string) string {
	if v < 0 {
		return "−" + FormatProfileAmount(-v, unit)
	}
	return "+" + FormatProfileAmount(v, unit)
}

// ProfileCaptureKindView is one kind's table and flamegraph. Flamegraph and
// Unit are serialized into the page for the inline renderer.
type ProfileCaptureKindView struct {
	Kind        string                   `json:"kind"`
	Unit        string                   `json:"unit"`
	Total       string                   `json:"-"`
	Rows        []ProfileCaptureRow      `json:"-"`
	Empty       bool                     `json:"empty"`
	Error       string                   `json:"-"`
	DownloadURL string                   `json:"-"`
	Flamegraph  *api.ProfileCaptureStack `json:"flamegraph,omitempty"`
}

type ProfileCaptureRow struct {
	Name, Location, Self, Total string
	SelfPercent                 float64
	SourceURL                   string
}

// ProfileCaptureTopFunctions bounds the dashboard function table.
const ProfileCaptureTopFunctions = 25

func ProfileCaptureURL(slug, id string) string {
	return "/dashboard/apps/" + url.PathEscape(slug) + "/profiles/captures/" + url.PathEscape(id)
}

// BuildProfileCaptureKind summarizes one merged kind for the capture page.
func BuildProfileCaptureKind(slug, id string, v api.ProfileCaptureView) ProfileCaptureKindView {
	out := ProfileCaptureKindView{Kind: v.Kind, Unit: v.Unit, Total: FormatProfileAmount(v.Total, v.Unit), Empty: v.Empty,
		DownloadURL: ProfileCaptureURL(slug, id) + "/pprof?" + url.Values{"kind": {v.Kind}}.Encode()}
	if !v.Empty {
		out.Flamegraph = v.Flamegraph
	}
	for i, f := range v.Functions {
		if i >= ProfileCaptureTopFunctions {
			break
		}
		location := f.File
		if f.Line > 0 {
			location = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		row := ProfileCaptureRow{Name: f.Name, Location: location, Self: FormatProfileAmount(f.Self, v.Unit), Total: FormatProfileAmount(f.Total, v.Unit)}
		if f.Source != nil {
			row.SourceURL = f.Source.URL
		}
		if v.Total > 0 {
			row.SelfPercent = 100 * float64(f.Self) / float64(v.Total)
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

// FormatProfileAmount renders CPU nanoseconds as a duration, heap bytes
// with binary units and stack counts as threads.
func FormatProfileAmount(v int64, unit string) string {
	if unit == "count" {
		if v == 1 {
			return "1 thread"
		}
		return fmt.Sprintf("%d threads", v)
	}
	if unit != "bytes" {
		return time.Duration(v).Round(time.Microsecond).String()
	}
	switch {
	case v >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(v)/(1<<30))
	case v >= 1<<20:
		return fmt.Sprintf("%.2f MiB", float64(v)/(1<<20))
	case v >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(v)/(1<<10))
	}
	return fmt.Sprintf("%d B", v)
}
