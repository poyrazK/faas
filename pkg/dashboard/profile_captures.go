package dashboard

import (
	"fmt"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ProfileCapturesView is the profiles-page panel for on-demand captures
// (ADR-967): a capture form and the app's recent captures.
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
}

type ProfileCaptureKindView struct {
	Kind        string
	Total       string
	Rows        []ProfileCaptureRow
	Empty       bool
	Error       string
	DownloadURL string
}

type ProfileCaptureRow struct {
	Name, Location, Self, Total string
	SelfPercent                 float64
}

// ProfileCaptureTopFunctions bounds the dashboard function table.
const ProfileCaptureTopFunctions = 25

func ProfileCaptureURL(slug, id string) string {
	return "/dashboard/apps/" + url.PathEscape(slug) + "/profiles/captures/" + url.PathEscape(id)
}

// BuildProfileCaptureKind summarizes one merged kind for the capture page.
func BuildProfileCaptureKind(slug, id string, v api.ProfileCaptureView) ProfileCaptureKindView {
	out := ProfileCaptureKindView{Kind: v.Kind, Total: FormatProfileAmount(v.Total, v.Unit), Empty: v.Empty,
		DownloadURL: ProfileCaptureURL(slug, id) + "/pprof?" + url.Values{"kind": {v.Kind}}.Encode()}
	for i, f := range v.Functions {
		if i >= ProfileCaptureTopFunctions {
			break
		}
		location := f.File
		if f.Line > 0 {
			location = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		row := ProfileCaptureRow{Name: f.Name, Location: location, Self: FormatProfileAmount(f.Self, v.Unit), Total: FormatProfileAmount(f.Total, v.Unit)}
		if v.Total > 0 {
			row.SelfPercent = 100 * float64(f.Self) / float64(v.Total)
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

// FormatProfileAmount renders CPU nanoseconds as a duration and heap bytes
// with binary units.
func FormatProfileAmount(v int64, unit string) string {
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
