package managedrealtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ExportRealtimeConsumerGroupDeadLetters(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupDeadLetterExportStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("dead-letter export unavailable"))
		return
	}
	req, err := parseRealtimeDeadLetterExport(r.URL.Query())
	if err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid export cursor, filters or limit"))
		return
	}
	page, err := store.ExportRealtimeConsumerGroupDeadLetters(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), req)
	if errors.Is(err, state.ErrRealtimeDeadLetterExportChanged) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "realtime_dead_letter_export_changed", "Export bounds changed", "the group identity or assignment boundary changed; restart the export"))
		return
	}
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	body, err := encodeRealtimeDeadLetterExport(page)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not prepare bounded dead-letter export"))
		return
	}
	writeRealtimeDeadLetterExport(w, r, page, req, body)
}

func parseRealtimeDeadLetterExport(q url.Values) (state.RealtimeDeadLetterExportRequest, error) {
	var req state.RealtimeDeadLetterExportRequest
	for _, key := range []string{"group_id", "after_sequence", "through_sequence", "snapshot_at", "limit"} {
		if values, ok := q[key]; ok && (len(values) != 1 || values[0] == "") {
			return req, state.ErrRealtimeConsumerGroupInvalid
		}
	}
	req.GroupID = q.Get("group_id")
	req.AfterSequence = q.Get("after_sequence")
	req.ThroughSequence = q.Get("through_sequence")
	req.FailureCodes = q["failure_code"]
	if value := q.Get("snapshot_at"); value != "" {
		snapshot, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return req, state.ErrRealtimeConsumerGroupInvalid
		}
		req.SnapshotAt = &snapshot
	}
	req.Limit = api.RealtimeConsumerGroupDeadLetterExportDefaultLimit
	if value := q.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > api.RealtimeConsumerGroupDeadLetterExportMaxRows {
			return req, state.ErrRealtimeConsumerGroupInvalid
		}
		req.Limit = limit
	}
	return req, nil
}

func encodeRealtimeDeadLetterExport(page state.RealtimeDeadLetterExportPage) ([]byte, error) {
	if len(page.Records) > api.RealtimeConsumerGroupDeadLetterExportMaxRows || page.Manifest.Count != len(page.Records) {
		return nil, errors.New("invalid export page size")
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	if err := encoder.Encode(page.Manifest); err != nil {
		return nil, err
	}
	for _, record := range page.Records {
		if err := encoder.Encode(record); err != nil {
			return nil, err
		}
		if body.Len() > api.RealtimeConsumerGroupDeadLetterExportMaxBytes {
			return nil, errors.New("export page exceeds byte bound")
		}
	}
	if body.Len() > api.RealtimeConsumerGroupDeadLetterExportMaxBytes {
		return nil, errors.New("export manifest exceeds byte bound")
	}
	return body.Bytes(), nil
}

func writeRealtimeDeadLetterExport(w http.ResponseWriter, r *http.Request, page state.RealtimeDeadLetterExportPage, req state.RealtimeDeadLetterExportRequest, body []byte) {
	m := page.Manifest
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	filename := fmt.Sprintf("dead-letters-%s-after-%s.jsonl", m.GroupID, m.AfterSequence)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("X-Realtime-Export-Group-ID", m.GroupID)
	w.Header().Set("X-Realtime-Export-Through-Sequence", m.ThroughSequence)
	w.Header().Set("X-Realtime-Export-Next-After-Sequence", m.NextAfterSequence)
	w.Header().Set("X-Realtime-Export-Has-More", strconv.FormatBool(m.HasMore))
	w.Header().Set("X-Realtime-Export-Snapshot-At", m.SnapshotAt.Format(time.RFC3339Nano))
	if m.HasMore {
		w.Header().Set("Link", "<"+realtimeDeadLetterExportNextURL(r, req, m)+">; rel=\"next\"")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func realtimeDeadLetterExportNextURL(r *http.Request, req state.RealtimeDeadLetterExportRequest, m state.RealtimeDeadLetterExportManifest) string {
	q := url.Values{"group_id": {m.GroupID}, "through_sequence": {m.ThroughSequence}, "snapshot_at": {m.SnapshotAt.Format(time.RFC3339Nano)}, "after_sequence": {m.NextAfterSequence}, "limit": {strconv.Itoa(req.Limit)}}
	for _, code := range req.FailureCodes {
		q.Add("failure_code", code)
	}
	for _, key := range []string{"endpoint_id", "channel", "group"} {
		if values := r.URL.Query()[key]; len(values) == 1 {
			q.Set(key, values[0])
		}
	}
	return r.URL.EscapedPath() + "?" + q.Encode()
}
