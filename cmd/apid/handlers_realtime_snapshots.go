package main

import (
	"encoding/base64"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) managedRealtimeChannelSnapshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "channel snapshots preview unavailable")
		return
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	ch := r.PathValue("channel")
	if problem := validateManagedRealtimeChannel(ch); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.store.(state.ManagedRealtimeSnapshotStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("channel snapshots unavailable"))
		return
	}
	var j state.ManagedRealtimeChannelSnapshot
	var err error
	switch r.Method {
	case http.MethodPut:
		var req api.ManagedRealtimeChannelSnapshotRequest
		if decodeJSONSized(r, &req, 90<<10) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid snapshot"))
			return
		}
		data, e := base64.StdEncoding.DecodeString(req.DataBase64)
		if e != nil || len(data) > state.ManagedRealtimeSnapshotMaxBytes {
			api.WriteProblem(w, api.ErrRealtimeInvalid("snapshot must contain at most 65536 bytes of base64 data"))
			return
		}
		j, err = store.PutManagedRealtimeChannelSnapshot(r.Context(), state.ManagedRealtimeChannelSnapshot{EndpointID: ep.ID, Channel: ch, Sequence: req.Sequence, Data: data, Binary: req.Binary})
	case http.MethodDelete:
		err = store.DeleteManagedRealtimeChannelSnapshot(r.Context(), ep.ID, ch)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
	default:
		j, err = store.GetManagedRealtimeChannelSnapshot(r.Context(), ep.ID, ch)
	}
	if errors.Is(err, state.ErrManagedRealtimeDurableCursorExpired) {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, "snapshot_unavailable", "Snapshot unavailable", "snapshot has expired or its sequence no longer has a replayable tail; publish a fresh snapshot"))
		return
	}
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "channel snapshot not found")
		return
	}
	if err != nil {
		s.writeManagedRealtimeHistoryError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.ManagedRealtimeChannelSnapshotResponse{Channel: ch, Sequence: j.Sequence, ResumeAfterSequence: j.Sequence, DataBase64: base64.StdEncoding.EncodeToString(j.Data), Binary: j.Binary, UpdatedAt: j.UpdatedAt, ExpiresAt: j.ExpiresAt, EntityVersions: j.EntityVersions, EntityExpirations: j.EntityExpirations})
}
