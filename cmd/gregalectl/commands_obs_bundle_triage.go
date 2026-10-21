package main

import (
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func validObsBundleTriageHistory(e api.ObsIncidentEvidenceResponse, limit int) bool {
	h := e.TriageHistory
	if h == nil {
		return true
	} // Old servers remain explicitly unknown in coverage.
	t := e.Incident.Triage
	if e.Incident.Type != "platform_alert" || t == nil || h.IncidentID != e.Incident.ID || h.SnapshotRevision != t.Revision || h.CurrentRevision != t.Revision || h.SnapshotRevision < 0 || h.SnapshotRevision > api.ObsAlertTriageRevisionMax || h.Limit != limit || len(h.Items) > limit || !h.GeneratedAt.Equal(e.GeneratedAt) || !h.SnapshotAt.Equal(h.GeneratedAt) || h.Truncated != (h.NextCursor != "") {
		return false
	}
	_, id, _ := strings.Cut(h.IncidentID, "/")
	if h.HistoryPath != operatorobs.TriageHistoryPath(id) {
		return false
	}
	c := h.Coverage
	if c.AvailableEntries < 0 || c.AvailableEntries > h.SnapshotRevision || c.MissingEntries != h.SnapshotRevision-c.AvailableEntries ||
		c.MissingEntries == 0 && (c.State != "complete" || c.Reason != "") || c.MissingEntries > 0 && (c.State != "gaps" || c.Reason != "missing_receipts") {
		return false
	}
	want := c.AvailableEntries
	if want > int64(limit) {
		want = int64(limit)
	}
	if int64(len(h.Items)) != want || h.Truncated != (c.AvailableEntries > int64(len(h.Items))) {
		return false
	}
	if c.AvailableEntries == 0 {
		if c.OldestAvailableRevision != nil || c.NewestAvailableRevision != nil {
			return false
		}
	} else if c.OldestAvailableRevision == nil || c.NewestAvailableRevision == nil || *c.OldestAvailableRevision < 1 || *c.NewestAvailableRevision > h.SnapshotRevision || *c.OldestAvailableRevision > *c.NewestAvailableRevision {
		return false
	}
	before := h.SnapshotRevision + 1
	for _, entry := range h.Items {
		if parsed, err := uuid.Parse(entry.ID); err != nil || parsed == uuid.Nil || entry.Revision < 1 || entry.Revision >= before || !api.AllowedObsAlertTriageAction(entry.Action) || entry.At.IsZero() {
			return false
		}
		if entry.TraceID != "" {
			if decoded, err := hex.DecodeString(entry.TraceID); err != nil || len(decoded) != 16 || strings.ToLower(entry.TraceID) != entry.TraceID {
				return false
			}
		}
		if entry.TraceID == "" && entry.TracePath != "" || entry.TraceID != "" && entry.TracePath != "/v1/admin/obs/traces/"+entry.TraceID {
			return false
		}
		before = entry.Revision
	}
	return true
}
