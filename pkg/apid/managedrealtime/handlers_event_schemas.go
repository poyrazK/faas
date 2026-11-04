package managedrealtime

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ManagedRealtimeEventSchema(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.previewUnavailable(w, acct.ID, s.HistoryPreviewEnabled, "event schemas preview unavailable") {
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
	version, err := strconv.Atoi(r.PathValue("version"))
	event := r.PathValue("event_type")
	if err != nil || version < 1 || version > 1000000 || api.ValidateRealtimeNotificationCategory(event) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("event_type requires 1..64 lowercase letters, digits, dot, underscore or hyphen; version must be 1..1000000"))
		return
	}
	store, ok := s.Host.Store().(state.ManagedRealtimeEventSchemaStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("event schemas unavailable"))
		return
	}
	var row state.ManagedRealtimeEventSchema
	if r.Method == http.MethodPut {
		var req api.ManagedRealtimeEventSchemaRequest
		if httpkit.DecodeJSONSized(r, &req, 20<<10) != nil || state.ValidateManagedRealtimeEventSchema(req.Schema) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("schema requires valid JSON Schema Draft 2020-12, at most 16384 bytes, with local references only"))
			return
		}
		row, err = store.PutManagedRealtimeEventSchema(r.Context(), state.ManagedRealtimeEventSchema{EndpointID: ep.ID, Channel: ch, EventType: event, Version: version, Schema: req.Schema})
	} else {
		row, err = store.GetManagedRealtimeEventSchema(r.Context(), ep.ID, ch, event, version)
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Schema version is immutable", "register a new version instead of changing an existing schema"))
		return
	}
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "event schema not found")
		return
	}
	if err != nil {
		s.writeManagedRealtimeHistoryError(w, r, err)
		return
	}
	if r.Method == http.MethodPut {
		s.Host.EmitAudit(r.Context(), "realtime.event_schema_registered", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": ch, "event_type": event, "version": version})
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, api.ManagedRealtimeEventSchemaResponse{Channel: ch, EventType: row.EventType, Version: row.Version, Schema: row.Schema, CreatedAt: row.CreatedAt})
}
