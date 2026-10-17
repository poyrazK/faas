package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting/activity"
)

// Projection excludes manifests, hostnames, fingerprints and credentials. Records
// are observed together under one memory lock or one SQL statement snapshot.
type HostingActivityRecord struct {
	OtherPreviews bool
	Run           api.HostingCIRun
	Release       *api.HostingRelease
	Preview       *api.HostingPreview
}
type HostingActivityRecords struct {
	Records    []HostingActivityRecord
	NextCursor string
}
type HostingActivityReader interface {
	ListHostingActivityRecords(context.Context, string, string, string) (HostingActivityRecords, error)
}

func hostingActivityPage(rows []HostingActivityRecord) HostingActivityRecords {
	out := HostingActivityRecords{Records: rows}
	if len(rows) > api.HostingActivityPageSize {
		out.Records = rows[:api.HostingActivityPageSize]
		last := out.Records[len(out.Records)-1].Run
		out.NextCursor = activity.Cursor(last.CreatedAt, last.ID)
	}
	return out
}
