package internalmail

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func eventFeedRoute(path string) bool {
	if path == EventFeedOperationsPath || strings.HasPrefix(path, EventFeedOperationsPath+"/") {
		return true
	}
	if !strings.HasPrefix(path, ProducersPath+"/") {
		return false
	}
	_, rest, ok := strings.Cut(strings.TrimPrefix(path, ProducersPath+"/"), "/")
	return ok && (rest == "event-consumers" || strings.HasPrefix(rest, "event-consumers/"))
}
func (s *Service) registerEventFeed(mux *http.ServeMux) {
	base := ProducersPath + "/{producer}/event-consumers/{consumer}"
	mux.HandleFunc("POST "+base+"/pull", s.postEventFeedPull)
	mux.HandleFunc("POST "+base+"/ack", s.postEventFeedAck)
	mux.HandleFunc("GET "+base, s.getProducerEventConsumer)
	mux.HandleFunc("GET "+EventFeedOperationsPath+"/{producer}", s.getOperatorEventConsumers)
	mux.HandleFunc("GET "+EventFeedOperationsPath+"/{producer}/{consumer}", s.getOperatorEventConsumer)
	mux.HandleFunc("POST "+EventFeedOperationsPath+"/replay", s.postEventFeedReplay)
}
func eventFeedNoQuery(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return false
	}
	return true
}
func (s *Service) postEventFeedPull(w http.ResponseWriter, r *http.Request) {
	if !eventFeedNoQuery(w, r) {
		return
	}
	var input struct {
		Limit *int `json:"limit"`
	}
	if !decode(w, r, &input) {
		return
	}
	limit := api.InternalMailEventFeedPageSize
	if input.Limit != nil {
		limit = *input.Limit
	}
	batch, err := s.PullEventFeed(r.Context(), r.PathValue("producer"), r.PathValue("consumer"), limit)
	respond(w, batch, err, http.StatusOK)
}
func (s *Service) postEventFeedAck(w http.ResponseWriter, r *http.Request) {
	if !eventFeedNoQuery(w, r) {
		return
	}
	var input EventFeedAckInput
	if !decode(w, r, &input) {
		return
	}
	ack, err := s.AckEventFeed(r.Context(), r.PathValue("producer"), r.PathValue("consumer"), input)
	respond(w, ack, err, http.StatusOK)
}
func (s *Service) getProducerEventConsumer(w http.ResponseWriter, r *http.Request) {
	if !eventFeedNoQuery(w, r) {
		return
	}
	status, err := s.ProducerEventConsumer(r.Context(), r.PathValue("producer"), r.PathValue("consumer"))
	respond(w, status, err, http.StatusOK)
}
func (s *Service) getOperatorEventConsumers(w http.ResponseWriter, r *http.Request) {
	store, err := s.eventFeedOperations()
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	list, err := operatorList(r, false, false)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	page, err := store.eventConsumers(r.Context(), r.PathValue("producer"), list)
	respond(w, page, err, http.StatusOK)
}
func (s *Service) getOperatorEventConsumer(w http.ResponseWriter, r *http.Request) {
	if !eventFeedNoQuery(w, r) {
		return
	}
	store, err := s.eventFeedOperations()
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	if !validProducerID(r.PathValue("producer")) || !validEventConsumer(r.PathValue("consumer")) {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	status, err := store.eventConsumerStatus(r.Context(), r.PathValue("producer"), r.PathValue("consumer"))
	respond(w, status, err, http.StatusOK)
}
func (s *Service) postEventFeedReplay(w http.ResponseWriter, r *http.Request) {
	if !eventFeedNoQuery(w, r) {
		return
	}
	var input EventFeedReplayRequest
	if !decode(w, r, &input) {
		return
	}
	record, err := s.ReplayEventConsumer(r.Context(), input)
	respond(w, record, err, http.StatusOK)
}
